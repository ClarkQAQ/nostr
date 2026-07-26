package pebbledb

import (
	"bytes"
	"container/heap"
	"context"
	"encoding/binary"
	"errors"
	"iter"
	"math"
	"sort"
	"sync"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"fiatjaf.com/nostr/eventstore/codec/betterbinary"
	"github.com/cockroachdb/pebble/v2"
)

// The query engine serves exactly one shape of query — the Store
// interface's: a single filter, results streamed newest-first, limits
// enforced during the scan. There is deliberately no second merge level,
// no multi-filter OR, and no raw/materializing variants: a relay composes
// those out of QueryEvents if it wants them.

type planMode int

const (
	modeIDs planMode = iota
	modeAuthors
	modeTags
	modeKinds
	modeTime
)

// ErrSearchUnsupported is yielded/returned when a Filter carries a NIP-50
// Search string: this backend does not implement full-text search and
// refuses to silently return unfiltered results.
var ErrSearchUnsupported = errors.New("relaystore: NIP-50 search is not supported by this backend")

var _ eventstore.Store = (*PebbleStore)(nil)

// QueryEvents streams events matching the filter, newest first, capped by
// maxLimit (and the filter's own Limit, whichever is smaller). Iteration is
// lazy: breaking out of the range loop closes all index iterators and
// releases the query slot immediately. Being always single-filter and
// descending, it drives the filter cursor directly — no second merge
// level, no merger heap, no cross-filter dedup bookkeeping.
func (s *PebbleStore) QueryEvents(ctx context.Context, filter nostr.Filter, maxLimit int) iter.Seq2[nostr.Event, error] {
	return func(yield func(nostr.Event, error) bool) {
		t0 := time.Now()
		defer func() { s.queryHist.record(time.Since(t0).Microseconds()) }()
		if filter.Search != "" {
			yield(nostr.Event{}, ErrSearchUnsupported)
			return
		}
		if err := s.acquire(ctx); err != nil {
			yield(nostr.Event{}, err)
			return
		}
		defer s.release()
		s.stats.queries.Add(1)

		c, err := s.newFilterRun(&filter, true, maxLimit).openCursor(ctx)
		if err != nil {
			yield(nostr.Event{}, err)
			return
		}
		for c.valid {
			// the cursor parses eagerly (get -> decode in place -> release),
			// so cur.ev is always non-nil here
			if !yield(*c.cur.ev, nil) {
				_ = c.finish()
				return
			}
			c.advance()
		}
		if err := c.finish(); err != nil {
			yield(nostr.Event{}, err)
		}
	}
}

// CountEvents returns the exact NIP-45 count for the filter.
func (s *PebbleStore) CountEvents(ctx context.Context, filter nostr.Filter) (int64, error) {
	if filter.Search != "" {
		return 0, ErrSearchUnsupported
	}
	if err := s.acquire(ctx); err != nil {
		return 0, err
	}
	defer s.release()
	s.stats.counts.Add(1)
	f := filter
	return s.newFilterRun(&f, false, 0).count(ctx)
}

// candidate is one accepted event at the cursor. ev is nil on count-only
// scans, which never load bodies unless a tag post-check requires them.
type candidate struct {
	ts uint64
	id nostr.ID
	ev *nostr.Event
}

// filterRun is the planner for a single filter.
type filterRun struct {
	s      *PebbleStore
	f      *nostr.Filter
	limit  int // effective per-filter limit; 0 = unlimited
	budget int64
	query  bool // true: bodies are loaded; false: count only

	scanned int64
}

func (s *PebbleStore) newFilterRun(f *nostr.Filter, forQuery bool, maxClamp int) *filterRun {
	limit := f.Limit
	if forQuery {
		if maxClamp <= 0 {
			maxClamp = s.opts.MaxLimit
		}
		if maxClamp > 0 && (limit <= 0 || limit > maxClamp) {
			limit = maxClamp
		}
	} else {
		limit = 0 // COUNT ignores limit: exact match count
	}
	return &filterRun{s: s, f: f, limit: limit, budget: int64(s.opts.MaxScanKeys), query: forQuery}
}

