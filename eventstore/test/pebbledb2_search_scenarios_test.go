package test

import (
	"context"
	"encoding/binary"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"

	"fiatjaf.com/nostr"
	pebbledb2 "fiatjaf.com/nostr/eventstore/pebbledb2"
	"github.com/cockroachdb/pebble/v2"
	"github.com/stretchr/testify/require"
)

// deleteSignature removes the content-signature ('F') key of an event,
// simulating a database written before the signature index existed.
func deleteSignature(t *testing.T, dir string, ev nostr.Event) {
	t.Helper()
	db, err := pebble.Open(dir, &pebble.Options{Merger: pebbledb2.CounterMerger})
	require.NoError(t, err)
	key := make([]byte, 1+8+32)
	key[0] = 'F'
	binary.BigEndian.PutUint64(key[1:], uint64(ev.CreatedAt))
	copy(key[9:], ev.ID[:])
	require.NoError(t, db.Delete(key, pebble.Sync))
	require.NoError(t, db.Close())
}

// TestPebbleDB2SearchLegacyMissingSignatures verifies that events whose
// signature key is absent (legacy rows) are still found even when their
// created_at sits inside the range covered by other signatures.
func TestPebbleDB2SearchLegacyMissingSignatures(t *testing.T) {
	dir := t.TempDir()
	db, err := pebbledb2.OpenDir(dir)
	require.NoError(t, err)
	ctx := context.Background()

	evA := mkEvent(1, nostr.PubKey{}, 1, "", "", 100, "alpha one")
	evB := mkEvent(2, nostr.PubKey{}, 1, "", "", 200, "beta one")
	evC := mkEvent(3, nostr.PubKey{}, 1, "", "", 300, "gamma one")
	for _, ev := range []nostr.Event{evA, evB, evC} {
		require.NoError(t, db.SaveEvent(ctx, ev))
	}
	require.NoError(t, db.Close())

	// remove only B's signature: the oldest remaining signature is A (ts=100),
	// so a naive "everything at or after 100 has a signature" assumption breaks.
	deleteSignature(t, dir, evB)

	db, err = pebbledb2.OpenDir(dir)
	require.NoError(t, err)
	defer db.Close()

	require.Equal(t, []nostr.ID{evB.ID}, querySearch(t, db, nostr.Filter{Search: "beta"}))
	require.Equal(t, []nostr.ID{evA.ID}, querySearch(t, db, nostr.Filter{Search: "alpha"}))
	require.Equal(t, []nostr.ID{evC.ID, evB.ID, evA.ID}, querySearch(t, db, nostr.Filter{Search: "one"}))
}

func TestPebbleDB2SearchSinceUntilBoundaries(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()
	events := []nostr.Event{
		mkEvent(1, nostr.PubKey{}, 1, "", "", 100, "hello world"),
		mkEvent(2, nostr.PubKey{}, 1, "", "", 200, "hello again"),
		mkEvent(3, nostr.PubKey{}, 1, "", "", 300, "hello once more"),
	}
	for _, ev := range events {
		require.NoError(t, db.SaveEvent(ctx, ev))
	}

	// inclusive boundaries: since == 200 and until == 300 both match ev2/ev3
	got := querySearch(t, db, nostr.Filter{Search: "hello", Since: 200, Until: 300})
	require.Equal(t, []nostr.ID{events[2].ID, events[1].ID}, got)

	got = querySearch(t, db, nostr.Filter{Search: "hello", Since: 200, Until: 200})
	require.Equal(t, []nostr.ID{events[1].ID}, got)

	got = querySearch(t, db, nostr.Filter{Search: "hello", Since: 301})
	require.Empty(t, got)

	got = querySearch(t, db, nostr.Filter{Search: "hello", Until: 99})
	require.Empty(t, got)

	// single-point range with the exact ts
	got = querySearch(t, db, nostr.Filter{Search: "hello", Since: 300, Until: 300})
	require.Equal(t, []nostr.ID{events[2].ID}, got)

	cnt, err := db.CountEvents(ctx, nostr.Filter{Search: "hello", Since: 200})
	require.NoError(t, err)
	require.Equal(t, int64(2), cnt)
}

