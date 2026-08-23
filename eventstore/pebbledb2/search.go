package pebbledb

import (
	"bytes"
	"context"
	"encoding/binary"
	"sort"
	"sync"
	"sync/atomic"

	"fiatjaf.com/nostr"
	"github.com/cockroachdb/pebble/v2"
)

// searchTask is one time-bounded piece of a signature-scan search.
type searchTask struct {
	lo, hi int64
}

// splitRange divides [dMin, dMax] into n contiguous pieces with no overlap
// and full coverage. The per-piece width is added repeatedly (never
// multiplied by the piece index), so a full-width range (dMin=0,
// dMax=MaxInt64, i.e. 2^63+1 points) does not overflow uint64.
func splitRange(dMin, dMax int64, n int) [][2]int64 {
	if dMax < dMin {
		return nil
	}
	width := uint64(dMax) - uint64(dMin) + 1
	// width == 0 only when the range covers all 2^64 values
	// ([MinInt64, MaxInt64]): keep the requested piece count.
	if width != 0 && width < uint64(n) {
		n = int(width)
	}
	w := width / uint64(n)
	if w == 0 {
		w = 1
	}
	out := make([][2]int64, 0, n)
	for i := 0; i < n; i++ {
		lo := uint64(dMin) + uint64(i)*w
		hi := lo + w - 1
		if i == n-1 {
			hi = uint64(dMax) // last piece absorbs the remainder
		}
		out = append(out, [2]int64{int64(lo), int64(hi)})
	}
	return out
}

// dataTimeRange returns the [min, max] created_at of stored bodies, or
// (0, -1) when the store is empty. Two seeks on the body index locate the
// extremes cheaply.
func (s *PebbleStore) dataTimeRange() (int64, int64) {
	it, err := s.db.NewIter(&pebble.IterOptions{LowerBound: timePrefix()})
	if err != nil {
		return 0, -1
	}
	defer it.Close()
	if !it.First() {
		return 0, -1
	}
	k := it.Key()
	min := int64(binary.BigEndian.Uint64(k[len(k)-tailLen : len(k)-idLen]))
	if !it.Last() {
		return min, min
	}
	k = it.Key()
	max := int64(binary.BigEndian.Uint64(k[len(k)-tailLen : len(k)-idLen]))
	return min, max
}

// searchTasks splits the filter's time range into up to 8 pieces for parallel
// scanning. Pieces are carved from the intersection of the filter range and
// the actual stored data range, so a mostly-uniform distribution spreads the
// work across shards instead of lumping it into one. Every piece scans the
// body index merged with the signature index, so coverage does not depend on
// how many signature keys exist.
func (q *filterRun) searchTasks() []searchTask {
	since, until := q.sinceUntil()
	if since > until {
		return nil
	}
	dMin, dMax := q.s.dataTimeRange()
	if dMax < 0 || dMax < since || dMin > until {
		return nil
	}
	lo := max(since, dMin)
	hi := min(until, dMax)
	if lo > hi {
		return nil
	}
	segs := splitRange(lo, hi, 8)
	tasks := make([]searchTask, 0, len(segs))
	for _, seg := range segs {
		tasks = append(tasks, searchTask{seg[0], seg[1]})
	}
	return tasks
}

