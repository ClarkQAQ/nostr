package mmm

import (
	"cmp"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
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

func TestDefragment(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mmm-defrag-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	mmmm := &MultiMmapManager{Dir: tmpDir}
	err = mmmm.Init()
	require.NoError(t, err)
	defer mmmm.Close()

	il, err := mmmm.EnsureLayer("a")
	require.NoError(t, err)
	defer il.Close()

	sk := nostr.MustSecretKeyFromHex("945e01e37662430162121b804d3645a86d97df9d256917d86735d0eb219393eb")

	const nevents = 30
	var stored [nevents]nostr.Event
	for i := range nevents {
		evt := nostr.Event{
			CreatedAt: nostr.Timestamp(i),
			Kind:      nostr.KindTextNote,
			Tags:      nostr.Tags{},
			Content:   fmt.Sprintf("============= event %d ============= "+strings.Repeat("+", 23), i),
		}
		evt.Sign(sk)
		err := il.SaveEvent(evt)
		require.NoError(t, err)
		stored[i] = evt
	}

	toDelete := []int{0, 5, 10, 15, 20}
	var remaining []nostr.Event
	for i, evt := range stored {
		if slices.Contains(toDelete, i) {
			err := il.DeleteEvent(evt.ID)
			require.NoError(t, err)
		} else {
			remaining = append(remaining, evt)
		}
	}

	require.Len(t, toDelete, len(mmmm.freeRangesAll))

	err = mmmm.Defragment(2)
	require.NoError(t, err)

	require.Len(t, mmmm.freeRangesAll, 3)
	require.Len(t, remaining, nevents-len(toDelete))

	// all remaining events still accessible with correct content via GetByID
	for _, evt := range remaining {
		gotEvt, layers := mmmm.GetByID(evt.ID)
		require.NotNil(t, gotEvt, "event %s should exist after defrag", evt.ID)
		require.NotEmpty(t, layers, "event %s should have layers after defrag", evt.ID)
		require.Equal(t, evt.Content, gotEvt.Content, "event %s content should match after defrag", evt.ID)

		// also accessible via a query
		require.Equal(t, il, layers[0])
	}

	evts := slices.Collect(il.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{nostr.KindTextNote}}, 100))
	require.Len(t, evts, nevents-len(toDelete))

	// free range invariants hold after defrag
	verifyFreeRangesInvariants(t, mmmm)

	// no overlapping positions after defrag
	mmmm.lmdbEnv.View(func(txn *lmdb.Txn) error {
		cursor, err := txn.OpenCursor(mmmm.indexId)
		require.NoError(t, err)
		defer cursor.Close()

		var allPositions []position
		for _, val, err := cursor.Get(nil, nil, lmdb.First); err == nil; _, val, err = cursor.Get(nil, val, lmdb.Next) {
			pos := positionFromBytes(val[0:12])
			allPositions = append(allPositions, pos)
		}

		slices.SortFunc(allPositions, func(a, b position) int {
			return cmp.Compare(a.start, b.start)
		})

		var lastEnd uint64
		for _, pos := range allPositions {
			if pos.start < lastEnd {
				t.Fatalf("event overlap after defrag: %d < %d", pos.start, lastEnd)
			}
			lastEnd = pos.start + uint64(pos.size)
		}
		return nil
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

func FuzzDefragment(f *testing.F) {
	f.Add(0)
	f.Fuzz(func(t *testing.T, seed int) {
		tmpDir, err := os.MkdirTemp("", "mmm-defrag-fuzz-*")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		logger := zerolog.Nop()
		rnd := rand.New(rand.NewPCG(uint64(seed), 0))

		mmmm := &MultiMmapManager{
			Dir:    tmpDir,
			Logger: &logger,
		}
		err = mmmm.Init()
		require.NoError(t, err)
		defer mmmm.Close()

		layerNames := []string{"a", "b", "c"}
		var layers []*IndexingLayer
		for _, name := range layerNames {
			il, err := mmmm.EnsureLayer(name)
			require.NoError(t, err)
			defer il.Close()
			layers = append(layers, il)
		}

		type indexedEvent struct {
			evt nostr.Event
			tag string
		}
		layerEvents := make([][]indexedEvent, len(layers))

		sk := nostr.MustSecretKeyFromHex("945e01e37662430162121b804d3645a86d97df9d256917d86735d0eb219393eb")
		pk := sk.Public()

		totalEvents := rnd.IntN(500)
		tChoices := []string{"foo", "bar", "banana"}
		var written int

		for written < totalEvents {
			n := rnd.IntN(50) + 1
			if n > totalEvents-written {
				n = totalEvents - written
			}

			for i := 0; i < n; i++ {
				sizeParam := rnd.IntN(2000)
				content := strings.Repeat("z", sizeParam)

				chosenTag := tChoices[rnd.IntN(3)]
				evt := nostr.Event{
					CreatedAt: nostr.Timestamp(rnd.Uint32()),
					Kind:      nostr.KindTextNote,
					Content:   content,
					Tags:      nostr.Tags{{"t", chosenTag}},
				}
				evt.Sign(sk)

				nLayers := rnd.IntN(len(layers)) + 1
				perm := rnd.Perm(len(layers))
				for pi := 0; pi < nLayers; pi++ {
					li := perm[pi]
					err := layers[li].SaveEvent(evt)
					require.NoError(t, err)
					layerEvents[li] = append(layerEvents[li], indexedEvent{evt, chosenTag})
				}
				written++
			}

			if n > 0 {
				totalRemaining := 0
				for _, levts := range layerEvents {
					totalRemaining += len(levts)
				}
				if totalRemaining > 0 {
					m := rnd.IntN(n)
					if m > totalRemaining {
						m = totalRemaining
					}
					for i := 0; i < m; i++ {
						var nonEmpty []int
						for li, levts := range layerEvents {
							if len(levts) > 0 {
								nonEmpty = append(nonEmpty, li)
							}
						}
						if len(nonEmpty) == 0 {
							break
						}
						li := nonEmpty[rnd.IntN(len(nonEmpty))]
						idx := rnd.IntN(len(layerEvents[li]))
						evtInfo := layerEvents[li][idx]
						err := layers[li].DeleteEvent(evtInfo.evt.ID)
						require.NoError(t, err)
						layerEvents[li] = append(layerEvents[li][:idx], layerEvents[li][idx+1:]...)
					}
				}
			}

			if n > 0 {
				o := rnd.IntN(n)
				for i := 0; i < o; i++ {
					if len(mmmm.freeRangesAll) > 1 {
						param := rnd.IntN(len(mmmm.freeRangesAll))
						err := mmmm.Defragment(param)
						require.NoError(t, err)
					}
				}
			}
		}

		// query each layer
		for li, il := range layers {
			levts := layerEvents[li]

			// query by author
			evts := slices.Collect(il.QueryEvents(nostr.Filter{Authors: []nostr.PubKey{pk}}, 10000))
			require.Equal(t, len(levts), len(evts))

			// query by author and kind
			evts = slices.Collect(il.QueryEvents(nostr.Filter{Authors: []nostr.PubKey{pk}, Kinds: []nostr.Kind{nostr.KindTextNote}}, 10000))
			require.Equal(t, len(levts), len(evts))

			// query by "t" tag
			for _, tagVal := range tChoices {
				expected := 0
				for _, ie := range levts {
					if ie.tag == tagVal {
						expected++
					}
				}
				evts = slices.Collect(il.QueryEvents(nostr.Filter{Tags: nostr.TagMap{"t": []string{tagVal}}}, 10000))
				require.Equal(t, expected, len(evts))
			}

			// query with no parameters
			allEvts := slices.Collect(il.QueryEvents(nostr.Filter{}, 10000))
			require.Equal(t, len(levts), len(allEvts))
		}

		// build union of all events across all layers
		allEventSet := make(map[string]nostr.Event)
		for _, levts := range layerEvents {
			for _, ie := range levts {
				allEventSet[ie.evt.ID.String()] = ie.evt
			}
		}

		// all events still accessible via GetByID
		for _, evt := range allEventSet {
			gotEvt, eventLayers := mmmm.GetByID(evt.ID)
			require.NotNil(t, gotEvt)
			require.NotEmpty(t, eventLayers)
			require.Equal(t, evt.Content, gotEvt.Content)
		}

		verifyFreeRangesInvariants(t, mmmm)

		mmmm.lmdbEnv.View(func(txn *lmdb.Txn) error {
			cursor, err := txn.OpenCursor(mmmm.indexId)
			require.NoError(t, err)
			defer cursor.Close()

			var allPositions []position
			for _, val, err := cursor.Get(nil, nil, lmdb.First); err == nil; _, val, err = cursor.Get(nil, val, lmdb.Next) {
				pos := positionFromBytes(val[0:12])
				allPositions = append(allPositions, pos)
			}

			slices.SortFunc(allPositions, func(a, b position) int {
				return cmp.Compare(a.start, b.start)
			})

			var lastEnd uint64
			for _, pos := range allPositions {
				if pos.start < lastEnd {
					t.Fatalf("event overlap after defrag: %d < %d", pos.start, lastEnd)
				}
				lastEnd = pos.start + uint64(pos.size)
			}
			return nil
		})
	})
}
