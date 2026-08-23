package test

import (
	"context"
	"encoding/binary"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"testing"

	"fiatjaf.com/nostr"
	pebbledb2 "fiatjaf.com/nostr/eventstore/pebbledb2"
	"fiatjaf.com/nostr/nip45"
	"github.com/stretchr/testify/require"
)

func openPebbleDB2(t *testing.T) *pebbledb2.PebbleStore {
	t.Helper()
	db, err := pebbledb2.OpenDir(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

func randHex32(rng *rand.Rand) string {
	var b [32]byte
	for i := range b {
		b[i] = byte(rng.UintN(256))
	}
	return nostr.HexEncodeToString(b[:])
}

func randPubkey(rng *rand.Rand) nostr.PubKey {
	var b [32]byte
	for i := range b {
		b[i] = byte(rng.UintN(256))
	}
	return nostr.PubKey(b)
}

// mkEvent builds a store-ready event with a deterministic unique id and
// optional single tag; no signing needed since the store does not verify.
func mkEvent(id uint64, pk nostr.PubKey, kind nostr.Kind, tagKey, ref string, ts int64, content string) nostr.Event {
	var idb [32]byte
	binary.BigEndian.PutUint64(idb[:8], id)
	var tags nostr.Tags
	if tagKey != "" {
		tags = nostr.Tags{{tagKey, ref}}
	}
	return nostr.Event{ID: nostr.ID(idb), PubKey: pk, Kind: kind, CreatedAt: nostr.Timestamp(ts), Tags: tags, Content: content}
}

func querySearch(t *testing.T, db *pebbledb2.PebbleStore, filter nostr.Filter) []nostr.ID {
	t.Helper()
	var ids []nostr.ID
	for ev, err := range db.QueryEvents(context.Background(), filter, 1000) {
		require.NoError(t, err)
		ids = append(ids, ev.ID)
	}
	return ids
}

func TestPebbleDB2HLLReactionAccuracy(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(1, 2))

	ref := randHex32(rng)
	filter := nostr.Filter{Tags: nostr.TagMap{"e": {ref}}, Kinds: []nostr.Kind{7}}
	offset := nip45.HyperLogLogEventPubkeyOffsetForFilter(filter)
	require.NotEqual(t, -1, offset)

	const n = 3000
	for i := 0; i < n; i++ {
		ev := mkEvent(uint64(i+1), randPubkey(rng), 7, "e", ref, int64(1_700_000_000+i), "reaction")
		require.NoError(t, db.SaveEvent(ctx, ev))
	}

	count, hll, err := db.CountEventsHLL(ctx, filter, offset)
	require.NoError(t, err)
	require.NotNil(t, hll)
	require.Len(t, hll.GetRegisters(), 256)
	require.InDelta(t, float64(n), float64(count), float64(n)*0.15)

	exact, err := db.CountEvents(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, int64(n), exact)
}

func TestPebbleDB2HLLFollowerCount(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(2, 3))

	target := randHex32(rng)
	filter := nostr.Filter{Tags: nostr.TagMap{"p": {target}}, Kinds: []nostr.Kind{3}}
	offset := nip45.HyperLogLogEventPubkeyOffsetForFilter(filter)
	require.NotEqual(t, -1, offset)

	const n = 1000
	for i := 0; i < n; i++ {
		ev := mkEvent(uint64(i+1), randPubkey(rng), 3, "p", target, int64(1_700_000_000+i), "following")
		require.NoError(t, db.SaveEvent(ctx, ev))
	}

	count, _, err := db.CountEventsHLL(ctx, filter, offset)
	require.NoError(t, err)
	require.InDelta(t, float64(n), float64(count), float64(n)*0.15)
}

