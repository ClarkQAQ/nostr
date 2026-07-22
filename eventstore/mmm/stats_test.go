package mmm

import (
	"os"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/require"
)

func TestComputeStats(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mmm_stats_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)
	mmmm := &MultiMmapManager{
		Dir: tmpDir,
	}
	err = mmmm.Init()
	require.NoError(t, err)
	defer mmmm.Close()
	il, err := mmmm.EnsureLayer("testlayer")
	require.NoError(t, err)

	// generate 5 random keys
	keys := make([]nostr.SecretKey, 5)
	pubkeys := make([]nostr.PubKey, 5)
	for i := 0; i < 5; i++ {
		privkey := nostr.Generate()
		keys[i] = privkey
		pubkeys[i] = privkey.Public()
	}

	// add 10 events from each key, alternating between kinds 1 and 11
	for i := 0; i < 5; i++ {
		for j := 0; j < 10; j++ {
			kind := nostr.Kind(1)
			if j%2 == 1 {
				kind = 11
			}

			evt := nostr.Event{
				PubKey:    pubkeys[i],
				CreatedAt: nostr.Now() - nostr.Timestamp(j)*3600, // j hours ago
				Kind:      kind,
				Tags:      nil,
				Content:   "test event",
			}
			err := evt.Sign(keys[i])
			require.NoError(t, err)

			// save event
			err = il.SaveEvent(evt)
			require.NoError(t, err)
		}
	}

	// test ComputeStats with no options
	stats, err := il.ComputeStats(StatsOptions{})
	require.NoError(t, err)

	// verify total count
	require.Equal(t, uint(50), stats.Total)

	// verify we have stats for all 5 pubkeys
	require.Len(t, stats.PerPubKey, 5)

	// verify each pubkey has 10 events
	for _, pubkey := range pubkeys {
		pkStats, _ := stats.PerPubKey[pubkey]
		require.Equal(t, uint(10), pkStats.Total)
	}

	// verify we have stats for both kinds
	require.Len(t, stats.PerKind, 2)

	// verify kind counts (should be 25 each for kinds 1 and 11)
	kindStats1, exists := stats.PerKind[1]
	require.True(t, exists, "missing stats for kind 1")
	require.Equal(t, uint(25), kindStats1.Total, "expected 25 events for kind 1, got %d", kindStats1.Total)

	kindStats11, exists := stats.PerKind[11]
	require.True(t, exists, "missing stats for kind 11")
	require.Equal(t, uint(25), kindStats11.Total, "expected 25 events for kind 11, got %d", kindStats11.Total)

	// test ComputeStats with OnlyPubKey option
	firstPubkey := pubkeys[0]
	stats, err = il.ComputeStats(StatsOptions{OnlyPubKey: firstPubkey})
	require.NoError(t, err, "failed to compute stats with OnlyPubKey: %v", err)

	require.Equal(t, uint(10), stats.Total)
	require.Len(t, stats.PerPubKey, 1)

	// test ComputeStats with OnlyPubKey set to a non-existent pubkey (zero results)
	bogusPriv := nostr.Generate()
	bogusPubkey := bogusPriv.Public()
	stats, err = il.ComputeStats(StatsOptions{OnlyPubKey: bogusPubkey})
	require.NoError(t, err)
	require.Equal(t, uint(0), stats.Total)
	require.Empty(t, stats.PerPubKey)
	require.Empty(t, stats.PerKind)
}
