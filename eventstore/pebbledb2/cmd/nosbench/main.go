// nosbench seeds a relaystore database with synthetic events and then
// hammers it with a realistic relay query mix, reporting throughput and
// latency percentiles.
//
// Usage:
//
//	go run ./cmd/nosbench -dir /data/db -seed 10000000 -writers 64 \
//	    -qworkers 8 -warmup 60 -qseconds 60
//
// To simulate a 2C2G box on a bigger machine:
//
//	GOMEMLIMIT=1500MiB GOGC=50 go run ./cmd/nosbench ...
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"iter"
	"math"
	mrand "math/rand/v2"
	"os"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"fiatjaf.com/nostr"
	relaystore "fiatjaf.com/nostr/eventstore/pebbledb2"
)

var (
	dir         = flag.String("dir", "/tmp/nosbench-db", "data directory")
	seed        = flag.Int("seed", 1_000_000, "events to seed (0 = skip seeding)")
	writers     = flag.Int("writers", 64, "concurrent seeding writers")
	qworkers    = flag.Int("qworkers", 8, "concurrent query workers")
	warmup      = flag.Int("warmup", 30, "unmeasured warmup seconds (warms block cache)")
	qseconds    = flag.Int("qseconds", 60, "measured query phase seconds")
	nauthors    = flag.Int("authors", 100_000, "author pool size")
	nhashtags   = flag.Int("hashtags", 500, "hashtag pool size")
	walsync     = flag.Bool("walsync", false, "fsync every write group")
	cacheMB     = flag.Int("cache", 128, "block cache MiB")
	compactions = flag.Int("compactions", 1, "max concurrent compactions")
	settle      = flag.Int("settle", 0, "seconds to let compactions drain before querying")
	fullcompact = flag.Bool("fullcompact", false, "force a full LSM compaction before querying")
)

// ---- deterministic synthetic data ----

func rand32(r *mrand.Rand) (b [32]byte) {
	for i := range b {
		b[i] = byte(r.Uint64())
	}
	return
}

func randHex(r *mrand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint64())
	}
	return hex.EncodeToString(b)
}

type dataset struct {
	authors  []nostr.PubKey
	hashtags []string
	// sample of seeded event ids & their authors, for #e/#p queries
	eIDs     []nostr.ID
	eAuthors []nostr.PubKey
	mu       sync.Mutex
}

func newDataset(nauthors, nhashtags int) *dataset {
	d := &dataset{}
	r := mrand.New(mrand.NewPCG(1, 2))
	for i := 0; i < nauthors; i++ {
		d.authors = append(d.authors, nostr.PubKey(rand32(r)))
	}
	for i := 0; i < nhashtags; i++ {
		d.hashtags = append(d.hashtags, fmt.Sprintf("topic%05d", i))
	}
	return d
}

// zipf-ish author pick: few authors write a lot
func (d *dataset) pickAuthor(r *mrand.Rand) nostr.PubKey {
	n := len(d.authors)
	u := r.Float64()
	rank := int(math.Pow(float64(n), u)) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= n {
		rank = n - 1
	}
	return d.authors[rank]
}

var kindWeights = []struct {
	kind nostr.Kind
	w    int
}{
	{1, 55},   // short text notes
	{7, 20},   // reactions
	{6, 8},    // reposts
	{3, 3},    // contact lists
	{0, 2},    // metadata
	{9735, 4}, // zaps
	{30023, 3},
	{10002, 2},
	{4, 3}, // legacy DMs
}

func pickKind(r *mrand.Rand) nostr.Kind {
	total := 0
	for _, kw := range kindWeights {
		total += kw.w
	}
	x := r.IntN(total)
	for _, kw := range kindWeights {
		if x < kw.w {
			return kw.kind
		}
		x -= kw.w
	}
	return 1
}

func (d *dataset) makeEvent(r *mrand.Rand, now int64) nostr.Event {
	kind := pickKind(r)
	author := d.pickAuthor(r)
	// created_at: spread over ~180 days, biased toward recent
	age := int64(r.ExpFloat64() * float64(7*24*3600))
	maxAge := int64(180 * 24 * 3600)
	if age > maxAge {
		age = r.Int64N(maxAge)
	}
	ts := now - age

	var tags nostr.Tags
	switch kind {
	case 1:
		// 30% reply with #e + #p
		if r.IntN(10) < 3 && len(d.eIDs) > 0 {
			idx := r.IntN(len(d.eIDs))
			tags = append(tags, nostr.Tag{"e", d.eIDs[idx].Hex()})
			tags = append(tags, nostr.Tag{"p", d.eAuthors[idx].Hex()})
		}
		// hashtags
		for i := 0; i < r.IntN(3); i++ {
			tags = append(tags, nostr.Tag{"t", d.hashtags[r.IntN(len(d.hashtags))]})
		}
	case 7, 6, 9735:
		if len(d.eIDs) > 0 {
			idx := r.IntN(len(d.eIDs))
			tags = append(tags, nostr.Tag{"e", d.eIDs[idx].Hex()})
			tags = append(tags, nostr.Tag{"p", d.eAuthors[idx].Hex()})
		}
	case 30023:
		tags = append(tags, nostr.Tag{"d", randHex(r, 8)})
		tags = append(tags, nostr.Tag{"t", d.hashtags[r.IntN(len(d.hashtags))]})
	}

	var ev nostr.Event
	_, _ = rand.Read(ev.ID[:])
	_, _ = rand.Read(ev.Sig[:])
	ev.PubKey = author
	ev.CreatedAt = nostr.Timestamp(ts)
	ev.Kind = kind
	ev.Tags = tags
	ev.Content = fmt.Sprintf("synthetic content for event %s lorem ipsum dolor sit amet",
		hex.EncodeToString(ev.ID[:8]))
	return ev
}

