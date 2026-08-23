package pebbledb

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/codec/betterbinary"
	"github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/bloom"
)

// Options configures a PebbleStore. The zero value is valid and targets a
// 2-core / 2GB-RAM node (see DefaultOptions).
type Options struct {
	// Dir is the data directory. Required.
	Dir string

	// CacheSize is the Pebble block cache size in bytes. Default 128 MiB.
	CacheSize int64

	// MemTableSize in bytes. Default 32 MiB. Total memtable memory is
	// capped at 2x this value.
	MemTableSize uint64

	// WALSync controls durability of each committed write group.
	// true  -> fsync per group (survives OS crash)
	// false -> fsync in background (survives process crash, may lose the
	//          last write group on OS crash; much higher ingest rate)
	// Default true.
	WALSync bool

	// MaxLimit caps the per-filter limit. A filter with limit=0 or
	// limit>MaxLimit is clamped to MaxLimit. Default 1000. 0 disables
	// clamping (dangerous on a public relay).
	MaxLimit int

	// MaxConcurrentQueries bounds how many queries scan the store at the
	// same time; the rest queue. Protects the block cache and iterators
	// on small machines. Default 16.
	MaxConcurrentQueries int

	// MaxScanKeys bounds how many index keys a single Query/Count call may
	// step through before it is aborted with ErrScanBudgetExceeded.
	// Default 500000.
	MaxScanKeys int

	// WriteGroupMax and WriteGroupInterval control group commit:
	// incoming writes accumulate until either is hit, then commit in one
	// batch with a single WAL fsync. Defaults 512 / 2ms.
	WriteGroupMax      int
	WriteGroupInterval time.Duration

	// IndexedTags decides which tag names get a 't' index entry.
	// nil means "all single-letter tag names" (NIP-01 convention).
	IndexedTags map[string]bool

	// MaxConcurrentCompactions caps background compactions.
	// Default 1 (leave a core for queries on a 2C2G box).
	MaxConcurrentCompactions int
}

// DefaultOptions returns Options tuned for 2 cores and 2 GB RAM:
//
//	block cache   128 MiB
//	memtables     <= 64 MiB
//	compactions   1 concurrent
//	everything else is query working set + Go GC headroom
func DefaultOptions(dir string) Options {
	return Options{
		Dir:                      dir,
		CacheSize:                128 << 20,
		MemTableSize:             32 << 20,
		WALSync:                  true,
		MaxLimit:                 1000,
		MaxConcurrentQueries:     16,
		MaxScanKeys:              500_000,
		WriteGroupMax:            512,
		WriteGroupInterval:       2 * time.Millisecond,
		MaxConcurrentCompactions: 1,
	}
}

// HighPerfOptions returns Options tuned for 4+ cores and 8+ GB RAM: bigger
// caches, more concurrent queries and compactions, larger write groups.
// Suitable for public relays under sustained high write+read load.
func HighPerfOptions(dir string) Options {
	return Options{
		Dir:                      dir,
		CacheSize:                2 << 30,
		MemTableSize:             128 << 20,
		WALSync:                  true,
		MaxLimit:                 5000,
		MaxConcurrentQueries:     64,
		MaxScanKeys:              2_000_000,
		WriteGroupMax:            2048,
		WriteGroupInterval:       2 * time.Millisecond,
		MaxConcurrentCompactions: 4,
	}
}

var (
	ErrClosed = errors.New("relaystore: store is closed")
	// ErrInvalidTimestamp is returned by SaveEvent/ReplaceEvent when the
	// event's CreatedAt is negative; the key encoding uses big-endian
	// uint64, so negative values would wrap and corrupt range scans.
	ErrInvalidTimestamp = errors.New("relaystore: negative CreatedAt is not allowed")
	// ErrScanBudgetExceeded is returned when a query stepped over more
	// index keys than Options.MaxScanKeys allows. It protects a small
	// node from pathological filters (e.g. COUNT over a huge time range).
	ErrScanBudgetExceeded = errors.New("relaystore: scan budget exceeded")
)

