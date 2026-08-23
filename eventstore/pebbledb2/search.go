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

// searchTask is one time-bounded piece of a signature-scan search. useBloom
// scans the 'F' signature index (skipping bodies whose bloom cannot contain
// the pattern); otherwise the plain timeline is scanned and every body is
// read (events predating the signature index, or too short to have one).
type searchTask struct {
	lo, hi   int64
	useBloom bool
}

// searchTasks splits the filter's time range based on signature coverage:
// everything before the oldest signature key is scanned via the timeline,
// everything at or after it is covered by 'F' entries. Each segment is split
// into up to 8 pieces for parallel scanning.
func (q *filterRun) searchTasks() []searchTask {
	bloomMin := q.s.bloomMinTS.Load()
	since, until := q.sinceUntil()
	var segs []searchTask
	if since > until {
		return nil
	}
	if until < bloomMin {
		segs = append(segs, searchTask{since, until, false})
	} else {
		if since < bloomMin {
			segs = append(segs, searchTask{since, bloomMin - 1, false})
		}
		lo := bloomMin
		if since > lo {
			lo = since
		}
		segs = append(segs, searchTask{lo, until, true})
	}

	const pieces = 8
	var tasks []searchTask
	for _, seg := range segs {
		width := uint64(seg.hi) - uint64(seg.lo) + 1
		n := uint64(pieces)
		if width < n {
			n = width
		}
		for i := uint64(0); i < n; i++ {
			lo := int64(uint64(seg.lo) + width*i/n)
			hi := int64(uint64(seg.lo) + width*(i+1)/n - 1)
			tasks = append(tasks, searchTask{lo, hi, seg.useBloom})
		}
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

// searchTask scans one time-bounded piece, testing content signatures (when
// available) and resolving survivors with a body read + exact substring
// match. Query mode retains at most q.limit candidates per task.
func (q *filterRun) searchTask(ctx context.Context, t searchTask, scanned *atomic.Int64) ([]candidate, int64, error) {
	prefix := timePrefix()
	if t.useBloom {
		prefix = bloomPrefix()
	}
	lower, upper := timeBounds(prefix, t.lo, t.hi)
	it, err := q.s.db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return nil, 0, err
	}
	defer it.Close()

	var cands []candidate
	var count int64
	for valid := it.First(); valid; valid = it.Next() {
		n := scanned.Add(1)
		if n > q.budget {
			return nil, 0, ErrScanBudgetExceeded
		}
		if n&0x3FF == 0 {
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
		}
		k := it.Key()
		ts := binary.BigEndian.Uint64(k[len(k)-tailLen : len(k)-idLen])
		var id nostr.ID
		copy(id[:], k[len(k)-idLen:])

		if t.useBloom {
			v := it.Value()
			if len(v) == bloomBytes && !bloomMaybeContains(v, q.searchGrams) {
				continue
			}
		}

		ev, err := q.s.loadBody(ts, id)
		if err != nil {
			return nil, 0, err
		}
		if ev == nil {
			continue
		}
		if !q.search.Contains(ev.Content) {
			continue
		}
		if !q.f.Matches(*ev) {
			continue
		}
		if q.query {
			cands = append(cands, candidate{ts: ts, id: id, ev: ev})
		} else {
			count++
		}
	}
	if err := it.Error(); err != nil {
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