func TestPebbleDB2SearchSameTimestampOrdering(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()
	// three events at the same ts; ordering must tie-break by id (desc)
	events := []nostr.Event{
		mkEvent(10, nostr.PubKey{}, 1, "", "", 500, "same time"),
		mkEvent(20, nostr.PubKey{}, 1, "", "", 500, "same time"),
		mkEvent(30, nostr.PubKey{}, 1, "", "", 500, "same time"),
	}
	for _, ev := range events {
		require.NoError(t, db.SaveEvent(ctx, ev))
	}

	got := querySearch(t, db, nostr.Filter{Search: "same"})
	require.Len(t, got, 3)
	// mkEvent puts the numeric id in the first 8 bytes big-endian, so
	// descending id order is 30, 20, 10.
	require.Equal(t, []nostr.ID{events[2].ID, events[1].ID, events[0].ID}, got)
}

func TestPebbleDB2SearchCombinedFilters(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(1, 1))
	pk1 := randPubkey(rng)
	pk2 := randPubkey(rand.New(rand.NewPCG(2, 2)))

	events := []nostr.Event{
		mkEvent(1, pk1, 1, "t", "news", 100, "breaking news today"),
		mkEvent(2, pk1, 1, "t", "sports", 200, "sports news update"),
		mkEvent(3, pk2, 1, "t", "news", 300, "news from abroad"),
		mkEvent(4, pk1, 2, "t", "news", 400, "news as a non-kind-1"),
	}
	for _, ev := range events {
		require.NoError(t, db.SaveEvent(ctx, ev))
	}

	// kind + search
	got := querySearch(t, db, nostr.Filter{Search: "news", Kinds: []nostr.Kind{1}})
	require.Equal(t, []nostr.ID{events[2].ID, events[1].ID, events[0].ID}, got)

	// author + search
	got = querySearch(t, db, nostr.Filter{Search: "news", Authors: []nostr.PubKey{pk1}})
	require.Equal(t, []nostr.ID{events[3].ID, events[1].ID, events[0].ID}, got)

	// tag + search
	got = querySearch(t, db, nostr.Filter{Search: "news", Tags: nostr.TagMap{"t": {"news"}}})
	require.Equal(t, []nostr.ID{events[3].ID, events[2].ID, events[0].ID}, got)

	// author + kind + search
	got = querySearch(t, db, nostr.Filter{Search: "news", Authors: []nostr.PubKey{pk1}, Kinds: []nostr.Kind{1}})
	require.Equal(t, []nostr.ID{events[1].ID, events[0].ID}, got)

	// ids + search
	got = querySearch(t, db, nostr.Filter{Search: "news", IDs: []nostr.ID{events[3].ID, events[1].ID, events[0].ID}})
	require.Equal(t, []nostr.ID{events[3].ID, events[1].ID, events[0].ID}, got)

	// counts agree with queries
	cnt, err := db.CountEvents(ctx, nostr.Filter{Search: "news", Authors: []nostr.PubKey{pk1}, Kinds: []nostr.Kind{1}})
	require.NoError(t, err)
	require.Equal(t, int64(2), cnt)

	// no match at all
	got = querySearch(t, db, nostr.Filter{Search: "zzzz"})
	require.Empty(t, got)
	cnt, err = db.CountEvents(ctx, nostr.Filter{Search: "zzzz"})
	require.NoError(t, err)
	require.Equal(t, int64(0), cnt)
}

