package mmm

import (
	"math/rand/v2"
	"os"
	"strings"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/PowerDNS/lmdb-go/lmdb"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func FuzzFreeRanges(f *testing.F) {
	f.Add(0)
	f.Fuzz(func(t *testing.T, seed int) {
		// create a temporary directory for the test
		tmpDir, err := os.MkdirTemp("", "mmm-freeranges-test-*")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		logger := zerolog.Nop()
		rnd := rand.New(rand.NewPCG(uint64(seed), 0))
		chance := func(n uint) bool {
			return rnd.UintN(100) < n
		}

		// initialize MMM
		mmmm := &MultiMmapManager{
			Dir:    tmpDir,
			Logger: &logger,
		}

		err = mmmm.Init()
		require.NoError(t, err)
		defer mmmm.Close()

		// create a single layer
		il, err := mmmm.EnsureLayer("a")
		require.NoError(t, err)
		defer il.Close()

		sk := nostr.MustSecretKeyFromHex("945e01e37662430162121b804d3645a86d97df9d256917d86735d0eb219393eb")

		total := 0
		for {
			freeBefore, spaceBefore := countUsableFreeRanges(t, mmmm)

			hasAdded := false
			for i := range rnd.IntN(40) {
				hasAdded = true

				content := "1" // ensure at least one event is as small as it can be
				if i > 0 {
					content = strings.Repeat("z", rnd.IntN(1000))
				}

				evt := nostr.Event{
					CreatedAt: nostr.Timestamp(rnd.Uint32()),
					Kind:      1,
					Content:   content,
					Tags:      nostr.Tags{},
				}
				evt.Sign(sk)
				err := il.SaveEvent(evt)
				require.NoError(t, err)

				total++
			}

			freeAfter, spaceAfter := countUsableFreeRanges(t, mmmm)
			if hasAdded && freeBefore > 0 {
				require.Lessf(t, spaceAfter, spaceBefore, "must use some of the existing free ranges when inserting new events (before: %d, after: %d)", freeBefore, freeAfter)
			}

			// delete some events
			if total > 0 {
				for range rnd.IntN(total) {
					for evt := range il.QueryEvents(nostr.Filter{}, 1) {
						err := il.DeleteEvent(evt.ID)
						require.NoError(t, err)

						total--
					}
				}
			}

			verifyFreeRangesInvariants(t, mmmm)

			// add more events
			for i := range rnd.IntN(40) {
				content := "1"
				if i > 0 {
					content = strings.Repeat("z", rnd.IntN(1000))
				}

				evt := nostr.Event{
					CreatedAt: nostr.Timestamp(rnd.Uint32()),
					Kind:      1,
					Content:   content,
					Tags:      nostr.Tags{},
				}
				evt.Sign(sk)
				err := il.SaveEvent(evt)
				require.NoError(t, err)

				total++
			}

			verifyFreeRangesInvariants(t, mmmm)

			mmmm.lmdbEnv.View(func(txn *lmdb.Txn) error {
				before := mmmm.freeRangesAll
				err := mmmm.gatherFreeRanges(txn)
				require.NoError(t, err)
				require.Equalf(t, mmmm.freeRangesAll, before, "expected %s, got %s", before, mmmm.freeRangesAll)
				return nil
			})

			if chance(20) {
				break
			}
		}
	})
}

func countUsableFreeRanges(t *testing.T, mmmm *MultiMmapManager) (count int, space int) {
	for _, fr := range mmmm.freeRangesAll {
		if fr.size >= LARGE_FREERANGE {
			count++
			space += int(fr.size)
		}
	}

	require.Equal(t, count, len(mmmm.freeRangesLarge))

	return count, space
}

func verifyFreeRangesInvariants(t *testing.T, mmmm *MultiMmapManager) {
	all := mmmm.freeRangesAll
	large := mmmm.freeRangesLarge

	for _, l := range large {
		found := false
		for _, a := range all {
			if l.start == a.start && l.size == a.size {
				found = true
				break
			}
		}
		require.True(t, found, "large range %v not found in all ranges", l)
	}

	for i := 1; i < len(all); i++ {
		require.Greater(t, all[i].start, all[i-1].start, "all ranges should be sorted by start")
	}

	for i := range all {
		for j := i + 1; j < len(all); j++ {
			end1 := all[i].start + uint64(all[i].size)
			end2 := all[j].start + uint64(all[j].size)
			require.False(t, (all[i].start >= all[j].start && all[i].start < end2) ||
				(all[j].start >= all[i].start && all[j].start < end1),
				"ranges %v and %v overlap", all[i], all[j])
		}
	}

	mmmm.lmdbEnv.View(func(txn *lmdb.Txn) error {
		before := make(positions, len(mmmm.freeRangesAll))
		copy(before, mmmm.freeRangesAll)
		err := mmmm.gatherFreeRanges(txn)
		require.NoError(t, err)
		require.Equal(t, before, mmmm.freeRangesAll, "recomputing free ranges should yield the same result")
		return nil
	})
}