// Stats are cumulative counters since Open.
type Stats struct {
	EventsStored  int64
	EventsDup     int64
	EventsDeleted int64
	Queries       int64
	Counts        int64
	KeysScanned   int64
	EventsLoaded  int64

	// Histogram snapshots (counts per bucket). Bucket boundaries for
	// latency histograms: 1ms, 5ms, 10ms, 25ms, 50ms, 100ms, 250ms,
	// 1s, +Inf. GroupSize buckets: 1, 2-4, 5-16, 17-64, 65-256,
	// 257-1024, 1025-4096, 4097-16384, 16385+.
	CommitHistSnapshot    [9]int64
	LoadBodyHistSnapshot  [9]int64
	QueryHistSnapshot     [9]int64
	GroupSizeHistSnapshot [9]int64
}

// hist is an internal concurrency-safe histogram with 9 buckets.
type hist struct {
	b [9]atomic.Int64
}

func (h *hist) record(us int64) {
	switch {
	case us < 1000:
		h.b[0].Add(1)
	case us < 5000:
		h.b[1].Add(1)
	case us < 10000:
		h.b[2].Add(1)
	case us < 25000:
		h.b[3].Add(1)
	case us < 50000:
		h.b[4].Add(1)
	case us < 100000:
		h.b[5].Add(1)
	case us < 250000:
		h.b[6].Add(1)
	case us < 1000000:
		h.b[7].Add(1)
	default:
		h.b[8].Add(1)
	}
}

func (h *hist) recordGroupSz(sz int64) {
	switch {
	case sz < 2:
		h.b[0].Add(1)
	case sz < 5:
		h.b[1].Add(1)
	case sz < 17:
		h.b[2].Add(1)
	case sz < 65:
		h.b[3].Add(1)
	case sz < 257:
		h.b[4].Add(1)
	case sz < 1025:
		h.b[5].Add(1)
	case sz < 4097:
		h.b[6].Add(1)
	case sz < 16385:
		h.b[7].Add(1)
	default:
		h.b[8].Add(1)
	}
}

func (h *hist) snapshot() [9]int64 {
	var s [9]int64
	for i := range h.b {
		s[i] = h.b[i].Load()
	}
	return s
}

// writeReq is one mutation: a save (del=false, replace=false, ev+raw set),
// a delete (del=true, ev is the fully-loaded event being removed), or a
// replace (replace=true, ev is the new event; existing versions are
// discovered by the writer goroutine itself). Routing replaces through the
// same write pipeline guarantees same-key replaces are serialized FIFO by
// the single writer.
type writeReq struct {
	ev      *nostr.Event
	raw     []byte
	bloom   []byte // content bloom signature ('F' value); nil when content <3B
	del     bool
	replace bool
	res     chan writeRes
}

type writeRes struct {
	stored  bool
	deleted []nostr.Event // replace=true: what was actually deleted
	err     error
}

// PebbleStore is a persistent Nostr event database backed by Pebble
// (LSM tree). It implements the Store interface and is safe for
// concurrent use.
type PebbleStore struct {
	opts Options
	db   *pebble.DB
	cch  *pebble.Cache

	writeCh chan *writeReq
	wg      sync.WaitGroup
	closed  atomic.Bool

	qsem chan struct{} // query concurrency gate

	// bloomMinTS is the created_at of the oldest content-signature key, or
	// MaxInt64 when none exist. Every event with content >= 3 bytes and
	// created_at >= bloomMinTS has an 'F' entry, so search scans can trust
	// the signature index from this point on (see computeBloomMinTS /
	// noteBloomMin).
	bloomMinTS atomic.Int64

	commitHist    hist
	loadBodyHist  hist
	queryHist     hist
	groupSizeHist hist

	stats struct {
		stored  atomic.Int64
		dup     atomic.Int64
		deleted atomic.Int64
		queries atomic.Int64
		counts  atomic.Int64
		scanned atomic.Int64
		loaded  atomic.Int64
	}
}

// Discard logger and tracers.
type NoopLoggerAndTracer struct{}

var _ pebble.LoggerAndTracer = NoopLoggerAndTracer{}

func (l NoopLoggerAndTracer) Infof(format string, args ...interface{}) {}