// searchRun executes a Search filter over the time index using the content
// signature index to skip body reads. Query mode returns the top-limit
// matching events (newest first); count mode returns the number of matches.
// Results from all shards are merged by (ts, id) descending.
func (q *filterRun) searchRun(ctx context.Context) ([]nostr.Event, int64, error) {
	tasks := q.searchTasks()
	if len(tasks) == 0 {
		return nil, 0, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var scanned atomic.Int64
	type taskRes struct {
		cands []candidate
		count int64
		err   error
	}
	resCh := make(chan taskRes, len(tasks))
	taskCh := make(chan searchTask)
	var wg sync.WaitGroup
	workers := min(8, len(tasks))
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range taskCh {
				cands, count, err := q.searchTask(ctx, t, &scanned)
				select {
				case resCh <- taskRes{cands: cands, count: count, err: err}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
sendTasks:
	for _, t := range tasks {
		select {
		case taskCh <- t:
		case <-ctx.Done():
			break sendTasks
		}
	}
	close(taskCh)
	wg.Wait()
	close(resCh)

	q.s.stats.scanned.Add(scanned.Load())

	var all []candidate
	var total int64
	var firstErr error
	for r := range resCh {
		if r.err != nil {
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		total += r.count
		all = append(all, r.cands...)
	}
	if firstErr != nil {
		return nil, 0, firstErr
	}
	if !q.query {
		return nil, total, nil
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].ts != all[j].ts {
			return all[i].ts > all[j].ts
		}
		return bytes.Compare(all[i].id[:], all[j].id[:]) > 0
	})
	if q.limit > 0 && len(all) > q.limit {
		all = all[:q.limit]
	}
	evs := make([]nostr.Event, 0, len(all))
	for _, c := range all {
		evs = append(evs, *c.ev)
	}
	return evs, 0, nil
}

// searchTask scans one time-bounded piece by merging the body index (every
// event) with the content-signature index (events whose content is at least 3
// bytes). A body without a signature — a legacy row or short content — is read
// directly; a body with a signature is skipped when the bloom cannot contain
// the pattern. Every body is visited exactly once, so the result is exact
// regardless of signature coverage.
func (q *filterRun) searchTask(ctx context.Context, t searchTask, scanned *atomic.Int64) ([]candidate, int64, error) {
	lower, upper := timeBounds(timePrefix(), t.lo, t.hi)
	itB, err := q.s.db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return nil, 0, err
	}
	defer itB.Close()

	fLower, fUpper := timeBounds(bloomPrefix(), t.lo, t.hi)
	itF, err := q.s.db.NewIter(&pebble.IterOptions{LowerBound: fLower, UpperBound: fUpper})
	if err != nil {
		return nil, 0, err
	}
	defer itF.Close()

	parseTail := func(k []byte) (uint64, nostr.ID) {
		var id nostr.ID
		copy(id[:], k[len(k)-idLen:])
		return binary.BigEndian.Uint64(k[len(k)-tailLen : len(k)-idLen]), id
	}

	var cands []candidate
	var count int64
	step := func() error {
		n := scanned.Add(1)
		if n > q.budget {
			return ErrScanBudgetExceeded
		}
		if n&0x3FF == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		return nil
	}

	// Both streams are ordered by (ts, id); the signature stream is a subset
	// of the body stream (every signature key was written together with its
	// body and is deleted with it), so a linear merge visits each body once.
	bOK := itB.First()
	fOK := itF.First()
	var bTS uint64
	var bID nostr.ID
	var fTS uint64
	var fID nostr.ID
	if bOK {
		bTS, bID = parseTail(itB.Key())
	}
	if fOK {
		fTS, fID = parseTail(itF.Key())
	}

	for bOK {
		if err := step(); err != nil {
			return nil, 0, err
		}
		// advance the signature stream to the current body
		for fOK && (fTS < bTS || (fTS == bTS && bytes.Compare(fID[:], bID[:]) < 0)) {
			fOK = itF.Next()
			if fOK {
				fTS, fID = parseTail(itF.Key())
			}
		}
		hasSig := fOK && fTS == bTS && bytes.Equal(fID[:], bID[:])
		if hasSig && !bloomMaybeContains(itF.Value(), q.searchGrams) {
			// signature says the pattern cannot be present; skip the body
			bOK = itB.Next()
			if bOK {
				bTS, bID = parseTail(itB.Key())
			}
			continue
		}

		ev, err := q.s.loadBody(bTS, bID)
		if err != nil {
			return nil, 0, err
		}
		if ev == nil {
		} else if q.search.Contains(ev.Content) && q.f.Matches(*ev) {
			if q.query {
				cands = append(cands, candidate{ts: bTS, id: bID, ev: ev})
			} else {
				count++
			}
		}
		bOK = itB.Next()
		if bOK {
			bTS, bID = parseTail(itB.Key())
		}
	}
	if err := itB.Error(); err != nil {
		return nil, 0, err
	}
	if err := itF.Error(); err != nil {
		return nil, 0, err
	}
	if q.query && q.limit > 0 && len(cands) > q.limit {
		sort.Slice(cands, func(i, j int) bool {
			if cands[i].ts != cands[j].ts {
				return cands[i].ts > cands[j].ts
			}
			return bytes.Compare(cands[i].id[:], cands[j].id[:]) > 0
		})
		cands = cands[:q.limit]
	}
	return cands, count, nil
}