// plan picks the driving index. Priority: ids > authors > tags > kinds > time.
// The most selective available dimension drives the index scan; remaining
// predicates are post-checked in memory.
func (q *filterRun) plan() planMode {
	f := q.f
	switch {
	case len(f.IDs) > 0:
		return modeIDs
	case len(f.Authors) > 0:
		return modeAuthors
	case countTagPreds(f) > 0:
		return modeTags
	case len(f.Kinds) > 0:
		return modeKinds
	default:
		return modeTime
	}
}

func countTagPreds(f *nostr.Filter) int {
	n := 0
	for _, vals := range f.Tags {
		if len(vals) > 0 {
			n++
		}
	}
	return n
}

// sinceUntil maps the filter's Timestamp bounds onto the internal int64
// range; the zero Timestamp means "unbounded".
func (q *filterRun) sinceUntil() (int64, int64) {
	since, until := int64(0), int64(math.MaxInt64)
	if q.f.Since > 0 {
		since = int64(q.f.Since)
	}
	if q.f.Until > 0 {
		until = int64(q.f.Until)
	}
	return since, until
}

// count executes the filter and returns the exact match count.
func (q *filterRun) count(ctx context.Context) (int64, error) {
	// Fast path 0: ids filters are point lookups.
	if q.plan() == modeIDs {
		cands, err := q.runIDs(ctx)
		if err != nil {
			return 0, err
		}
		return int64(len(cands)), nil
	}
	// Fast path 1: unbounded author+kind counts are pure counter point reads.
	if q.plan() == modeAuthors && countTagPreds(q.f) == 0 &&
		q.f.Since <= 0 && q.f.Until <= 0 && len(q.f.Kinds) > 0 {
		var sum int64
		for _, a := range q.f.Authors {
			for _, kind := range q.f.Kinds {
				n, err := q.s.readCounter(pkKindCounterKey(a[:], uint32(kind)))
				if err != nil {
					return 0, err
				}
				sum += n
			}
		}
		return sum, nil
	}
	// Fast path 2: kind/time-only counts use the two-tier (hour+day) rollup
	// counters — a 24h COUNT is a handful of counter reads plus at most two
	// partial-hour scans, instead of a 250k-key full scan.
	if q.plan() == modeKinds || q.plan() == modeTime {
		if n, ok, err := q.countRollup(ctx); ok || err != nil {
			return n, err
		}
	}
	// Generic path: stream the index scan and count (bodies load only when
	// a tag post-check requires them).
	c, err := q.openCursor(ctx)
	if err != nil {
		return 0, err
	}
	var n int64
	for c.valid {
		n++
		c.advance()
	}
	return n, c.finish()
}

// countRollup answers kind/time COUNTs from the per-hour/per-day counters:
// full days and full hours are summed from counters (bounded prefix scans
// over a few hundred counter keys), at most two partial edge hours fall
// back to event-index scans. ok=false means the range is too narrow for
// rollups to pay off and the caller should scan instead.
func (q *filterRun) countRollup(ctx context.Context) (int64, bool, error) {
	since, until := q.sinceUntil()
	if since > until {
		return 0, true, nil
	}
	d0, d1 := dayOf(since), dayOf(until)
	if d0 == d1 {
		// same day: rollup pays off once the range covers full hours
		if h0, h1 := hourOf(since), hourOf(until); h1 < h0+2 {
			return 0, false, nil // narrow range: plain scan is already cheap
		}
	}

	kinds := q.f.Kinds // nil in modeTime -> use the all-kinds counters
	var sum int64

	// full days [d0+1, d1-1] from day counters
	if d1 > d0+1 {
		if len(kinds) == 0 {
			n, err := q.s.sumCounters(dayCounterPrefix(), d0+1, d1-1)
			if err != nil {
				return 0, true, err
			}
			sum += n
		} else {
			for _, kind := range kinds {
				n, err := q.s.sumCounters(kindDayCounterPrefix(uint32(kind)), d0+1, d1-1)
				if err != nil {
					return 0, true, err
				}
				sum += n
			}
		}
	}

	// partial edge days: full hours from hour counters, at most two
	// partial edge hours fall back to event-index scans
	edgeDay := func(day uint32) error {
		lo := int64(day) * secondsPerDay
		hi := lo + secondsPerDay - 1
		if since > lo {
			lo = since
		}
		if until < hi {
			hi = until
		}
		h0, h1 := hourOf(lo), hourOf(hi)
		if h1 > h0+1 {
			if len(kinds) == 0 {
				n, err := q.s.sumCounters(hourCounterPrefix(), h0+1, h1-1)
				if err != nil {
					return err
				}
				sum += n
			} else {
				for _, kind := range kinds {
					n, err := q.s.sumCounters(kindHourCounterPrefix(uint32(kind)), h0+1, h1-1)
					if err != nil {
						return err
					}
					sum += n
				}
			}
		}
		edgeHour := func(h uint32) error {
			hlo := int64(h) * secondsPerHour
			hhi := hlo + secondsPerHour - 1
			if lo > hlo {
				hlo = lo
			}
			if hi < hhi {
				hhi = hi
			}
			if len(kinds) == 0 {
				n, err := q.scanCountKeys(ctx, timePrefix(), hlo, hhi)
				if err == nil {
					sum += n
				}
				return err
			}
			for _, kind := range kinds {
				n, err := q.scanCountKeys(ctx, kindPrefix(uint32(kind)), hlo, hhi)
				if err != nil {
					return err
				}
				sum += n
			}
			return nil
		}
		if err := edgeHour(h0); err != nil {
			return err
		}
		if h1 != h0 {
			return edgeHour(h1)
		}
		return nil
	}
	if err := edgeDay(d0); err != nil {
		return 0, true, err
	}
	if d1 != d0 {
		if err := edgeDay(d1); err != nil {
			return 0, true, err
		}
	}
	return sum, true, nil
}