func (l NoopLoggerAndTracer) Errorf(format string, args ...interface{}) {}

func (l NoopLoggerAndTracer) Fatalf(format string, args ...interface{}) {}

func (l NoopLoggerAndTracer) Eventf(ctx context.Context, format string, args ...interface{}) {
}

func (l NoopLoggerAndTracer) IsTracingEnabled(ctx context.Context) bool {
	return false
}

// Open opens (or creates) a store and starts the write pipeline.
func Open(opts Options) (*PebbleStore, error) {
	if opts.Dir == "" {
		return nil, errors.New("relaystore: Options.Dir is required")
	}
	d := DefaultOptions(opts.Dir)
	// fill unset fields with defaults
	if opts.CacheSize == 0 {
		opts.CacheSize = d.CacheSize
	}
	if opts.MemTableSize == 0 {
		opts.MemTableSize = d.MemTableSize
	}
	if opts.MaxLimit == 0 {
		opts.MaxLimit = d.MaxLimit
	}
	if opts.MaxConcurrentQueries == 0 {
		opts.MaxConcurrentQueries = d.MaxConcurrentQueries
	}
	if opts.MaxScanKeys == 0 {
		opts.MaxScanKeys = d.MaxScanKeys
	}
	if opts.WriteGroupMax == 0 {
		opts.WriteGroupMax = d.WriteGroupMax
	}
	if opts.WriteGroupInterval == 0 {
		opts.WriteGroupInterval = d.WriteGroupInterval
	}
	if opts.MaxConcurrentCompactions == 0 {
		opts.MaxConcurrentCompactions = d.MaxConcurrentCompactions
	}

	cch := pebble.NewCache(opts.CacheSize)
	maxComp := opts.MaxConcurrentCompactions
	popts := &pebble.Options{
		Cache:                       cch,
		MemTableSize:                opts.MemTableSize,
		MemTableStopWritesThreshold: 2,
		MaxOpenFiles:                512,
		WALBytesPerSync:             1 << 20,
		Merger:                      CounterMerger,
		// pebble/v2 caps background compaction concurrency through a
		// [lower, upper] range instead of the old MaxConcurrentCompactions
		CompactionConcurrencyRange: func() (int, int) { return 1, maxComp },
		// discard logging and tracing
		Logger:          NoopLoggerAndTracer{},
		LoggerAndTracer: NoopLoggerAndTracer{},
	}
	// Pebble defaults ship without bloom filters and with 2MB target files,
	// which is fine for tiny KV workloads but brutal here: every point get
	// (event body load, write dedup check) would probe several levels, and a
	// multi-GB store would shatter into hundreds of small SSTs, making every
	// iterator expensive to create. Bloom filters short-circuit point gets;
	// in v2, TargetFileSizes[0] sets L0's file size and each later level
	// defaults to doubling it, so 16MB here keeps every level's files large.
	popts.TargetFileSizes[0] = 16 << 20
	for i := range popts.Levels {
		popts.Levels[i].FilterPolicy = bloom.FilterPolicy(10)
	}
	db, err := pebble.Open(opts.Dir, popts)
	if err != nil {
		cch.Unref()
		return nil, fmt.Errorf("relaystore: open pebble: %w", err)
	}

	s := &PebbleStore{
		opts:    opts,
		db:      db,
		cch:     cch,
		writeCh: make(chan *writeReq, opts.WriteGroupMax*4),
		qsem:    make(chan struct{}, opts.MaxConcurrentQueries),
	}
	s.bloomMinTS.Store(math.MaxInt64)
	s.bloomMinTS.Store(s.computeBloomMinTS())
	s.wg.Add(1)
	go s.writerLoop()
	return s, nil
}

// OpenDir opens a store at dir with the default 2C2G-tuned options.
func OpenDir(dir string) (*PebbleStore, error) { return Open(DefaultOptions(dir)) }

// Close drains the write pipeline and closes the database.
func (s *PebbleStore) Close() error {
	if s.closed.Swap(true) {
		return nil
	}
	close(s.writeCh)
	s.wg.Wait()
	err := s.db.Close()
	s.cch.Unref()
	return err
}

