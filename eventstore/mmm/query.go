package mmm

import (
	"encoding/binary"
	"fmt"
	"iter"
	"log"
	"math"
	"slices"
	"sync"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/codec/betterbinary"
	"fiatjaf.com/nostr/eventstore/internal"
	"github.com/PowerDNS/lmdb-go/lmdb"
)

var tempResultsPool = sync.Pool{
	New: func() any {
		return make([]nostr.Event, 0, 64)
	},
}

// GetByID returns the event -- if found in this mmm -- and all the IndexingLayers it belongs to.
func (b *MultiMmapManager) GetByID(id nostr.ID) (*nostr.Event, IndexingLayers) {
	var event *nostr.Event
	layers := b.queryByIDs([]nostr.ID{id}, func(evt nostr.Event) bool {
		event = &evt
		return false
	}, nil, true)

	if event != nil {
		present := make([]*IndexingLayer, len(layers))
		for i, id := range layers {
			present[i] = b.layers.ByID(id)
		}
		return event, present
	}

	return nil, nil
}

// queryByIDs emits the events of the given id to the given channel if they exist anywhere in this mmm.
func (b *MultiMmapManager) queryByIDs(
	ids []nostr.ID,
	yield func(nostr.Event) bool,
	restrictToLayer *uint16, // pass -1 if not restricted
	withLayers bool,
) (layers []uint16) {
	b.lmdbEnv.View(func(txn *lmdb.Txn) error {
		txn.RawRead = true

		for _, id := range ids {
			val, err := txn.Get(b.indexId, id[0:8])
			if err == nil {
				pos := positionFromBytes(val[0:12])
				evt := nostr.Event{}
				if err := b.loadEvent(pos, &evt); err != nil {
					panic(fmt.Errorf("failed to decode event %s from %v: %w", id, pos, err))
				}

				restrictionSatisfied := restrictToLayer == nil
				if withLayers {
					layers = make([]uint16, 0, (len(val)-12)/2)
				}
				if withLayers || !restrictionSatisfied {
					for s := 12; s < len(val); s += 2 {
						layer := binary.BigEndian.Uint16(val[s : s+2])
						if withLayers {
							layers = append(layers, layer)
						}
						if !restrictionSatisfied && layer == *restrictToLayer {
							restrictionSatisfied = true
						}
					}
				}

				if !restrictionSatisfied {
					continue
				}

				if !yield(evt) {
					return nil
				}
			}
		}

		return nil
	})

	return layers
}

func (il *IndexingLayer) QueryEvents(filter nostr.Filter, maxLimit int) iter.Seq[nostr.Event] {
	return func(yield func(nostr.Event) bool) {
		if filter.IDs != nil {
			il.mmmm.queryByIDs(filter.IDs, yield, &il.id, false)
			return
		}

		if filter.Search != "" {
			return
		}

		// max number of events we'll return
		if tlimit := filter.GetTheoreticalLimit(); tlimit == 0 {
			return
		} else if tlimit < maxLimit {
			maxLimit = tlimit
		}

		il.lmdbEnv.View(func(txn *lmdb.Txn) error {
			txn.RawRead = true

			return il.query(txn, filter, maxLimit, yield)
		})
	}
}

func (il *IndexingLayer) query(txn *lmdb.Txn, filter nostr.Filter, limit int, yield func(nostr.Event) bool) error {
	queries, extraAuthors, extraKinds, extraTagKey, extraTagValues, since, err := il.prepareQueries(filter)
	if err != nil {
		return err
	}

	iterators := make(iterators, len(queries))
	batchSizePerQuery := internal.BatchSizePerNumberOfQueries(limit, len(queries))

	for q, query := range queries {
		cursor, err := txn.OpenCursor(queries[q].dbi)
		if err != nil {
			return err
		}
		iterators[q] = &iterator{
			query:  query,
			cursor: cursor,
		}

		defer cursor.Close()
		iterators[q].seek(queries[q].startingPoint)
	}

	// initial pull from all queries
	for i := range iterators {
		iterators[i].pull(batchSizePerQuery, since)
	}

	numberOfIteratorsToPullOnEachRound := max(1, int(math.Ceil(float64(len(iterators))/float64(12))))
	totalEventsEmitted := 0
	tempResults := tempResultsPool.Get().([]nostr.Event)
	defer tempResultsPool.Put(tempResults[:0])

	for len(iterators) > 0 {
		// reset stuff
		tempResults = tempResults[:0]

		// after pulling from all iterators once we now find out what iterators are
		// the ones we should keep pulling from next (i.e. which one's last emitted timestamp is the highest)
		k := min(numberOfIteratorsToPullOnEachRound, len(iterators))
		iterators.quickselect(k)
		threshold := iterators.threshold(k)

		// so we can emit all the events higher than the threshold
		for i := range iterators {
			for t := 0; t < len(iterators[i].timestamps); t++ {
				if iterators[i].timestamps[t] >= threshold {
					posb := iterators[i].posbs[t]

					// discard this regardless of what happens
					iterators[i].timestamps = internal.SwapDelete(iterators[i].timestamps, t)
					iterators[i].posbs = internal.SwapDelete(iterators[i].posbs, t)
					t--

					// fetch actual event
					pos := positionFromBytes(posb)
					bin := il.mmmm.mmapf[pos.start : pos.start+uint64(pos.size)]

					// check it against pubkeys without decoding the entire thing
					if extraAuthors != nil && !slices.Contains(extraAuthors, betterbinary.GetPubKey(bin)) {
						continue
					}

					// check it against kinds without decoding the entire thing
					if extraKinds != nil && !slices.Contains(extraKinds, betterbinary.GetKind(bin)) {
						continue
					}

					// decode the entire thing
					event := nostr.Event{}
					if err := betterbinary.Unmarshal(bin, &event); err != nil {
						log.Printf("mmm: value read error (id %s) on query prefix %x sp %x dbi %v: %s\n",
							betterbinary.GetID(bin).Hex(), iterators[i].query.prefix, iterators[i].query.startingPoint, iterators[i].query.dbi, err)
						continue
					}

					// if there is still a tag to be checked, do it now
					if extraTagValues != nil && !event.Tags.ContainsAny(extraTagKey, extraTagValues) {
						continue
					}

					tempResults = append(tempResults, event)
				}
			}
		}

		// emit this stuff in order
		slices.SortFunc(tempResults, nostr.CompareEventReverse)
		for _, evt := range tempResults {
			if !yield(evt) {
				return nil
			}

			totalEventsEmitted++
			if totalEventsEmitted == limit {
				return nil
			}
		}

		// now pull more events
		for i := 0; i < min(len(iterators), numberOfIteratorsToPullOnEachRound); i++ {
			if iterators[i].exhausted {
				if len(iterators[i].posbs) == 0 {
					// eliminating this from the list of iterators
					iterators = internal.SwapDelete(iterators, i)
					i--
				}
				continue
			}

			iterators[i].pull(batchSizePerQuery, since)
		}
	}

	return nil
}