func (d *dataset) record(ev *nostr.Event) {
	d.mu.Lock()
	if len(d.eIDs) < 200_000 {
		d.eIDs = append(d.eIDs, ev.ID)
		d.eAuthors = append(d.eAuthors, ev.PubKey)
	}
	d.mu.Unlock()
}

// ---- latency recorder ----

type latencies struct {
	mu sync.Mutex
	m  map[string][]float64
}

func newLatencies() *latencies { return &latencies{m: make(map[string][]float64)} }

func (l *latencies) add(name string, d time.Duration) {
	l.mu.Lock()
	arr := l.m[name]
	if len(arr) < 300_000 {
		l.m[name] = append(arr, float64(d.Microseconds()))
	}
	l.mu.Unlock()
}

func (l *latencies) reset() {
	l.mu.Lock()
	l.m = make(map[string][]float64)
	l.mu.Unlock()
}

func pct(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[int(p*float64(len(sorted)-1))]
}

func (l *latencies) report() {
	l.mu.Lock()
	defer l.mu.Unlock()
	names := make([]string, 0, len(l.m))
	for n := range l.m {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Printf("\n%-38s %8s %9s %9s %9s %9s\n", "query", "n", "p50", "p90", "p99", "max")
	for _, n := range names {
		arr := l.m[n]
		sort.Float64s(arr)
		fmt.Printf("%-38s %8d %8.0fus %8.0fus %8.0fus %8.0fus\n",
			n, len(arr), pct(arr, .5), pct(arr, .9), pct(arr, .99), arr[len(arr)-1])
	}
}

// ---- query mix (Store-interface only: QueryEvents / CountEvents) ----

func drain(seq iter.Seq2[nostr.Event, error]) error {
	for _, err := range seq {
		if err != nil {
			return err
		}
	}
	return nil
}

func runQueryWorker(ctx context.Context, store *relaystore.PebbleStore, d *dataset, now int64,
	stop *atomic.Bool, lat *latencies, qcount *atomic.Int64, wid int,
) {
	r := mrand.New(mrand.NewPCG(uint64(wid), 0xbeef))
	for !stop.Load() {
		x := r.IntN(100)
		var name string
		var err error
		start := time.Now()
		switch {
		case x < 33: // global feed
			name = "feed: kinds=[1] limit=20"
			err = drain(store.QueryEvents(ctx, nostr.Filter{Kinds: []nostr.Kind{1}, Limit: 20}, 1000))
		case x < 58: // profile feed
			name = "profile: authors=[a] kinds=[1] limit=30"
			err = drain(store.QueryEvents(ctx, nostr.Filter{
				Authors: []nostr.PubKey{d.pickAuthor(r)}, Kinds: []nostr.Kind{1}, Limit: 30,
			}, 1000))
		case x < 68: // thread
			if len(d.eIDs) == 0 {
				continue
			}
			name = "thread: #e=[id] limit=100"
			err = drain(store.QueryEvents(ctx, nostr.Filter{
				Tags: nostr.TagMap{"e": {d.eIDs[r.IntN(len(d.eIDs))].Hex()}}, Limit: 100,
			}, 1000))
		case x < 78: // mentions
			name = "mentions: #p=[pk] limit=50"
			err = drain(store.QueryEvents(ctx, nostr.Filter{
				Tags: nostr.TagMap{"p": {d.pickAuthor(r).Hex()}}, Limit: 50,
			}, 1000))
		case x < 88: // hashtag + time window
			name = "hashtag: #t limit=50 since=1h"
			err = drain(store.QueryEvents(ctx, nostr.Filter{
				Tags:  nostr.TagMap{"t": {d.hashtags[r.IntN(len(d.hashtags))]}},
				Since: nostr.Timestamp(now - 3600),
				Limit: 50,
			}, 1000))
		case x < 93: // COUNT: kind + time window (rollup counters)
			name = "count: kinds=[7] since=24h"
			_, err = store.CountEvents(ctx, nostr.Filter{
				Kinds: []nostr.Kind{7}, Since: nostr.Timestamp(now - 24*3600),
			})
		default: // COUNT with author (counter point reads)
			name = "count: authors=[a] kinds=[1]"
			_, err = store.CountEvents(ctx, nostr.Filter{
				Authors: []nostr.PubKey{d.pickAuthor(r)}, Kinds: []nostr.Kind{1},
			})
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, name, "err:", err)
			continue
		}
		lat.add(name, time.Since(start))
		qcount.Add(1)
	}
}