// Stats returns cumulative counters.
func (s *PebbleStore) Stats() Stats {
	return Stats{
		EventsStored:  s.stats.stored.Load(),
		EventsDup:     s.stats.dup.Load(),
		EventsDeleted: s.stats.deleted.Load(),
		Queries:       s.stats.queries.Load(),
		Counts:        s.stats.counts.Load(),
		KeysScanned:   s.stats.scanned.Load(),
		EventsLoaded:  s.stats.loaded.Load(),

		CommitHistSnapshot:    s.commitHist.snapshot(),
		LoadBodyHistSnapshot:  s.loadBodyHist.snapshot(),
		QueryHistSnapshot:     s.queryHist.snapshot(),
		GroupSizeHistSnapshot: s.groupSizeHist.snapshot(),
	}
}

// Metrics exposes the underlying Pebble metrics (LSM shape, cache hit
// rate, compaction debt) for monitoring.
func (s *PebbleStore) Metrics() *pebble.Metrics { return s.db.Metrics() }

// Compact forces a full compaction of the LSM down to the bottom level.
// Call it after a bulk import: it collapses the fragmented LSM (and drops
// shadowed index entries) so queries see few, large SSTs instead of
// hundreds of small ones.
func (s *PebbleStore) Compact() error {
	return s.db.Compact(context.Background(), []byte{0x00}, []byte{0xFF}, true)
}

// computeBloomMinTS finds the created_at of the oldest content-signature key,
// or MaxInt64 when no signatures exist yet (e.g. a database created before
// the feature). Two iterator seeks make this cheap even on huge databases.
func (s *PebbleStore) computeBloomMinTS() int64 {
	it, err := s.db.NewIter(&pebble.IterOptions{LowerBound: bloomPrefix()})
	if err != nil {
		return math.MaxInt64
	}
	defer it.Close()
	if !it.First() {
		return math.MaxInt64
	}
	k := it.Key()
	if len(k) < 1+tsLen {
		return math.MaxInt64
	}
	return int64(binary.BigEndian.Uint64(k[1 : 1+tsLen]))
}

// noteBloomMin lowers bloomMinTS to ts when a new signature key is written
// with an older created_at than any seen so far. Called from the writer
// goroutine so the signature index stays trustworthy between opens.
func (s *PebbleStore) noteBloomMin(ts uint64) {
	for {
		cur := s.bloomMinTS.Load()
		if cur <= int64(ts) {
			return
		}
		if s.bloomMinTS.CompareAndSwap(cur, int64(ts)) {
			return
		}
	}
}

func (s *PebbleStore) indexedTag(name string) bool {
	if s.opts.IndexedTags == nil {
		return len(name) == 1
	}
	return s.opts.IndexedTags[name]
}

func (s *PebbleStore) Init(ctx context.Context) error {
	return nil
}

// SaveEvent persists an event and all its index entries.
// Saving an already-known id is an idempotent no-op (nil error).
func (s *PebbleStore) SaveEvent(ctx context.Context, ev nostr.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.save(&ev)
	return err
}

// save is the write-pipeline entry point; stored=false means the id
// already existed (dedup by id).
func (s *PebbleStore) save(ev *nostr.Event) (bool, error) {
	if s.closed.Load() {
		return false, ErrClosed
	}
	if ev.CreatedAt < 0 {
		return false, ErrInvalidTimestamp
	}
	bin := make([]byte, betterbinary.Measure(*ev))
	if err := betterbinary.Marshal(*ev, bin); err != nil {
		return false, err
	}
	req := &writeReq{ev: ev, raw: bin, bloom: contentBloom(ev.Content), res: make(chan writeRes, 1)}
	select {
	case s.writeCh <- req:
	default:
		// backpressure: block instead of dropping
		s.writeCh <- req
	}
	r := <-req.res
	return r.stored, r.err
}