// sumCounters adds up counter values for days/hours [d0, d1] under a
// counter prefix (keys are prefix + bucket(4): one small prefix scan).
func (s *PebbleStore) sumCounters(prefix []byte, d0, d1 uint32) (int64, error) {
	lower, upper := timeBounds4(prefix, d0, d1)
	it, err := s.db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return 0, err
	}
	defer it.Close()
	var sum int64
	for valid := it.First(); valid; valid = it.Next() {
		sum += dec64(it.Value())
	}
	return sum, it.Error()
}

// scanCountKeys counts index keys under prefix within the ts range.
func (q *filterRun) scanCountKeys(ctx context.Context, prefix []byte, since, until int64) (int64, error) {
	lower, upper := timeBounds(prefix, since, until)
	it, err := q.s.db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return 0, err
	}
	defer it.Close()
	var n int64
	for valid := it.First(); valid; valid = it.Next() {
		if q.scanned >= q.budget {
			return 0, ErrScanBudgetExceeded
		}
		if q.scanned&0x3FF == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		q.scanned++
		n++
	}
	q.s.stats.scanned.Add(n)
	return n, it.Error()
}

// ----------------------------------------------------------------------

// scanSource wraps a Pebble iterator positioned at the current candidate.
// Every secondary index key ends with ts(8)+id(32), so one decoder fits all.
// Scans always run newest-first (created_at DESC): that is the only order
// the query layer serves.
type scanSource struct {
	it    *pebble.Iterator
	valid bool
	ts    uint64
	id    nostr.ID
}

func newScanSource(db *pebble.DB, prefix []byte, since, until int64) (*scanSource, error) {
	lower, upper := timeBounds(prefix, since, until)
	it, err := db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return nil, err
	}
	ss := &scanSource{it: it}
	ss.advance(true)
	return ss, nil
}

func (ss *scanSource) advance(first bool) {
	if first {
		ss.valid = ss.it.Last()
	} else {
		ss.valid = ss.it.Prev()
	}
	if ss.valid {
		k := ss.it.Key()
		n := len(k)
		ss.ts = binary.BigEndian.Uint64(k[n-tailLen : n-idLen])
		copy(ss.id[:], k[n-idLen:])
	}
}

func (ss *scanSource) close() { ss.it.Close() }

// srcHeap is a k-way merge over scan sources, popping the largest (ts, id)
// pair first (created_at DESC, id DESC tie-break).
type srcHeap struct {
	srcs []*scanSource
}

func (h srcHeap) Len() int { return len(h.srcs) }
func (h srcHeap) Less(i, j int) bool {
	a, b := h.srcs[i], h.srcs[j]
	if a.ts != b.ts {
		return a.ts > b.ts
	}
	return bytes.Compare(a.id[:], b.id[:]) > 0
}
func (h srcHeap) Swap(i, j int) { h.srcs[i], h.srcs[j] = h.srcs[j], h.srcs[i] }
func (h *srcHeap) Push(x any)   { h.srcs = append(h.srcs, x.(*scanSource)) }
func (h *srcHeap) Pop() any {
	old := h.srcs
	n := len(old)
	s := old[n-1]
	old[n-1] = nil
	h.srcs = old[:n-1]
	return s
}