func TestPebbleDB2HLLTagIsolation(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(3, 4))

	refE := randHex32(rng)
	refQ := randHex32(rng)
	for i := 0; i < 100; i++ {
		require.NoError(t, db.SaveEvent(ctx, mkEvent(uint64(i+1), randPubkey(rng), 1, "e", refE, int64(1_700_000_000+i), "reply")))
		require.NoError(t, db.SaveEvent(ctx, mkEvent(uint64(1000+i+1), randPubkey(rng), 1, "q", refQ, int64(1_700_001_000+i), "quote")))
	}

	filterE := nostr.Filter{Tags: nostr.TagMap{"e": {refE}}, Kinds: []nostr.Kind{1}}
	countE, _, err := db.CountEventsHLL(ctx, filterE, nip45.HyperLogLogEventPubkeyOffsetForFilter(filterE))
	require.NoError(t, err)
	require.InDelta(t, 100, countE, 15)

	filterQ := nostr.Filter{Tags: nostr.TagMap{"q": {refQ}}, Kinds: []nostr.Kind{1, 1111}}
	countQ, _, err := db.CountEventsHLL(ctx, filterQ, nip45.HyperLogLogEventPubkeyOffsetForFilter(filterQ))
	require.NoError(t, err)
	require.InDelta(t, 100, countQ, 15)

	// the #e sketch for refQ must not see the #q events
	filterENoQ := nostr.Filter{Tags: nostr.TagMap{"e": {refQ}}, Kinds: []nostr.Kind{1}}
	countENoQ, _, err := db.CountEventsHLL(ctx, filterENoQ, nip45.HyperLogLogEventPubkeyOffsetForFilter(filterENoQ))
	require.NoError(t, err)
	require.Equal(t, int64(0), countENoQ)
}

func TestPebbleDB2HLLQuoteMerge(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(4, 5))

	ref := randHex32(rng)
	for i := 0; i < 80; i++ {
		require.NoError(t, db.SaveEvent(ctx, mkEvent(uint64(i+1), randPubkey(rng), 1, "q", ref, int64(1_700_000_000+i), "quote")))
	}
	for i := 0; i < 70; i++ {
		require.NoError(t, db.SaveEvent(ctx, mkEvent(uint64(1000+i+1), randPubkey(rng), 1111, "q", ref, int64(1_700_001_000+i), "comment")))
	}

	filter := nostr.Filter{Tags: nostr.TagMap{"q": {ref}}, Kinds: []nostr.Kind{1, 1111}}
	count, hll, err := db.CountEventsHLL(ctx, filter, nip45.HyperLogLogEventPubkeyOffsetForFilter(filter))
	require.NoError(t, err)
	require.NotNil(t, hll)
	require.InDelta(t, 150, count, 22.5)
}

func TestPebbleDB2HLLFallbackOnMissingSketch(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(5, 6))

	ref := randHex32(rng)
	// only kind-1 quotes exist, so the kind-1111 #q sketch is missing
	for i := 0; i < 50; i++ {
		require.NoError(t, db.SaveEvent(ctx, mkEvent(uint64(i+1), randPubkey(rng), 1, "q", ref, int64(1_700_000_000+i), "quote")))
	}

	filter := nostr.Filter{Tags: nostr.TagMap{"q": {ref}}, Kinds: []nostr.Kind{1, 1111}}
	count, hll, err := db.CountEventsHLL(ctx, filter, nip45.HyperLogLogEventPubkeyOffsetForFilter(filter))
	require.NoError(t, err)
	require.Nil(t, hll)
	require.Equal(t, int64(50), count)

	// never-referenced ref: exact 0, nil sketch
	ref2 := randHex32(rng)
	filter2 := nostr.Filter{Tags: nostr.TagMap{"e": {ref2}}, Kinds: []nostr.Kind{7}}
	count2, hll2, err := db.CountEventsHLL(ctx, filter2, nip45.HyperLogLogEventPubkeyOffsetForFilter(filter2))
	require.NoError(t, err)
	require.Nil(t, hll2)
	require.Equal(t, int64(0), count2)
}

func TestPebbleDB2HLLDeleteIsMonotonic(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(6, 7))

	ref := randHex32(rng)
	filter := nostr.Filter{Tags: nostr.TagMap{"e": {ref}}, Kinds: []nostr.Kind{7}}
	offset := nip45.HyperLogLogEventPubkeyOffsetForFilter(filter)

	const n = 500
	ids := make([]nostr.ID, 0, n)
	for i := 0; i < n; i++ {
		ev := mkEvent(uint64(i+1), randPubkey(rng), 7, "e", ref, int64(1_700_000_000+i), "reaction")
		ids = append(ids, ev.ID)
		require.NoError(t, db.SaveEvent(ctx, ev))
	}

	before, _, err := db.CountEventsHLL(ctx, filter, offset)
	require.NoError(t, err)

	for i := 0; i < n/2; i++ {
		require.NoError(t, db.DeleteEvent(ctx, ids[i]))
	}

	after, hll, err := db.CountEventsHLL(ctx, filter, offset)
	require.NoError(t, err)
	require.NotNil(t, hll)
	require.GreaterOrEqual(t, after, before)

	exact, err := db.CountEvents(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, int64(n/2), exact)
}