// writerLoop is the single writer goroutine: it coalesces pending mutations
// (saves AND deletes) into one Pebble batch per group, so N events cost ~1
// WAL fsync. Routing deletes through the same pipeline keeps the rollup
// counters race-free.
func (s *PebbleStore) writerLoop() {
	defer s.wg.Done()
	group := make([]*writeReq, 0, s.opts.WriteGroupMax)
	for {
		first, ok := <-s.writeCh
		if !ok {
			return // closed
		}
		group = append(group[:0], first)

		// drain any backlog without waiting
	backlog:
		for len(group) < s.opts.WriteGroupMax {
			select {
			case r, ok := <-s.writeCh:
				if !ok {
					s.commitGroup(group)
					return
				}
				group = append(group, r)
			default:
				break backlog
			}
		}

		if len(group) <= 1 {
			// single write, no backlog — commit immediately
			s.commitGroup(group)
			continue
		}

		// backlog exists: wait up to WriteGroupInterval for more
		timer := time.NewTimer(s.opts.WriteGroupInterval)
	collect:
		for len(group) < s.opts.WriteGroupMax {
			select {
			case r, ok := <-s.writeCh:
				if !ok {
					timer.Stop()
					s.commitGroup(group)
					return
				}
				group = append(group, r)
			case <-timer.C:
				break collect
			}
		}
		timer.Stop()
		s.commitGroup(group)
	}
}