func TestPebbleDB2SearchPatternEdgeCases(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()
	events := []nostr.Event{
		mkEvent(1, nostr.PubKey{}, 1, "", "", 100, "aaaaaa"),
		mkEvent(2, nostr.PubKey{}, 1, "", "", 200, "xx\tnewline\nand punctuation!@#"),
		mkEvent(3, nostr.PubKey{}, 1, "", "", 300, strings.Repeat("abcdefghijklmnopqrstuvwxyz", 4)), // 26 distinct trigrams
		mkEvent(4, nostr.PubKey{}, 1, "", "", 400, "abc"),                                           // exactly 3 bytes
		mkEvent(5, nostr.PubKey{}, 1, "", "", 500, "ab"),                                            // 2 bytes, no signature
		mkEvent(6, nostr.PubKey{}, 1, "", "", 600, ""),                                              // empty
	}
	for _, ev := range events {
		require.NoError(t, db.SaveEvent(ctx, ev))
	}

	// repeated-char pattern inside repeated-char content
	require.Equal(t, []nostr.ID{events[0].ID}, querySearch(t, db, nostr.Filter{Search: "aaaa"}))
	require.Equal(t, []nostr.ID{events[0].ID}, querySearch(t, db, nostr.Filter{Search: "aaa"}))

	// special characters survive the bloom + BMH paths
	require.Equal(t, []nostr.ID{events[1].ID}, querySearch(t, db, nostr.Filter{Search: "newline"}))
	require.Equal(t, []nostr.ID{events[1].ID}, querySearch(t, db, nostr.Filter{Search: "!@#"}))

	// long patterns (more than the 16 tested trigrams) must still recall
	require.Equal(t, []nostr.ID{events[2].ID}, querySearch(t, db, nostr.Filter{Search: "abcdefghijklmnopqrstuvwxyz"}))
	require.Equal(t, []nostr.ID{events[2].ID}, querySearch(t, db, nostr.Filter{Search: "uvwxyzabcd"}))

	// exactly-3-byte content and pattern
	require.Equal(t, []nostr.ID{events[3].ID, events[2].ID}, querySearch(t, db, nostr.Filter{Search: "abc"}))
	require.Equal(t, []nostr.ID{events[2].ID}, querySearch(t, db, nostr.Filter{Search: "abcd"}))

	// 2-byte pattern falls back to the generic scan and still matches
	require.Equal(t, []nostr.ID{events[4].ID, events[3].ID, events[2].ID}, querySearch(t, db, nostr.Filter{Search: "ab"}))
	require.Empty(t, querySearch(t, db, nostr.Filter{Search: "zz"}))
	require.Empty(t, querySearch(t, db, nostr.Filter{Search: "empty"}))
}

func TestPebbleDB2SearchLimit(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		ev := mkEvent(uint64(i+1), nostr.PubKey{}, 1, "", "", int64(100+i), "limited results")
		require.NoError(t, db.SaveEvent(ctx, ev))
	}

	var got []nostr.ID
	for ev, err := range db.QueryEvents(ctx, nostr.Filter{Search: "limited", Limit: 5}, 1000) {
		require.NoError(t, err)
		got = append(got, ev.ID)
	}
	require.Len(t, got, 5)
	// newest first: ts 129 .. 125
	require.Equal(t, []nostr.ID{
		mkEvent(30, nostr.PubKey{}, 1, "", "", 129, "x").ID,
		mkEvent(29, nostr.PubKey{}, 1, "", "", 128, "x").ID,
		mkEvent(28, nostr.PubKey{}, 1, "", "", 127, "x").ID,
		mkEvent(27, nostr.PubKey{}, 1, "", "", 126, "x").ID,
		mkEvent(26, nostr.PubKey{}, 1, "", "", 125, "x").ID,
	}, got)

	cnt, err := db.CountEvents(ctx, nostr.Filter{Search: "limited"})
	require.NoError(t, err)
	require.Equal(t, int64(30), cnt)
}

func TestPebbleDB2SearchMixedUTF8(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()
	events := []nostr.Event{
		mkEvent(1, nostr.PubKey{}, 1, "", "", 100, "中文 hello 世界"),
		mkEvent(2, nostr.PubKey{}, 1, "", "", 200, "hello 中文 mixed"),
	}
	for _, ev := range events {
		require.NoError(t, db.SaveEvent(ctx, ev))
	}

	require.Equal(t, []nostr.ID{events[1].ID, events[0].ID}, querySearch(t, db, nostr.Filter{Search: "中文"}))
	require.Equal(t, []nostr.ID{events[1].ID, events[0].ID}, querySearch(t, db, nostr.Filter{Search: "hello"}))
	require.Equal(t, []nostr.ID{events[1].ID}, querySearch(t, db, nostr.Filter{Search: "mixed"}))
}