func TestPebbleDB2HLLConcurrentWrites(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()

	ref := randHex32(rand.New(rand.NewPCG(7, 8)))
	filter := nostr.Filter{Tags: nostr.TagMap{"e": {ref}}, Kinds: []nostr.Kind{7}}
	offset := nip45.HyperLogLogEventPubkeyOffsetForFilter(filter)

	const writers, per = 8, 300
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(w+1), 0))
			for i := 0; i < per; i++ {
				id := uint64(w)*per + uint64(i) + 1
				ev := mkEvent(id, randPubkey(rng), 7, "e", ref, int64(1_700_000_000+id), "reaction")
				if err := db.SaveEvent(ctx, ev); err != nil {
					t.Errorf("save: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	count, hll, err := db.CountEventsHLL(ctx, filter, offset)
	require.NoError(t, err)
	require.NotNil(t, hll)
	require.InDelta(t, float64(writers*per), float64(count), float64(writers*per)*0.15)
}

func TestPebbleDB2HLLNotEligible(t *testing.T) {
	db := openPebbleDB2(t)
	_, _, err := db.CountEventsHLL(context.Background(), nostr.Filter{Since: 100, Tags: nostr.TagMap{"e": {"x"}}, Kinds: []nostr.Kind{7}}, 8)
	require.ErrorIs(t, err, pebbledb2.ErrHLLNotEligible)
}

func TestPebbleDB2SearchBasic(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()

	events := []nostr.Event{
		mkEvent(1, nostr.PubKey{}, 1, "", "", 1000, "Hello World from the relay"),
		mkEvent(2, nostr.PubKey{}, 1, "", "", 2000, "HELLO there, everyone"),
		mkEvent(3, nostr.PubKey{}, 1, "", "", 3000, "nothing interesting here"),
		mkEvent(4, nostr.PubKey{}, 1, "", "", 4000, "hello"),
		mkEvent(5, nostr.PubKey{}, 2, "", "", 5000, "HELLO in another kind"),
	}
	for _, ev := range events {
		require.NoError(t, db.SaveEvent(ctx, ev))
	}

	// case-insensitive substring across all kinds, newest first
	ids := querySearch(t, db, nostr.Filter{Search: "hello"})
	require.Equal(t, []nostr.ID{events[4].ID, events[3].ID, events[1].ID, events[0].ID}, ids)

	cnt, err := db.CountEvents(ctx, nostr.Filter{Search: "hello"})
	require.NoError(t, err)
	require.Equal(t, int64(4), cnt)

	// combined with a kind constraint
	ids = querySearch(t, db, nostr.Filter{Search: "hello", Kinds: []nostr.Kind{1}})
	require.Equal(t, []nostr.ID{events[3].ID, events[1].ID, events[0].ID}, ids)

	// combined with a tag constraint
	ids = querySearch(t, db, nostr.Filter{Search: "hello", Tags: nostr.TagMap{"t": {"x"}}})
	require.Empty(t, ids)
}

func TestPebbleDB2SearchUTF8(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()
	events := []nostr.Event{
		mkEvent(1, nostr.PubKey{}, 1, "", "", 1000, "你好世界"),
		mkEvent(2, nostr.PubKey{}, 1, "", "", 2000, "今天天气不错"),
	}
	for _, ev := range events {
		require.NoError(t, db.SaveEvent(ctx, ev))
	}

	ids := querySearch(t, db, nostr.Filter{Search: "世界"})
	require.Equal(t, []nostr.ID{events[0].ID}, ids)

	cnt, err := db.CountEvents(ctx, nostr.Filter{Search: "天气"})
	require.NoError(t, err)
	require.Equal(t, int64(1), cnt)
}

func TestPebbleDB2SearchShortPattern(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()
	events := []nostr.Event{
		mkEvent(1, nostr.PubKey{}, 1, "", "", 1000, "abc"),
		mkEvent(2, nostr.PubKey{}, 1, "", "", 2000, "xyz"),
	}
	for _, ev := range events {
		require.NoError(t, db.SaveEvent(ctx, ev))
	}

	// sub-3-byte patterns fall back to the generic scan
	ids := querySearch(t, db, nostr.Filter{Search: "ab"})
	require.Equal(t, []nostr.ID{events[0].ID}, ids)
	cnt, err := db.CountEvents(ctx, nostr.Filter{Search: "bc"})
	require.NoError(t, err)
	require.Equal(t, int64(1), cnt)
}

func TestPebbleDB2SearchMatchesBruteForce(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(8, 9))

	alphabet := "abcdefghijklmnopqrstuvwxyz "
	const m = 400
	events := make([]nostr.Event, m)
	contents := make([]string, m)
	for i := range events {
		var sb strings.Builder
		l := 8 + rng.IntN(40)
		for j := 0; j < l; j++ {
			sb.WriteByte(alphabet[rng.IntN(len(alphabet))])
		}
		contents[i] = sb.String()
		events[i] = mkEvent(uint64(i+1), randPubkey(rng), 1, "", "", int64(1_700_000_000+i), contents[i])
		require.NoError(t, db.SaveEvent(ctx, events[i]))
	}

	brute := func(pattern string) []nostr.Event {
		lower := strings.ToLower(pattern)
		var out []nostr.Event
		for _, ev := range events {
			if strings.Contains(strings.ToLower(ev.Content), lower) {
				out = append(out, ev)
			}
		}
		slices.SortFunc(out, nostr.CompareEventReverse)
		return out
	}

	patterns := []string{"hello", "the", "world", "abc"}
	// plus substrings actually present in some contents (exercises recall)
	for i := 0; i < 8; i++ {
		idx := rng.IntN(m)
		c := contents[idx]
		if len(c) < 6 {
			continue
		}
		start := rng.IntN(len(c) - 4)
		patterns = append(patterns, c[start:start+4+rng.IntN(3)])
	}

	for _, pat := range patterns {
		expected := brute(pat)
		got := querySearch(t, db, nostr.Filter{Search: pat})
		require.Len(t, got, len(expected), "pattern %q", pat)
		for i := range got {
			require.Equal(t, expected[i].ID, got[i], "pattern %q pos %d", pat, i)
		}
		cnt, err := db.CountEvents(ctx, nostr.Filter{Search: pat})
		require.NoError(t, err)
		require.Equal(t, int64(len(expected)), cnt, "count pattern %q", pat)
	}
}

func TestPebbleDB2SearchDeleteReplace(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()

	ev1 := mkEvent(1, nostr.PubKey{}, 1, "", "", 1000, "the quick brown fox")
	require.NoError(t, db.SaveEvent(ctx, ev1))
	require.Len(t, querySearch(t, db, nostr.Filter{Search: "quick"}), 1)

	require.NoError(t, db.DeleteEvent(ctx, ev1.ID))
	require.Empty(t, querySearch(t, db, nostr.Filter{Search: "quick"}))

	// replaceable: v2 supersedes v1, v1's signature must go away
	ev2 := mkEvent(2, nostr.PubKey{}, 0, "", "", 2000, "alpha version")
	ev3 := mkEvent(3, nostr.PubKey{}, 0, "", "", 3000, "beta version")
	deleted, err := db.ReplaceEvent(ctx, ev2)
	require.NoError(t, err)
	require.Empty(t, deleted)
	deleted, err = db.ReplaceEvent(ctx, ev3)
	require.NoError(t, err)
	require.Len(t, deleted, 1)

	require.Empty(t, querySearch(t, db, nostr.Filter{Search: "alpha"}))
	require.Len(t, querySearch(t, db, nostr.Filter{Search: "beta"}), 1)
}

func TestPebbleDB2SearchScanBudget(t *testing.T) {
	db, err := pebbledb2.Open(pebbledb2.Options{Dir: t.TempDir(), MaxScanKeys: 200})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ctx := context.Background()

	for i := 0; i < 1000; i++ {
		ev := mkEvent(uint64(i+1), nostr.PubKey{}, 1, "", "", int64(1_700_000_000+i), "some content here")
		require.NoError(t, db.SaveEvent(ctx, ev))
	}

	_, err = db.CountEvents(ctx, nostr.Filter{Search: "content"})
	require.ErrorIs(t, err, pebbledb2.ErrScanBudgetExceeded)
}

func TestPebbleDB2SearchAfterReopen(t *testing.T) {
	dir := t.TempDir()
	db, err := pebbledb2.OpenDir(dir)
	require.NoError(t, err)
	ctx := context.Background()
	ev := mkEvent(1, nostr.PubKey{}, 1, "", "", 1000, "hello world")
	require.NoError(t, db.SaveEvent(ctx, ev))
	require.NoError(t, db.Close())

	db2, err := pebbledb2.OpenDir(dir)
	require.NoError(t, err)
	defer db2.Close()

	ids := querySearch(t, db2, nostr.Filter{Search: "hello"})
	require.Equal(t, []nostr.ID{ev.ID}, ids)
}
