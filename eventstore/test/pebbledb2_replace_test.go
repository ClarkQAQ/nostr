package test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	pebbledb2 "fiatjaf.com/nostr/eventstore/pebbledb2"
	"github.com/stretchr/testify/require"
)

// TestPebbleDB2_NegativeTimestamp verifies that SaveEvent and ReplaceEvent
// reject events with CreatedAt < 0 (they would corrupt the big-endian
// uint64 time ordering in index keys).
func TestPebbleDB2_NegativeTimestamp(t *testing.T) {
	os.RemoveAll(dbpath + "pebble2-negts")
	db, err := pebbledb2.OpenDir(dbpath + "pebble2-negts")
	require.NoError(t, err)
	defer db.Close()

	sk := sk3
	pk := nostr.GetPublicKey(sk)

	// Save path
	ev := nostr.Event{
		CreatedAt: nostr.Timestamp(-100),
		Content:   "bad ts",
		Kind:      1,
	}
	require.NoError(t, ev.Sign(sk))
	err = db.SaveEvent(context.Background(), ev)
	require.ErrorIs(t, err, pebbledb2.ErrInvalidTimestamp)

	// Replace path
	ev2 := nostr.Event{
		CreatedAt: nostr.Timestamp(-50),
		Content:   "bad ts replace",
		Kind:      0,
	}
	require.NoError(t, ev2.Sign(sk))
	_, err = db.ReplaceEvent(context.Background(), ev2)
	require.ErrorIs(t, err, pebbledb2.ErrInvalidTimestamp)

	// Verify nothing was written
	n, err := db.CountEvents(context.Background(), nostr.Filter{
		Authors: []nostr.PubKey{pk},
	})
	require.NoError(t, err)
	require.Equal(t, int64(0), n)
}

// TestPebbleDB2_ConcurrentReplace races N goroutines calling ReplaceEvent on
// the same (pubkey, kind). Without serialization the final DB can hold more
// than one version; with it exactly one must survive: the one with the
// highest CreatedAt.
func TestPebbleDB2_ConcurrentReplace(t *testing.T) {
	os.RemoveAll(dbpath + "pebble2-race")
	db, err := pebbledb2.OpenDir(dbpath + "pebble2-race")
	require.NoError(t, err)
	defer db.Close()

	const N = 100
	sk := sk3
	pk := nostr.GetPublicKey(sk)

	var wg sync.WaitGroup
	errCh := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ev := nostr.Event{
				CreatedAt: nostr.Timestamp(1000 + i), // each goroutine a distinct ts
				Content:   fmt.Sprintf(`{"name":"v%d"}`, i),
				Kind:      0, // replaceable
			}
			if err := ev.Sign(sk); err != nil {
				errCh <- err
				return
			}
			if _, err := db.ReplaceEvent(context.Background(), ev); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		t.Fatalf("replace error: %v", e)
	}

	results := eventstore.CollectEvents(db.QueryEvents(context.Background(), nostr.Filter{
		Authors: []nostr.PubKey{pk},
		Kinds:   []nostr.Kind{0},
	}, 1000))
	require.Len(t, results, 1, "exactly one version must survive")
	require.Equal(t, nostr.Timestamp(1000+N-1), results[0].CreatedAt,
		"the surviving version must be the one with max CreatedAt")
}

// TestPebbleDB2_ConcurrentAddressableReplace is the same race but for
// addressable events (kind 30023) sharing a d-tag.
func TestPebbleDB2_ConcurrentAddressableReplace(t *testing.T) {
	os.RemoveAll(dbpath + "pebble2-race-addr")
	db, err := pebbledb2.OpenDir(dbpath + "pebble2-race-addr")
	require.NoError(t, err)
	defer db.Close()

	const N = 100
	sk := sk4
	pk := nostr.GetPublicKey(sk)

	var wg sync.WaitGroup
	errCh := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ev := nostr.Event{
				CreatedAt: nostr.Timestamp(2000 + i),
				Content:   fmt.Sprintf("version %d", i),
				Tags:      nostr.Tags{{"d", "race-article"}},
				Kind:      30023, // addressable
			}
			if err := ev.Sign(sk); err != nil {
				errCh <- err
				return
			}
			if _, err := db.ReplaceEvent(context.Background(), ev); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		t.Fatalf("replace error: %v", e)
	}

	results := eventstore.CollectEvents(db.QueryEvents(context.Background(), nostr.Filter{
		Authors: []nostr.PubKey{pk},
		Kinds:   []nostr.Kind{30023},
		Tags:    nostr.TagMap{"d": []string{"race-article"}},
	}, 1000))
	require.Len(t, results, 1, "exactly one version must survive")
	require.Equal(t, nostr.Timestamp(2000+N-1), results[0].CreatedAt)
}

// TestPebbleDB2_ReplaceDeletedListMatchesActual verifies that the deleted
// list returned by ReplaceEvent matches what was actually removed from DB.
func TestPebbleDB2_ReplaceDeletedListMatchesActual(t *testing.T) {
	os.RemoveAll(dbpath + "pebble2-repl-del")
	db, err := pebbledb2.OpenDir(dbpath + "pebble2-repl-del")
	require.NoError(t, err)
	defer db.Close()

	sk := sk3
	pk := nostr.GetPublicKey(sk)

	// Save 5 sequential versions (each ReplaceEvent deletes the previous).
	var allSaved []nostr.Event
	for i := 0; i < 5; i++ {
		ev := nostr.Event{
			CreatedAt: nostr.Timestamp(3000 + i),
			Content:   fmt.Sprintf("v%d", i),
			Kind:      0,
		}
		require.NoError(t, ev.Sign(sk))
		deleted, err := db.ReplaceEvent(context.Background(), ev)
		require.NoError(t, err)
		if i == 0 {
			require.Empty(t, deleted, "first save deletes nothing")
		} else {
			require.Len(t, deleted, 1, "each replace deletes exactly one prior version")
			require.Equal(t, allSaved[len(allSaved)-1].ID, deleted[0].ID)
		}
		allSaved = append(allSaved, ev)
	}

	// Final DB state: only the newest survives.
	results := eventstore.CollectEvents(db.QueryEvents(context.Background(), nostr.Filter{
		Authors: []nostr.PubKey{pk},
		Kinds:   []nostr.Kind{0},
	}, 1000))
	require.Len(t, results, 1)
	require.Equal(t, allSaved[4].ID, results[0].ID)
}