// ---- main ----

func main() {
	flag.Parse()
	now := time.Now().Unix()
	ctx := context.Background()

	opts := relaystore.DefaultOptions(*dir)
	opts.WALSync = *walsync
	opts.CacheSize = int64(*cacheMB) << 20
	opts.MaxConcurrentCompactions = *compactions
	store, err := relaystore.Open(opts)
	if err != nil {
		panic(err)
	}
	defer store.Close()

	d := newDataset(*nauthors, *nhashtags)

	// ---- seed phase ----
	if *seed > 0 {
		fmt.Printf("seeding %d events with %d writers...\n", *seed, *writers)
		var done atomic.Int64
		start := time.Now()
		var wg sync.WaitGroup
		for w := 0; w < *writers; w++ {
			wg.Add(1)
			go func(wid int) {
				defer wg.Done()
				r := mrand.New(mrand.NewPCG(uint64(wid), 0xdead))
				for {
					i := done.Add(1)
					if i > int64(*seed) {
						return
					}
					ev := d.makeEvent(r, now)
					if err := store.SaveEvent(ctx, ev); err != nil {
						fmt.Fprintln(os.Stderr, "save:", err)
						return
					}
					d.record(&ev)
					if i%500_000 == 0 {
						el := time.Since(start).Seconds()
						fmt.Printf("  %d events, %.0f ev/s, heap=%dMiB\n",
							i, float64(i)/el, heapMiB())
					}
				}
			}(w)
		}
		wg.Wait()
		el := time.Since(start)
		fmt.Printf("seeded %d events in %s (%.0f ev/s avg)\n", *seed, el.Round(time.Millisecond), float64(*seed)/el.Seconds())
	} else {
		fmt.Println("seed skipped; sampling timeline for referenceable ids...")
		for ev, err := range store.QueryEvents(ctx, nostr.Filter{Limit: 1000}, 1000) {
			if err != nil {
				fmt.Fprintln(os.Stderr, "sample:", err)
				break
			}
			d.eIDs = append(d.eIDs, ev.ID)
			d.eAuthors = append(d.eAuthors, ev.PubKey)
		}
	}

	// ---- settle phase ----
	if *settle > 0 {
		fmt.Printf("settling %ds for compactions to drain...\n", *settle)
		time.Sleep(time.Duration(*settle) * time.Second)
	}
	if *fullcompact {
		fmt.Printf("running full compaction...\n")
		t0 := time.Now()
		if err := store.Compact(); err != nil {
			fmt.Fprintln(os.Stderr, "compact:", err)
		}
		fmt.Printf("full compaction done in %s\n", time.Since(t0).Round(time.Second))
	}
	m := store.Metrics()
	fmt.Printf("LSM: L0=%d tables, L6=%d tables, block cache hit rate %.1f%%\n",
		m.Levels[0].TablesCount, m.Levels[6].TablesCount, hitRate(m.BlockCache.Hits, m.BlockCache.Misses))

	// ---- query phase: warmup (unmeasured) then measured window ----
	lat := newLatencies()
	var qcount atomic.Int64
	var stop atomic.Bool
	var wg sync.WaitGroup
	for w := 0; w < *qworkers; w++ {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			runQueryWorker(ctx, store, d, now, &stop, lat, &qcount, wid)
		}(w)
	}

	fmt.Printf("warming up for %ds (%d workers)...\n", *warmup, *qworkers)
	time.Sleep(time.Duration(*warmup) * time.Second)

	lat.reset()
	qcount.Store(0)
	t0 := time.Now()
	fmt.Printf("measuring for %ds...\n", *qseconds)
	time.Sleep(time.Duration(*qseconds) * time.Second)
	stop.Store(true)
	wg.Wait()
	elapsed := time.Since(t0)

	fmt.Printf("\ntotal queries: %d  (%.0f qps, %d workers)\n",
		qcount.Load(), float64(qcount.Load())/elapsed.Seconds(), *qworkers)
	lat.report()
	fmt.Printf("\nheap=%dMiB  goroutines=%d\n", heapMiB(), runtime.NumGoroutine())
	m = store.Metrics()
	fmt.Printf("block cache hit rate: %.1f%%\n", hitRate(m.BlockCache.Hits, m.BlockCache.Misses))
	fmt.Printf("store stats: %+v\n", store.Stats())
}

func heapMiB() uint64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapInuse >> 20
}

func hitRate(hits, misses int64) float64 {
	if hits+misses == 0 {
		return 0
	}
	return 100 * float64(hits) / float64(hits+misses)
}
