package mmm

import (
	"fmt"
	"iter"
	"maps"
	"math/rand/v2"
	"os"
	"slices"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func FuzzTest(f *testing.F) {
	f.Add(0, uint(84), uint(10), uint(5))
	f.Fuzz(func(t *testing.T, seed int, nlayers, nevents, ndeletes uint) {
		nlayers = nlayers%23 + 1
		nevents = nevents%10000 + 1
		ndeletes = ndeletes % nevents

		// create a temporary directory for the test
		tmpDir, err := os.MkdirTemp("", "mmm-test-*")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		logger := zerolog.Nop()
		rnd := rand.New(rand.NewPCG(uint64(seed), 0))

		// initialize MMM
		mmmm := &MultiMmapManager{
			Dir:    tmpDir,
			Logger: &logger,
		}

		err = mmmm.Init()
		require.NoError(t, err)
		defer mmmm.Close()

		for i := range nlayers {
			name := string([]byte{97 + byte(i)})
			il, err := mmmm.EnsureLayer(name)
			defer il.Close()
			require.NoError(t, err, "layer %s/%d", name, i)
		}

		// create test events
		sk := nostr.MustSecretKeyFromHex("945e01e37662430162121b804d3645a86d97df9d256917d86735d0eb219393eb")
		storedIds := make([]nostr.ID, nevents)
		nTags := make(map[nostr.ID]int)
		storedByLayer := make(map[string][]nostr.ID)

		// create n events with random combinations of tags
		for i := 0; i < int(nevents); i++ {
			tags := nostr.Tags{}
			// randomly add 1-nlayers tags
			numTags := 1 + (i % int(nlayers))
			usedTags := make(map[string]bool)

			for j := 0; j < numTags; j++ {
				tag := string([]byte{97 + byte(i%int(nlayers))})
				if !usedTags[tag] {
					tags = append(tags, nostr.Tag{"t", tag})
					usedTags[tag] = true
				}
			}

			evt := nostr.Event{
				CreatedAt: nostr.Timestamp(i),
				Kind:      nostr.Kind(i), // hack to query by serial id
				Tags:      tags,
				Content:   fmt.Sprintf("test content %d", i),
			}
			evt.Sign(sk)

			for _, layer := range mmmm.layers {
				if evt.Tags.FindWithValue("t", layer.name) != nil {
					err := layer.SaveEvent(evt)
					require.NoError(t, err)
					storedByLayer[layer.name] = append(storedByLayer[layer.name], evt.ID)
				}
			}

			storedIds[i] = evt.ID
			nTags[evt.ID] = len(evt.Tags)
		}

		// perform random rescans -- shouldn't break the database
		if rnd.UintN(3) == 1 {
			mmmm.Rescan()
		}

		// verify each layer has the correct events
		for i, layer := range mmmm.layers {
			count := 0
			for evt := range layer.QueryEvents(nostr.Filter{}, 11000) {
				require.True(t, evt.Tags.ContainsAny("t", []string{layer.name}))
				count++
			}
			require.Equal(t, count, len(storedByLayer[layer.name]), "layer %d ('%s')", i, layer.name)

			// call ComputeStats
			stats, err := layer.ComputeStats(StatsOptions{})
			require.NoError(t, err, "ComputeStats failed for layer %d ('%s')", i, layer.name)
			require.Equal(t, stats.Total, uint(count))
			if count > 0 {
				require.GreaterOrEqual(t, len(stats.PerWeek), 1)
				require.Len(t, stats.PerPubKey, 1)
				for pk := range maps.Keys(stats.PerPubKey) {
					require.Equal(t, pk, sk.Public())
				}
			}
		}

		// randomly select n events to delete from random layers
		deleted := make(map[nostr.ID][]*IndexingLayer)

		for range ndeletes {
			id := storedIds[rnd.Int()%len(storedIds)]
			layer := mmmm.layers[rnd.Int()%len(mmmm.layers)]

			evt, layers := mmmm.GetByID(id)
			if slices.Contains(deleted[id], layer) {
				// already deleted from this layer
				require.NotContains(t, layers, layer)
			} else if evt != nil && evt.Tags.FindWithValue("t", layer.name) != nil {
				require.Contains(t, layers, layer)

				// delete now
				layer.DeleteEvent(evt.ID)
				deleted[id] = append(deleted[id], layer)
			} else {
				// was never saved to this in the first place
				require.NotContains(t, layers, layer)
			}
		}

		// perform random rescans -- shouldn't break the database
		if rnd.UintN(3) == 1 {
			mmmm.Rescan()
		}

		// perform random defrags -- shouldn't break the database
		if rnd.UintN(3) == 1 {
			mmmm.Defragment(len(deleted) / 3)
		}

		for id, deletedlayers := range deleted {
			evt, foundlayers := mmmm.GetByID(id)

			for _, layer := range deletedlayers {
				require.NotContains(t, foundlayers, layer)
			}
			for _, layer := range foundlayers {
				require.NotNil(t, evt.Tags.FindWithValue("t", layer.name))
			}

			if nTags[id] == len(deletedlayers) && evt != nil {
				deletedlayersnames := make([]string, len(deletedlayers))
				for i, layer := range deletedlayers {
					deletedlayersnames[i] = layer.name
				}

				t.Fatalf("id %s has %d tags %v, should have been deleted from %v, but wasn't: %s",
					id, nTags[id], evt.Tags, deletedlayersnames, evt)
			} else if nTags[id] > len(deletedlayers) {
				t.Fatalf("id %s should still be available as it had %d tags and was only deleted from %v, but isn't",
					id, nTags[id], deletedlayers)
			}

			if evt != nil {
				for _, layer := range mmmm.layers {
					// verify event still accessible from other layers
					if slices.Contains(foundlayers, layer) {
						next, stop := iter.Pull(layer.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{evt.Kind}}, 11000)) // hack
						_, fetched := next()
						require.True(t, fetched)
						stop()
					} else {
						// and not accessible from this layer we just deleted
						next, stop := iter.Pull(layer.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{evt.Kind}}, 11000)) // hack
						_, fetched := next()
						require.True(t, fetched)
						stop()
					}
				}
			}
		}

		// perform random rescans -- shouldn't break the database
		if rnd.UintN(3) == 1 {
			mmmm.Rescan()
		}

		// now delete a layer and events that only exist in that layer should vanish
		layer := mmmm.layers[rnd.Int()%len(mmmm.layers)]
		eventsThatShouldVanish := make([]nostr.ID, 0, nevents/2)
		for evt := range layer.QueryEvents(nostr.Filter{}, 11000) {
			if len(evt.Tags) == 1+len(deleted[evt.ID]) {
				eventsThatShouldVanish = append(eventsThatShouldVanish, evt.ID)
			}
		}

		// perform random rescans -- shouldn't break the database
		if rnd.UintN(3) == 1 {
			mmmm.Rescan()
		}

		err = mmmm.DropLayer(layer.name)
		require.NoError(t, err)

		// perform random rescans -- shouldn't break the database
		if rnd.UintN(3) == 1 {
			mmmm.Rescan()
		}

		for _, id := range eventsThatShouldVanish {
			v, ils := mmmm.GetByID(id)
			require.Nil(t, v)
			require.Empty(t, ils)
		}
	})
}
