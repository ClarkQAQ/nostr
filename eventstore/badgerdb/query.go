package badgerdb

import (
	"context"
	"iter"
	"log"
	"math"
	"slices"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/codec/betterbinary"
	"fiatjaf.com/nostr/eventstore/internal"
	"fiatjaf.com/nostr/eventstore/searcher"
	"github.com/dgraph-io/badger/v4"
)

func (b *BadgerBackend) QueryEvents(ctx context.Context, filter nostr.Filter, maxLimit int) iter.Seq[nostr.Event] {
	return func(yield func(nostr.Event) bool) {
		if filter.IDs != nil {
			// when there are ids we ignore everything else and just fetch the ids
			if err := b.DB.View(func(txn *badger.Txn) error {
				return b.queryByIds(txn, filter.IDs, yield)
			}); err != nil {
				log.Printf("badger: unexpected id query error: %s\n", err)
			}
			return
		}

		// max number of events we'll return
		if tlimit := filter.GetTheoreticalLimit(); tlimit == 0 {
			return
		} else if tlimit < maxLimit {
			maxLimit = tlimit
		}

		// do a normal query based on various filters
		if err := b.DB.View(func(txn *badger.Txn) error {
			return b.query(txn, filter, maxLimit, yield)
		}); err != nil {
			log.Printf("badger: unexpected query error: %s\n", err)
		}
	}
}

func (b *BadgerBackend) queryByIds(txn *badger.Txn, ids []nostr.ID, yield func(nostr.Event) bool) error {
	for _, id := range ids {
		rawKey := make([]byte, 1+32)
		rawKey[0] = prefixRaw[0]
		copy(rawKey[1:], id[:])

		item, err := txn.Get(rawKey)
		if err != nil {
			continue
		}

		bin, err := item.ValueCopy(nil)
		if err != nil {
			continue
		}

		event := nostr.Event{}
		if err := betterbinary.Unmarshal(bin, &event); err != nil {
			continue
		}

		if !yield(event) {
			return nil
		}
	}

	return nil
}

func (b *BadgerBackend) query(txn *badger.Txn, filter nostr.Filter, limit int, yield func(nostr.Event) bool) error {
	queries, extraAuthors, extraKinds, extraTagKey, extraTagValues, since, err := b.prepareQueries(filter)
	if err != nil {
		return err
	}

	its := make(iterators, 0, len(queries))
	defer func() {
		// Close all iterators
		for _, it := range its {
			if it.it != nil {
				it.it.Close()
			}
		}
	}()

	for _, q := range queries {
		opts := badger.DefaultIteratorOptions
		opts.Reverse = true
		opts.Prefix = q.prefix
		opts.PrefetchValues = false // we only need keys initially

		badgerIt := txn.NewIterator(opts)
		it := newIterator(q, badgerIt)

		it.seek(q.startingPoint)
		if it.exhausted {
			badgerIt.Close()
			continue
		}

		its = append(its, it)
	}

	if len(its) == 0 {
		return nil
	}

	// when limit is 0 (unlimited), use a reasonable default batch size
	effectiveLimit := limit
	if effectiveLimit == 0 {
		effectiveLimit = 1024 // default batch size for unlimited queries
	}

	batchSizePerQuery := internal.BatchSizePerNumberOfQueries(effectiveLimit, len(queries))
	// ensure batch size is at least 1 to avoid zero-capacity slices
	if batchSizePerQuery < 1 {
		batchSizePerQuery = 1
	}

	// initial pull from all queries
	for i := range its {
		its[i].pull(batchSizePerQuery, since)
	}

	numberOfIteratorsToPullOnEachRound := max(1, int(math.Ceil(float64(len(its))/float64(12))))
	totalEventsEmitted := 0
	tempResults := make([]nostr.Event, 0, batchSizePerQuery*2)

	var searchSearcher *searcher.Searcher
	if filter.Search != "" {
		searchSearcher = searcher.NewSearcher(filter.Search)
	}

	for len(its) > 0 {
		// reset stuff
		tempResults = tempResults[:0]

		// after pulling from all iterators once we now find out what iterators are
		// the ones we should keep pulling from next (i.e. which one's last emitted timestamp is the highest)
		k := min(numberOfIteratorsToPullOnEachRound, len(its))
		its.quickselect(k)
		threshold := its.threshold(k)

		// so we can emit all the events higher than the threshold
		for i := range its {
			for t := 0; t < len(its[i].timestamps); t++ {
				if its[i].timestamps[t] >= threshold {
					id := its[i].ids[t]

					// discard this regardless of what happens
					its[i].timestamps = internal.SwapDelete(its[i].timestamps, t)
					its[i].ids = internal.SwapDelete(its[i].ids, t)
					t--

					// fetch actual event
					rawKey := make([]byte, 1+32)
					rawKey[0] = prefixRaw[0]
					copy(rawKey[1:], id)

					item, err := txn.Get(rawKey)
					if err != nil {
						continue
					}

					bin, err := item.ValueCopy(nil)
					if err != nil {
						log.Printf("badger: failed to get value for %x: %s\n", id, err)
						continue
					}

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
						log.Printf("badger: value read error (id %s) on query prefix %x: %s\n",
							betterbinary.GetID(bin).Hex(), its[i].query.prefix, err)
						continue
					}

					// if there is still a tag to be checked, do it now
					if extraTagValues != nil && !event.Tags.ContainsAny(extraTagKey, extraTagValues) {
						continue
					}

					if searchSearcher != nil && !searchSearcher.Contains(event.Content) {
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
			// only check limit if it was non-zero (unlimited otherwise)
			if limit != 0 && totalEventsEmitted == limit {
				return nil
			}
		}

		// now pull more events
		for i := 0; i < min(len(its), numberOfIteratorsToPullOnEachRound); i++ {
			if its[i].exhausted {
				if len(its[i].ids) == 0 {
					// close the iterator before removing
					if its[i].it != nil {
						its[i].it.Close()
						its[i].it = nil
					}
					// eliminating this from the list of iterators
					its = internal.SwapDelete(its, i)
					i--
				}
				continue
			}

			its[i].pull(batchSizePerQuery, since)
		}
	}

	return nil
}