func (s *PebbleStore) commitGroup(group []*writeReq) {
	t0 := time.Now()
	defer func() {
		s.groupSizeHist.recordGroupSz(int64(len(group)))
		s.commitHist.record(time.Since(t0).Microseconds())
	}()
	batch := s.db.NewBatch()
	defer batch.Close()

	type pending struct {
		req     *writeReq
		deleted []nostr.Event // replace=true: versions actually deleted
	}
	pend := make([]pending, 0, len(group))
	seenSave := make(map[nostr.ID]struct{}, len(group))
	seenDel := make(map[nostr.ID]struct{}, len(group))
	// In-group replace ledger: tracks the running newest event for each
	// (pubkey,kind,dHash) tuple seen in this group, plus the events that
	// were superseded and must be deleted at commit time. Without this,
	// N concurrent same-key replaces coalesced into one group would all
	// see an empty DB and all survive.
	type replaceKey struct {
		pk    nostr.PubKey
		kind  uint32
		dHash string // empty for replaceable; d-tag value for addressable
	}
	type replaceState struct {
		newest     *nostr.Event   // running newest in DB+group
		newestInDB bool           // whether newest came from DB (vs a prior replace in this group)
		toDelete   []*nostr.Event // events to delete at commit
	}
	replaces := make(map[replaceKey]*replaceState, 4)
	mkReplaceKey := func(ev *nostr.Event) replaceKey {
		k := replaceKey{pk: ev.PubKey, kind: uint32(ev.Kind)}
		if ev.Kind.IsAddressable() {
			k.dHash = ev.Tags.GetD()
		}
		return k
	}

	// Pre-pass: concurrent dedup check for plain saves. Pebble has no
	// MultiGet, but db.Get is goroutine-safe and bloom-filtered, so a small
	// worker pool beats serial lookups when the group is large. The map
	// stores id -> exists; lookups that errored are simply absent from the
	// map and the main loop falls through to the batch.Set path (Pebble
	// will surface the error there if it persists).
	const dedupWorkers = 8
	saveDedup := make(map[nostr.ID]bool, len(group))
	{
		var wg sync.WaitGroup
		var mu sync.Mutex
		jobs := make(chan nostr.ID, len(group))
		for w := 0; w < dedupWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for jid := range jobs {
					_, cl, err := s.db.Get(locatorKey(jid[:]))
					if cl != nil {
						cl.Close()
					}
					if err != nil && !errors.Is(err, pebble.ErrNotFound) {
						continue
					}
					mu.Lock()
					saveDedup[jid] = (err == nil)
					mu.Unlock()
				}
			}()
		}
		for _, req := range group {
			if !req.del && !req.replace {
				jobs <- req.ev.ID
			}
		}
		close(jobs)
		wg.Wait()
	}
	// rollup counter deltas for this whole group, applied as one
	// read-modify-write per counter key
	counterDelta := make(map[string]int64, 8)
	addDelta := func(key []byte, d int64) { counterDelta[string(key)] += d }
	bump := func(ev *nostr.Event, d int64) {
		day := dayOf(int64(ev.CreatedAt))
		hour := hourOf(int64(ev.CreatedAt))
		kind := uint32(ev.Kind)
		addDelta(counterKey(kindDayCounterPrefix(kind), day), d)
		addDelta(counterKey(dayCounterPrefix(), day), d)
		addDelta(counterKey(kindHourCounterPrefix(kind), hour), d)
		addDelta(counterKey(hourCounterPrefix(), hour), d)
		addDelta(pkKindCounterKey(ev.PubKey[:], kind), d)
	}

	for _, req := range group {
		id := req.ev.ID
		lk := locatorKey(id[:])
		ts := uint64(req.ev.CreatedAt)

		if req.replace {
			// Replace: the writer goroutine is the only decision point, so
			// same-key replaces are strictly serialized FIFO. We maintain a
			// per-group ledger so that replace #2 sees the (uncommitted)
			// effect of replace #1 in the same group.
			rkey := mkReplaceKey(req.ev)
			st, ok := replaces[rkey]
			if !ok {
				// First replace for this key in the group: load DB state.
				existing, newest, err := s.findReplaceVersions(req.ev)
				if err != nil {
					req.res <- writeRes{err: err}
					continue
				}
				st = &replaceState{newest: newest, newestInDB: newest != nil}
				for _, e := range existing {
					st.toDelete = append(st.toDelete, e)
				}
				replaces[rkey] = st
			}

			// Decide: is req.ev strictly newer than the running newest?
			if st.newest != nil && st.newest.CreatedAt >= req.ev.CreatedAt {
				req.res <- writeRes{stored: false}
				continue
			}

			// Accept: req.ev becomes the running newest. Whatever was the
			// previous newest (DB-sourced or in-group) is now superseded by
			// req.ev. Attribute ALL pending deletions to req.ev: from the
			// caller's perspective, req.ev is the one that won and caused
			// every prior version to be removed.
			deleted := make([]nostr.Event, 0, len(st.toDelete)+1)
			for _, old := range st.toDelete {
				if old.ID == id {
					continue
				}
				if _, dup := seenDel[old.ID]; !dup {
					seenDel[old.ID] = struct{}{}
					deleted = append(deleted, *old)
				}
			}
			if st.newest != nil && st.newest.ID != id {
				if _, dup := seenDel[st.newest.ID]; !dup {
					seenDel[st.newest.ID] = struct{}{}
					deleted = append(deleted, *st.newest)
				}
			}
			// Reset ledger: the running newest is now req.ev; nothing else
			// pending deletion for this key yet.
			st.newest = req.ev
			st.newestInDB = false
			st.toDelete = st.toDelete[:0]

			// Dedup: same id saved twice in one group is a no-op.
			if _, dup := seenSave[id]; dup {
				s.stats.dup.Add(1)
				req.res <- writeRes{stored: false}
				continue
			}
			seenSave[id] = struct{}{}

			// Id-exists-in-DB check is unnecessary: even if the id is in DB,
			// we just re-Set (Pebble dedups by key; value is the same encTS).
			// Saves one point lookup per replace.
			if err := batch.Set(lk, encTS(ts), nil); err != nil {
				req.res <- writeRes{err: err}
				continue
			}
			if err := batch.Set(bodyKey(ts, id[:]), req.raw, nil); err != nil {
				req.res <- writeRes{err: err}
				continue
			}
			for _, k := range indexKeysFor(req.ev, s.indexedTag) {
				_ = batch.Set(k, nil, nil)
			}
			if req.bloom != nil {
				_ = batch.Set(bloomKey(ts, id[:]), req.bloom, nil)
				s.noteBloomMin(ts)
			}
			addHLLMerges(batch, req.ev)
			bump(req.ev, 1)
			// Apply deletions for the versions we just superseded. We delete
			// their locator, body, and index keys, and decrement counters.
			for i := range deleted {
				old := &deleted[i]
				oldLk := locatorKey(old.ID[:])
				oldTs := uint64(old.CreatedAt)
				_ = batch.Delete(oldLk, nil)
				_ = batch.Delete(bodyKey(oldTs, old.ID[:]), nil)
				_ = batch.Delete(bloomKey(oldTs, old.ID[:]), nil)
				for _, k := range indexKeysFor(old, s.indexedTag) {
					_ = batch.Delete(k, nil)
				}
				bump(old, -1)
			}
			pend = append(pend, pending{req: req, deleted: deleted})
			continue
		}

		if !req.del {
			if _, dup := seenSave[id]; dup {
				s.stats.dup.Add(1)
				req.res <- writeRes{stored: false}
				continue
			}
			seenSave[id] = struct{}{}

			// Dedup against DB: the pre-pass already did this concurrently.
			if exists, ok := saveDedup[id]; ok && exists {
				s.stats.dup.Add(1)
				req.res <- writeRes{stored: false}
				continue
			}

			if err := batch.Set(lk, encTS(ts), nil); err != nil {
				req.res <- writeRes{err: err}
				continue
			}
			if err := batch.Set(bodyKey(ts, id[:]), req.raw, nil); err != nil {
				req.res <- writeRes{err: err}
				continue
			}
			for _, k := range indexKeysFor(req.ev, s.indexedTag) {
				_ = batch.Set(k, nil, nil)
			}
			if req.bloom != nil {
				_ = batch.Set(bloomKey(ts, id[:]), req.bloom, nil)
				s.noteBloomMin(ts)
			}
			addHLLMerges(batch, req.ev)
			bump(req.ev, 1)
			pend = append(pend, pending{req: req})
		} else {
			// delete: skip if already deleted (in this group or before)
			if _, dup := seenDel[id]; dup {
				req.res <- writeRes{stored: false}
				continue
			}
			seenDel[id] = struct{}{}
			_, cl, err := s.db.Get(lk)
			if errors.Is(err, pebble.ErrNotFound) {
				req.res <- writeRes{stored: false}
				continue
			}
			if err != nil {
				cl.Close()
				req.res <- writeRes{err: err}
				continue
			}
			cl.Close()

			_ = batch.Delete(lk, nil)
			_ = batch.Delete(bodyKey(ts, id[:]), nil)
			_ = batch.Delete(bloomKey(ts, id[:]), nil)
			for _, k := range indexKeysFor(req.ev, s.indexedTag) {
				_ = batch.Delete(k, nil)
			}
			bump(req.ev, -1)
			pend = append(pend, pending{req: req})
		}
	}

	// apply rollup counter deltas as pebble.Merge operands: the DB-owned
	// Merger sums them with any prior value at read/compaction time, so we
	// skip the per-key read-modify-write entirely.
	for ckey, delta := range counterDelta {
		if delta == 0 {
			continue
		}
		if err := batch.Merge([]byte(ckey), enc64(delta), nil); err != nil {
			for _, p := range pend {
				p.req.res <- writeRes{err: err}
			}
			return
		}
	}

	var syncOpt *pebble.WriteOptions
	if s.opts.WALSync {
		syncOpt = pebble.Sync
	} else {
		syncOpt = pebble.NoSync
	}
	err := batch.Commit(syncOpt)
	for _, p := range pend {
		if err == nil {
			if p.req.replace {
				s.stats.stored.Add(1)
				s.stats.deleted.Add(int64(len(p.deleted)))
			} else if p.req.del {
				s.stats.deleted.Add(1)
			} else {
				s.stats.stored.Add(1)
			}
		}
		p.req.res <- writeRes{stored: err == nil, deleted: p.deleted, err: err}
	}
}