// buildSources creates the driving-index iterators for the chosen plan and
// reports whether candidates must be post-checked against the full filter.
func (q *filterRun) buildSources() (srcs []*scanSource, postCheck bool, err error) {
	since, until := q.sinceUntil()
	f := q.f
	db := q.s.db

	closeOnErr := func() {
		for _, s := range srcs {
			s.close()
		}
	}

	switch q.plan() {
	case modeAuthors:
		postCheck = countTagPreds(f) > 0
		for _, a := range f.Authors {
			if len(f.Kinds) > 0 {
				// (pubkey, kind, created_at) index: kind filter is free
				for _, kind := range f.Kinds {
					ss, cerr := newScanSource(db, pubkeyKindPrefix(a[:], uint32(kind)), since, until)
					if cerr != nil {
						closeOnErr()
						return nil, false, cerr
					}
					srcs = append(srcs, ss)
				}
			} else {
				// (pubkey, created_at) timeline
				ss, cerr := newScanSource(db, pubkeyPrefix(a[:]), since, until)
				if cerr != nil {
					closeOnErr()
					return nil, false, cerr
				}
				srcs = append(srcs, ss)
			}
		}
		return srcs, postCheck, nil

	case modeTags:
		// drive from the tag name with the fewest values (most selective);
		// other tag predicates are post-checked.
		names := make([]string, 0, len(f.Tags))
		for name, vals := range f.Tags {
			if len(vals) > 0 {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		driving := names[0]
		for _, n := range names[1:] {
			if len(f.Tags[n]) < len(f.Tags[driving]) {
				driving = n
			}
		}
		postCheck = len(f.Kinds) > 0 || len(f.Authors) > 0 || len(names) > 1
		for _, v := range f.Tags[driving] {
			ss, cerr := newScanSource(db, tagPrefix(driving, v), since, until)
			if cerr != nil {
				closeOnErr()
				return nil, false, cerr
			}
			srcs = append(srcs, ss)
		}
		return srcs, postCheck, nil

	case modeKinds:
		for _, kind := range f.Kinds {
			ss, cerr := newScanSource(db, kindPrefix(uint32(kind)), since, until)
			if cerr != nil {
				closeOnErr()
				return nil, false, cerr
			}
			srcs = append(srcs, ss)
		}
		return srcs, false, nil

	default: // modeTime
		ss, cerr := newScanSource(db, timePrefix(), since, until)
		if cerr != nil {
			return nil, false, cerr
		}
		return []*scanSource{ss}, false, nil
	}
}

// ----------------------------------------------------------------------

// filterCursor streams the matches of ONE filter in (ts, id) DESC order,
// enforcing the per-filter limit during the scan. Duplicates (one event
// surfacing from several OR'd tag values) pop adjacently from the heap, so
// a last-emitted comparison dedups them with O(1) state — no seen map.
// Body loads are batched for concurrent resolution: collecting PREFETCH
// candidates from the heap and loading their bodies in parallel reduces
// p99 latency by avoiding serial db.Get round-trips.
type filterCursor struct {
	q         *filterRun
	ctx       context.Context
	postCheck bool
	isIDs     bool

	// scan-path state
	h    srcHeap
	srcs []*scanSource

	// ids-path state (materialized: point lookups bounded by len(ids))
	ids []candidate
	idx int

	// prefetch buffer: bodies loaded and post-checked, ready to yield one by one
	prefBuf []candidate
	prefIdx int

	accepted int
	cur      candidate
	valid    bool
	err      error

	lastTS  uint64
	lastID  nostr.ID
	hasLast bool
	closed  bool
}

const prefetchSize = 32

// advanceOne steps the heap once and sets cur to the next (ts,id) pair
// without loading the body. Used for count-only scans.
func (c *filterCursor) advanceOne() {
	q := c.q
	for c.h.Len() > 0 {
		if q.limit > 0 && c.accepted >= q.limit {
			break
		}
		if q.scanned >= q.budget {
			c.err = ErrScanBudgetExceeded
			break
		}
		if q.scanned&0x3FF == 0 {
			if err := c.ctx.Err(); err != nil {
				c.err = err
				break
			}
		}
		src := c.h.srcs[0]
		ts, id := src.ts, src.id
		src.advance(false)
		if src.valid {
			heap.Fix(&c.h, 0)
		} else {
			heap.Pop(&c.h)
		}
		q.scanned++
		if c.hasLast && c.lastTS == ts && c.lastID == id {
			continue
		}
		c.lastTS, c.lastID, c.hasLast = ts, id, true
		c.accepted++
		c.cur = candidate{ts: ts, id: id, ev: nil}
		c.valid = true
		return
	}
	c.valid = false
}

func (q *filterRun) openCursor(ctx context.Context) (*filterCursor, error) {
	c := &filterCursor{q: q, ctx: ctx}
	if q.plan() == modeIDs {
		idss, err := q.runIDs(ctx)
		if err != nil {
			return nil, err
		}
		c.isIDs = true
		c.ids = idss
		c.advance()
		return c, nil
	}
	srcs, postCheck, err := q.buildSources()
	if err != nil {
		return nil, err
	}
	c.postCheck = postCheck
	c.srcs = srcs
	for _, s := range srcs {
		if s.valid {
			c.h.srcs = append(c.h.srcs, s)
		}
	}
	heap.Init(&c.h)
	c.advance()
	return c, nil
}

// advance moves the cursor to the next accepted candidate.
func (c *filterCursor) advance() {
	q := c.q

	// ids path: replay the materialized, sorted point-lookup results
	if c.isIDs {
		if c.idx < len(c.ids) {
			c.cur = c.ids[c.idx]
			c.idx++
			c.valid = true
		} else {
			c.valid = false
		}
		return
	}

	// serve from prefetch buffer first
	for c.prefIdx < len(c.prefBuf) {
		if q.limit > 0 && c.accepted >= q.limit {
			break
		}
		cand := c.prefBuf[c.prefIdx]
		c.prefIdx++
		c.accepted++
		c.cur = cand
		c.valid = true
		return
	}
	c.prefBuf = c.prefBuf[:0]
	c.prefIdx = 0

	// Limit reached during prefBuf serving: stop.
	if q.limit > 0 && c.accepted >= q.limit {
		c.valid = false
		return
	}

	// Count-only path: one candidate from the heap, no body load needed.
	if !q.query && !c.postCheck {
		c.advanceOne()
		return
	}

	// Prefetch loop: may retry across batches if all candidates in one
	// batch get filtered out by postCheck.
	for c.h.Len() > 0 {
		// Collect up to prefetchSize raw candidates from the heap.
		var rawCands []struct {
			ts uint64
			id nostr.ID
		}
		for c.h.Len() > 0 && len(rawCands) < prefetchSize {
			if q.scanned >= q.budget {
				c.err = ErrScanBudgetExceeded
				c.valid = false
				return
			}
			if q.scanned&0x3FF == 0 {
				if err := c.ctx.Err(); err != nil {
					c.err = err
					c.valid = false
					return
				}
			}
			src := c.h.srcs[0]
			ts, id := src.ts, src.id

			src.advance(false)
			if src.valid {
				heap.Fix(&c.h, 0)
			} else {
				heap.Pop(&c.h)
			}
			q.scanned++

			if c.hasLast && c.lastTS == ts && c.lastID == id {
				continue
			}
			c.lastTS, c.lastID, c.hasLast = ts, id, true
			rawCands = append(rawCands, struct {
				ts uint64
				id nostr.ID
			}{ts, id})
		}

		if len(rawCands) == 0 {
			c.valid = false
			return
		}

		// Concurrent body resolution.
		type loaded struct {
			cand candidate
			idx  int
			err  error
		}
		results := make(chan loaded, len(rawCands))
		var wg sync.WaitGroup
		const workers = 8
		jobs := make(chan int, len(rawCands))
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range jobs {
					rc := rawCands[j]
					ev, err := q.s.loadBody(rc.ts, rc.id)
					if err != nil {
						results <- loaded{idx: j, err: err}
						return
					}
					if ev == nil {
						continue
					}
					if c.postCheck && !q.f.Matches(*ev) {
						continue
					}
					results <- loaded{cand: candidate{ts: rc.ts, id: rc.id, ev: ev}, idx: j}
				}
			}()
		}
		for i := range rawCands {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		close(results)

		loadedMap := make(map[int]candidate, len(rawCands))
		var firstErr error
		for r := range results {
			if r.err != nil {
				if firstErr == nil {
					firstErr = r.err
				}
				continue
			}
			loadedMap[r.idx] = r.cand
		}
		if firstErr != nil {
			c.err = firstErr
			c.valid = false
			return
		}
		for i := range rawCands {
			if cand, ok := loadedMap[i]; ok {
				c.prefBuf = append(c.prefBuf, cand)
			}
		}
		if len(c.prefBuf) > 0 {
			break // got at least one accepted candidate
		}
		// All filtered out by postCheck; loop back for next batch.
	}

	if len(c.prefBuf) == 0 {
		c.valid = false
		return
	}
	// Yield the first from the freshly filled prefetch buffer.
	cand := c.prefBuf[0]
	c.prefIdx = 1
	c.accepted++
	c.cur = cand
	c.valid = true
}

// close releases all iterators and settles the scan statistics in one
// atomic add (instead of one per stepped key).
func (c *filterCursor) close() {
	if c.closed {
		return
	}
	c.closed = true
	c.q.s.stats.scanned.Add(c.q.scanned)
	for _, s := range c.srcs {
		if c.err == nil {
			c.err = s.it.Error()
		}
		s.close()
	}
}

// finish closes the cursor and returns the first error encountered
// (scan error, budget/ctx abort, or iterator error surfaced at close).
func (c *filterCursor) finish() error {
	c.close()
	return c.err
}

// runIDs resolves an ids filter via concurrent point lookups + in-memory
// AND checks. Bounded by len(ids); results are sorted (ts, id) DESC to
// match the scan order convention.
func (q *filterRun) runIDs(ctx context.Context) ([]candidate, error) {
	ids := q.f.IDs
	if len(ids) == 0 {
		return nil, nil
	}
	// Dedup while preserving order
	seen := make(map[nostr.ID]struct{}, len(ids))
	uniq := make([]nostr.ID, 0, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		uniq = append(uniq, id)
	}

	// Resolve concurrently. Pebble has no MultiGet; db.Get is goroutine-safe
	// and bloom-filtered, so a small worker pool is the next best thing.
	type result struct {
		cand candidate
		ok   bool
	}
	const workers = 8
	jobs := make(chan nostr.ID, len(uniq))
	results := make(chan result, len(uniq))
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				tsb, cl, err := q.s.db.Get(locatorKey(id[:]))
				if err != nil {
					if cl != nil {
						cl.Close()
					}
					results <- result{ok: false}
					continue
				}
				ts := binary.BigEndian.Uint64(tsb)
				cl.Close()
				ev, err := q.s.loadBody(ts, id)
				if err != nil || ev == nil {
					results <- result{ok: false}
					continue
				}
				results <- result{cand: candidate{ts: ts, id: id, ev: ev}, ok: true}
			}
		}()
	}
	for _, id := range uniq {
		select {
		case jobs <- id:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return nil, ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	close(results)

	out := make([]candidate, 0, len(uniq))
	for r := range results {
		if !r.ok {
			continue
		}
		if !q.f.Matches(*r.cand.ev) {
			continue
		}
		out = append(out, r.cand)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ts != out[j].ts {
			return out[i].ts > out[j].ts
		}
		return bytes.Compare(out[i].id[:], out[j].id[:]) > 0
	})
	if q.limit > 0 && len(out) > q.limit {
		out = out[:q.limit]
	}
	return out, nil
}

// ----------------------------------------------------------------------

// loadBody fetches and decodes one event body by its (ts, id) coordinates —
// the same coordinates every index key already carries, so scans never pay
// the locator hop. The value is parsed in place while the iterator handle
// is open: no defensive copy (the decoder copies out any strings it keeps).
func (s *PebbleStore) loadBody(ts uint64, id nostr.ID) (*nostr.Event, error) {
	t0 := time.Now()
	defer func() { s.loadBodyHist.record(time.Since(t0).Microseconds()) }()
	raw, cl, err := s.db.Get(bodyKey(ts, id[:]))
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	defer cl.Close()
	s.stats.loaded.Add(1)

	evt := &nostr.Event{}
	if err := betterbinary.Unmarshal(raw, evt); err != nil {
		return nil, err
	}

	return evt, nil
}
