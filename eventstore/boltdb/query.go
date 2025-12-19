package boltdb

import (
	"iter"
	"log"
	"math"
	"slices"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/codec/betterbinary"
	"fiatjaf.com/nostr/eventstore/internal"
	"go.etcd.io/bbolt"
)

func (b *BoltBackend) QueryEvents(filter nostr.Filter, maxLimit int) iter.Seq[nostr.Event] {
	return func(yield func(nostr.Event) bool) {
		if filter.IDs != nil {
			// when there are ids we ignore everything else and just fetch the ids
			if err := b.DB.View(func(txn *bbolt.Tx) error {
				return b.queryByIds(txn, filter.IDs, yield)
			}); err != nil {
				log.Printf("bolt: unexpected id query error: %s\n", err)
			}
			return
		}

		// ignore search queries
		if filter.Search != "" {
			return
		}

		// max number of events we'll return
		if tlimit := filter.GetTheoreticalLimit(); tlimit == 0 {
			return
		} else if tlimit < maxLimit {
			maxLimit = tlimit
		}

		// do a normal query based on various filters
		if err := b.DB.View(func(txn *bbolt.Tx) error {
			return b.query(txn, filter, maxLimit, yield)
		}); err != nil {
			log.Printf("bolt: unexpected query error: %s\n", err)
		}
	}
}

func (b *BoltBackend) queryByIds(txn *bbolt.Tx, ids []nostr.ID, yield func(nostr.Event) bool) error {
	rawBucket := txn.Bucket(rawEventStore)

	for _, id := range ids {
		bin := rawBucket.Get(id[16:24])
		if bin == nil {
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

func (b *BoltBackend) query(txn *bbolt.Tx, filter nostr.Filter, limit int, yield func(nostr.Event) bool) error {
	queries, extraAuthors, extraKinds, extraTagKey, extraTagValues, since, err := b.prepareQueries(filter)
	if err != nil {
		return err
	}

	iterators := make(iterators, 0, len(queries))
	for _, query := range queries {
		bucket := txn.Bucket(query.bucket)

		it := newIterator(query, bucket.Cursor())

		it.seek(query.startingPoint)
		if it.exhausted {
			// this may happen rarely
			continue
		}

		iterators = append(iterators, it)
	}

	batchSizePerQuery := internal.BatchSizePerNumberOfQueries(limit, len(queries))

	// initial pull from all queries
	for i := range iterators {
		iterators[i].pull(batchSizePerQuery, since)
	}

	numberOfIteratorsToPullOnEachRound := max(1, int(math.Ceil(float64(len(iterators))/float64(12))))
	totalEventsEmitted := 0
	tempResults := make([]nostr.Event, 0, batchSizePerQuery*2)

	rawBucket := txn.Bucket(rawEventStore)

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
					idPtr := iterators[i].idPtrs[t]

					// discard this regardless of what happens
					iterators[i].timestamps = internal.SwapDelete(iterators[i].timestamps, t)
					iterators[i].idPtrs = internal.SwapDelete(iterators[i].idPtrs, t)
					t--

					// fetch actual event
					bin := rawBucket.Get(idPtr)
					if bin == nil {
						log.Printf("bolt: failed to get %x from raw event store (query prefix=%x, index=%s, bucket=%s)\n", idPtr, err, iterators[i].query.prefix, string(iterators[i].query.bucket))
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
						log.Printf("bolt: value read error (id %s) on query prefix %x sp %x dbi %s: %s\n",
							betterbinary.GetID(bin).Hex(), iterators[i].query.prefix, iterators[i].query.startingPoint, string(iterators[i].query.bucket), err)
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
				if len(iterators[i].idPtrs) == 0 {
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