// readCounter returns the current value of a rollup counter (0 if absent).
func (s *PebbleStore) readCounter(key []byte) (int64, error) {
	vb, cl, err := s.db.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer cl.Close()
	return dec64(vb), nil
}

// getEvent fetches one event by id (locator hop + body read); internal,
// used by the delete pipeline to derive the index keys being removed.
// Returns (nil, nil) when the id is unknown.
func (s *PebbleStore) getEvent(id nostr.ID) (*nostr.Event, error) {
	tsb, cl, err := s.db.Get(locatorKey(id[:]))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	ts := binary.BigEndian.Uint64(tsb)
	cl.Close()
	return s.loadBody(ts, id)
}

// DeleteEvent removes an event and all its index entries; deleting an
// unknown id is a no-op. NIP-09 authorization ("only the author may
// delete") is the relay layer's job: load via QueryEvents, compare
// pubkeys, then call DeleteEvent.
func (s *PebbleStore) DeleteEvent(ctx context.Context, id nostr.ID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.delete(id)
	return err
}

// delete is the delete pipeline entry.
func (s *PebbleStore) delete(id nostr.ID) (deleted bool, err error) {
	if s.closed.Load() {
		return false, ErrClosed
	}
	ev, err := s.getEvent(id)
	if err != nil || ev == nil {
		return false, err
	}
	req := &writeReq{ev: ev, del: true, res: make(chan writeRes, 1)}
	select {
	case s.writeCh <- req:
	default:
		s.writeCh <- req
	}
	r := <-req.res
	return r.stored, r.err
}

