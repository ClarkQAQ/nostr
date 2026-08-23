package test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	pebbledb2 "fiatjaf.com/nostr/eventstore/pebbledb2"
	"github.com/stretchr/testify/require"
)

func TestPebbleDB2CloseRace(t *testing.T) {
	db, err := pebbledb2.OpenDir(t.TempDir())
	require.NoError(t, err)
	ctx := context.Background()

	const writers = 8
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(w+1), 2))
			for i := 0; i < 50; i++ {
				ev := mkEvent(uint64(w*1000+i), randPubkey(rng), 1, "", "", int64(1_700_000_000+i), "race")
				if err := db.SaveEvent(ctx, ev); err != nil && !errors.Is(err, pebbledb2.ErrClosed) {
					t.Errorf("save: %v", err)
					return
				}
				if i%3 == 0 {
					if _, err := db.ReplaceEvent(ctx, ev); err != nil && !errors.Is(err, pebbledb2.ErrClosed) {
						t.Errorf("replace: %v", err)
						return
					}
				}
			}
		}(w)
	}
	// let some writes land, then race Close against the rest
	time.Sleep(2 * time.Millisecond)
	require.NoError(t, db.Close())
	wg.Wait()

	// every write after Close must fail cleanly, never panic
	ev := mkEvent(999999, nostr.PubKey{}, 1, "", "", 1_800_000_000, "post close")
	err = db.SaveEvent(ctx, ev)
	require.ErrorIs(t, err, pebbledb2.ErrClosed)
	_, err = db.ReplaceEvent(ctx, ev)
	require.ErrorIs(t, err, pebbledb2.ErrClosed)
}

func TestPebbleDB2AddressableNoResidue(t *testing.T) {
	db, err := pebbledb2.Open(pebbledb2.Options{Dir: t.TempDir(), MaxLimit: 10})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ctx := context.Background()
	pk := randPubkey(rand.New(rand.NewPCG(2, 3)))

	// 120 replacements of the same (pubkey, kind=30000, d) tuple; with the
	// old MaxLimit-clamped version lookup, old versions beyond 10 would
	// never be deleted and would leak into queries.
	for i := 0; i < 120; i++ {
		ev := mkEvent(uint64(i+1), pk, 30000, "d", "channel-1", int64(1_700_000_000+i), fmt.Sprintf("v%d", i))
		_, err := db.ReplaceEvent(ctx, ev)
		require.NoError(t, err)
	}

	got := querySearch(t, db, nostr.Filter{Authors: []nostr.PubKey{pk}, Kinds: []nostr.Kind{30000}})
	require.Len(t, got, 1)
	require.Equal(t, mkEvent(120, pk, 30000, "d", "channel-1", int64(1_700_000_119), "x").ID, got[0])
}

func TestPebbleDB2LimitZero(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()
	ev := mkEvent(1, nostr.PubKey{}, 1, "", "", 100, "hello world")
	require.NoError(t, db.SaveEvent(ctx, ev))

	got := querySearch(t, db, nostr.Filter{Search: "hello", LimitZero: true})
	require.Empty(t, got)

	got = querySearch(t, db, nostr.Filter{Kinds: []nostr.Kind{1}, LimitZero: true})
	require.Empty(t, got)

	// plain limit 0 without LimitZero flag still means "server default"
	// in this backend; LimitZero is set by the relay layer only.
	cnt, err := db.CountEvents(ctx, nostr.Filter{Search: "hello"})
	require.NoError(t, err)
	require.Equal(t, int64(1), cnt)
}

func TestPebbleDB2CountDuplicateDimensions(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(3, 4))
	pk := randPubkey(rng)

	const n = 50
	for i := 0; i < n; i++ {
		ev := mkEvent(uint64(i+1), pk, 1, "", "", int64(1_700_000_000+i), "dup test")
		require.NoError(t, db.SaveEvent(ctx, ev))
	}

	// author+kind counter fast path: duplicated dimensions must not double-count
	cnt, err := db.CountEvents(ctx, nostr.Filter{Authors: []nostr.PubKey{pk, pk}, Kinds: []nostr.Kind{1, 1}})
	require.NoError(t, err)
	require.Equal(t, int64(n), cnt)

	// kind+time rollup path: wide range triggers day/hour counters
	cnt, err = db.CountEvents(ctx, nostr.Filter{
		Kinds: []nostr.Kind{1, 1},
		Since: 1_700_000_000,
		Until: 1_700_000_100,
	})
	require.NoError(t, err)
	require.Equal(t, int64(n), cnt)
}

func TestPebbleDB2HLLFallbackLarge(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(4, 5))

	ref := randHex32(rng)
	// many events spread over a wide time range so the fallback scan hits
	// multiple shards
	const n = 4000
	for i := 0; i < n; i++ {
		ts := int64(1_000_000 + i*1000)
		ev := mkEvent(uint64(i+1), randPubkey(rng), 1111, "q", ref, ts, "comment")
		require.NoError(t, db.SaveEvent(ctx, ev))
	}

	// kind 1 has no sketch for this ref, so CountEventsHLL falls back to the
	// exact sharded scan; the result must be exact and complete
	filter := nostr.Filter{Tags: nostr.TagMap{"q": {ref}}, Kinds: []nostr.Kind{1, 1111}}
	count, hll, err := db.CountEventsHLL(ctx, filter, 8)
	require.NoError(t, err)
	require.Nil(t, hll)
	require.Equal(t, int64(n), count)
}
