package pebbledb

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"sync/atomic"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/codec/betterbinary"
	"fiatjaf.com/nostr/nip45"
	"fiatjaf.com/nostr/nip45/hyperloglog"
	"github.com/cockroachdb/pebble/v2"
)

// ErrHLLNotEligible is returned by CountEventsHLL when the filter is not one
// of the NIP-45 shapes this backend maintains sketches for.
var ErrHLLNotEligible = errors.New("relaystore: filter is not eligible for NIP-45 HLL counting")

// addHLLMerges appends one sparse merge operand per NIP-45 target of the
// event: (register index, value) derived from the author pubkey and the
// target's deterministic offset. Operands are 3 bytes, so the write path
// never reads the current sketch (no read-modify-write); Pebble accumulates
// and compacts them. Called only from the writer goroutine's commit path.
func addHLLMerges(batch *pebble.Batch, ev *nostr.Event) {
	for t := range nip45.HyperLogLogTargetsForEventWithTags(*ev) {
		if len(t.TagKey) != 1 {
			continue
		}
		ref, ok := hexDecode32(t.Ref)
		if !ok {
			continue
		}
		idx, val := hyperloglog.RegisterForPubkey(ev.PubKey, t.Offset)
		operand := [3]byte{hllSparse, idx, val}
		_ = batch.Merge(hllKey(t.TagKey[0], uint32(ev.Kind), ref), operand[:], nil)
	}
}

func hexDecode32(s string) ([]byte, bool) {
	b, err := nostr.HexDecodeString(s)
	if err != nil || len(b) != idLen {
		return nil, false
	}
	return b, true
}

// CountEventsHLL returns the estimated number of distinct pubkeys that
// referenced the filter's target event, following NIP-45. It reads the
// incrementally-maintained HyperLogLog sketches for each kind in the filter;
// when any sketch is missing (e.g. events written before this feature
// existed, or a filter whose ref was never referenced) it falls back to an
// exact scan and returns the exact distinct count with a nil sketch.
func (s *PebbleStore) CountEventsHLL(ctx context.Context, filter nostr.Filter, offset int) (int64, *hyperloglog.HyperLogLog, error) {
	tagKey, tagValue, ok := nip45.HyperLogLogFilterTag(filter)
	if !ok {
		return 0, nil, ErrHLLNotEligible
	}
	if err := s.acquire(ctx); err != nil {
		return 0, nil, err
	}
	defer s.release()
	s.stats.counts.Add(1)

	var regs [256]uint8
	missing := false
	ref, isHex := hexDecode32(tagValue)
	if isHex {
		for _, kind := range filter.Kinds {
			val, cl, err := s.db.Get(hllKey(tagKey[0], uint32(kind), ref))
			if errors.Is(err, pebble.ErrNotFound) {
				missing = true
				continue
			}
			if err != nil {
				if cl != nil {
					cl.Close()
				}
				return 0, nil, err
			}
			if len(val) == 1+256 {
				for i, v := range val[1:] {
					if v > regs[i] {
						regs[i] = v
					}
				}
			} else {
				missing = true
			}
			cl.Close()
		}
	} else {
		// non-hex target (e.g. address format): no sketch can exist, the
		// exact scan below is the only answer.
		missing = true
	}

	if missing {
		n, err := s.hllFallbackCount(ctx, tagKey, tagValue, filter.Kinds)
		return n, nil, err
	}

	cnt := hyperloglog.CountRegisters(regs[:])
	hll := hyperloglog.NewWithRegisters(regs[:], offset)
	return int64(cnt), hll, nil
}

// hllFallbackCount exactly counts the distinct pubkeys that referenced
// tagValue with one of the given kinds, scanning the tag index over the full
// time domain in parallel shards. Each shard keeps its own author set to
// avoid contention; sets are unioned at the end. Only the pubkey and kind are
// decoded from each body, never the full event.
func (s *PebbleStore) hllFallbackCount(ctx context.Context, tagKey, tagValue string, kinds []nostr.Kind) (int64, error) {
	prefix := tagPrefix(tagKey, tagValue)
	kindOK := make(map[nostr.Kind]struct{}, len(kinds))
	for _, k := range kinds {
		kindOK[k] = struct{}{}
	}

	const shardCount = 8
	type shard struct {
		lo, hi int64
	}
	shards := make([]shard, 0, shardCount)
	{
		// [0, MaxInt64] holds 2^63 timestamps; split into equal ranges.
		const total = uint64(1) << 63
		for i := uint64(0); i < shardCount; i++ {
			lo := total * i / shardCount
			hi := total*(i+1)/shardCount - 1
			shards = append(shards, shard{int64(lo), int64(hi)})
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var scanned atomic.Int64
	var firstErr atomic.Value // stores error, if any

	var mu sync.Mutex
	all := make(map[nostr.PubKey]struct{})

	var wg sync.WaitGroup
	for _, sh := range shards {
		wg.Add(1)
		go func(sh shard) {
			defer wg.Done()
			local := make(map[nostr.PubKey]struct{})
			lower, upper := timeBounds(prefix, sh.lo, sh.hi)
			it, err := s.db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
			if err != nil {
				firstErr.Store(err)
				cancel()
				return
			}
			defer it.Close()
			for valid := it.First(); valid; valid = it.Next() {
				if firstErr.Load() != nil {
					return
				}
				n := scanned.Add(1)
				if n > int64(s.opts.MaxScanKeys) {
					firstErr.Store(ErrScanBudgetExceeded)
					cancel()
					return
				}
				if n&0x3FF == 0 {
					if err := ctx.Err(); err != nil {
						return
					}
				}
				k := it.Key()
				ts := binary.BigEndian.Uint64(k[len(k)-tailLen : len(k)-idLen])
				var id nostr.ID
				copy(id[:], k[len(k)-idLen:])

				raw, cl, err := s.db.Get(bodyKey(ts, id[:]))
				if err != nil {
					continue // deleted between scan and read; skip
				}
				kind := betterbinary.GetKind(raw)
				cl.Close()
				if _, ok := kindOK[kind]; !ok {
					continue
				}
				local[betterbinary.GetPubKey(raw)] = struct{}{}
			}
			if err := it.Error(); err != nil && firstErr.Load() == nil {
				firstErr.Store(err)
				cancel()
				return
			}
			mu.Lock()
			for pk := range local {
				all[pk] = struct{}{}
			}
			mu.Unlock()
		}(sh)
	}
	wg.Wait()

	s.stats.scanned.Add(scanned.Load())
	if e := firstErr.Load(); e != nil {
		return 0, e.(error)
	}
	return int64(len(all)), nil
}