func (s *PebbleStore) acquire(ctx context.Context) error {
	select {
	case s.qsem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *PebbleStore) release() { <-s.qsem }

// ReplaceEvent implements NIP-01 replaceable/addressable semantics:
//
//   - non-replaceable kinds: plain save, deleted == nil
//   - replaceable (0, 3, 10000-19999): keeps the newest per (pubkey, kind)
//   - addressable (30000-39999): keeps the newest per (pubkey, kind, d-tag)
//
// If a newer (or equal) version already exists, the incoming event is NOT
// stored and deleted is empty. Otherwise the incoming event is stored and
// every superseded version is physically deleted and returned.
//
// The whole check-and-swap runs inside the single writer goroutine, so
// concurrent ReplaceEvent calls for the same (pubkey, kind[, d]) are
// strictly serialized FIFO — last writer wins is guaranteed.
func (s *PebbleStore) ReplaceEvent(ctx context.Context, ev nostr.Event) ([]nostr.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !ev.Kind.IsReplaceable() && !ev.Kind.IsAddressable() {
		return nil, s.SaveEvent(ctx, ev)
	}
	return s.replace(&ev)
}

// replace routes the whole replace operation through the write pipeline so
// the writer goroutine is the only decision point.
func (s *PebbleStore) replace(ev *nostr.Event) ([]nostr.Event, error) {
	if s.closed.Load() {
		return nil, ErrClosed
	}
	if ev.CreatedAt < 0 {
		return nil, ErrInvalidTimestamp
	}
	bin := make([]byte, betterbinary.Measure(*ev))
	if err := betterbinary.Marshal(*ev, bin); err != nil {
		return nil, err
	}
	req := &writeReq{ev: ev, raw: bin, bloom: contentBloom(ev.Content), replace: true, res: make(chan writeRes, 1)}
	select {
	case s.writeCh <- req:
	default:
		s.writeCh <- req
	}
	r := <-req.res
	return r.deleted, r.err
}

// findReplaceVersions queries the DB for all existing versions of the same
// (pubkey, kind[, d]) tuple as ev. Called only from the writer goroutine;
// the single-writer serialization means no concurrent replace can mutate
// the result between query and write.
func (s *PebbleStore) findReplaceVersions(ev *nostr.Event) (existing []*nostr.Event, newest *nostr.Event, err error) {
	f := nostr.Filter{
		Authors: []nostr.PubKey{ev.PubKey},
		Kinds:   []nostr.Kind{ev.Kind},
	}
	if ev.Kind.IsAddressable() {
		f.Tags = nostr.TagMap{"d": {ev.Tags.GetD()}}
	}
	// use a fresh context: we are inside the writer goroutine and the
	// caller's ctx may already be cancelled; we still must finish the
	// replace to keep counters consistent.
	c, err := s.newFilterRun(&f, true, 0).openCursor(context.Background())
	if err != nil {
		return nil, nil, err
	}
	for c.valid {
		existing = append(existing, c.cur.ev)
		c.advance()
	}
	if err := c.finish(); err != nil {
		return nil, nil, err
	}
	for _, e := range existing {
		if newest == nil || e.CreatedAt > newest.CreatedAt {
			newest = e
		}
	}
	return existing, newest, nil
}