func TestPebbleDB2SearchConcurrent(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(9, 9))
	words := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	for i := 0; i < 200; i++ {
		word := words[i%len(words)]
		ev := mkEvent(uint64(i+1), randPubkey(rng), 1, "", "", int64(1_700_000_000+i), "post about "+word)
		require.NoError(t, db.SaveEvent(ctx, ev))
	}

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			word := words[w%len(words)]
			cnt, err := db.CountEvents(ctx, nostr.Filter{Search: word})
			if err != nil {
				t.Errorf("count: %v", err)
				return
			}
			if cnt != 40 {
				t.Errorf("word %q: got %d want 40", word, cnt)
			}
		}(w)
	}
	wg.Wait()
}

func TestPebbleDB2SearchNoSignaturesAtAll(t *testing.T) {
	dir := t.TempDir()
	db, err := pebbledb2.OpenDir(dir)
	require.NoError(t, err)
	ctx := context.Background()

	events := []nostr.Event{
		mkEvent(1, nostr.PubKey{}, 1, "", "", 100, "first phrase"),
		mkEvent(2, nostr.PubKey{}, 1, "", "", 200, "second phrase"),
		mkEvent(3, nostr.PubKey{}, 1, "", "", 300, "third phrase"),
	}
	for _, ev := range events {
		require.NoError(t, db.SaveEvent(ctx, ev))
	}
	require.NoError(t, db.Close())

	// a fully legacy database: no signature keys at all
	for _, ev := range events {
		deleteSignature(t, dir, ev)
	}

	db, err = pebbledb2.OpenDir(dir)
	require.NoError(t, err)
	defer db.Close()

	require.Equal(t, []nostr.ID{events[2].ID, events[1].ID, events[0].ID},
		querySearch(t, db, nostr.Filter{Search: "phrase"}))
	require.Equal(t, []nostr.ID{events[1].ID}, querySearch(t, db, nostr.Filter{Search: "second"}))
	require.Empty(t, querySearch(t, db, nostr.Filter{Search: "absent"}))

	cnt, err := db.CountEvents(ctx, nostr.Filter{Search: "phrase"})
	require.NoError(t, err)
	require.Equal(t, int64(3), cnt)
}

func TestPebbleDB2SearchSaturatedBloom(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(7, 7))
	alphabet := "abcdefghijklmnopqrstuvwxyz0123456789 "

	// ~2000 distinct trigrams saturate the 1024-bit bloom; correctness then
	// rests entirely on the BMH substring check after the body read.
	long := make([]byte, 0, 700)
	for len(long) < 700 {
		long = append(long, alphabet[rng.IntN(len(alphabet))])
	}
	content := string(long) + " needle"
	ev := mkEvent(1, nostr.PubKey{}, 1, "", "", 100, content)
	require.NoError(t, db.SaveEvent(ctx, ev))

	require.Equal(t, []nostr.ID{ev.ID}, querySearch(t, db, nostr.Filter{Search: "needle"}))
	require.Equal(t, []nostr.ID{ev.ID}, querySearch(t, db, nostr.Filter{Search: content[:30]}))
	require.Empty(t, querySearch(t, db, nostr.Filter{Search: "zzzzzzzz"}))
}

func TestPebbleDB2SearchWideRangeDistribution(t *testing.T) {
	db := openPebbleDB2(t)
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(8, 8))

	// spread events across a wide ts range so every shard has data; results
	// must be complete, deduplicated and globally ordered
	var want int
	for i := 0; i < 300; i++ {
		ts := int64(1_000_000 + i*1_000_000) // 1e6 .. ~3e8
		word := "needle"
		if i%3 == 0 {
			word = "plaintext"
		}
		ev := mkEvent(uint64(i+1), randPubkey(rng), 1, "", "", ts, "a "+word+" post")
		if word == "needle" {
			want++
		}
		require.NoError(t, db.SaveEvent(ctx, ev))
	}

	got := querySearch(t, db, nostr.Filter{Search: "needle"})
	require.Len(t, got, want)
	// globally ordered newest-first
	for i := 1; i < len(got); i++ {
		prev := binary.BigEndian.Uint64(got[i-1][:8])
		cur := binary.BigEndian.Uint64(got[i][:8])
		require.Greater(t, prev, cur, "ids must be descending")
	}
	cnt, err := db.CountEvents(ctx, nostr.Filter{Search: "needle"})
	require.NoError(t, err)
	require.Equal(t, int64(want), cnt)
}
