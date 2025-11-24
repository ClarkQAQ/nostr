package boltdb

import (
	"bytes"
	"encoding/binary"
	"slices"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/codec/betterbinary"
	"fiatjaf.com/nostr/nip45"
	"fiatjaf.com/nostr/nip45/hyperloglog"
	"github.com/templexxx/xhex"
	"go.etcd.io/bbolt"
)

func (b *BoltBackend) CountEvents(filter nostr.Filter) (uint32, error) {
	var count uint32 = 0

	queries, extraAuthors, extraKinds, extraTagKey, extraTagValues, since, err := b.prepareQueries(filter)
	if err != nil {
		return 0, err
	}

	err = b.DB.View(func(txn *bbolt.Tx) error {
		rawBucket := txn.Bucket(rawEventStore)

		// actually iterate
		for _, q := range queries {
			cursor := txn.Bucket(q.bucket).Cursor()

			it := newIterator(q, cursor)
			it.seek(q.startingPoint)

			for {
				// we already have a k and a v and an err from the cursor setup, so check and use these
				if !bytes.HasPrefix(it.key, q.prefix) {
					// either iteration has errored or we reached the end of this prefix
					break // stop this cursor and move to the next one
				}

				createdAt := binary.BigEndian.Uint32(it.key[len(it.key)-4:])
				if createdAt < since {
					break
				}

				if extraAuthors == nil && extraKinds == nil && extraTagValues == nil {
					count++
				} else {
					// fetch actual event
					bin := rawBucket.Get(it.currIdPtr)
					if bin == nil {
						it.next()
						continue
					}

					// check it against pubkeys without decoding the entire thing
					if !slices.Contains(extraAuthors, betterbinary.GetPubKey(bin)) {
						it.next()
						continue
					}

					// check it against kinds without decoding the entire thing
					if !slices.Contains(extraKinds, betterbinary.GetKind(bin)) {
						it.next()
						continue
					}

					evt := &nostr.Event{}
					if err := betterbinary.Unmarshal(bin, evt); err != nil {
						it.next()
						continue
					}

					// if there is still a tag to be checked, do it now
					if !evt.Tags.ContainsAny(extraTagKey, extraTagValues) {
						it.next()
						continue
					}

					count++
				}
			}
		}

		return nil
	})

	return count, err
}

// CountEventsHLL is like CountEvents, but it will build a hyperloglog value while iterating through results,
// following NIP-45
func (b *BoltBackend) CountEventsHLL(filter nostr.Filter, offset int) (uint32, *hyperloglog.HyperLogLog, error) {
	if useCache, _ := b.EnableHLLCacheFor(filter.Kinds[0]); useCache {
		return b.countEventsHLLCached(filter)
	}

	var count uint32 = 0

	// this is different than CountEvents because some of these extra checks are not applicable in HLL-valid filters
	queries, _, extraKinds, extraTagKey, extraTagValues, since, err := b.prepareQueries(filter)
	if err != nil {
		return 0, nil, err
	}

	hll := hyperloglog.New(offset)

	err = b.DB.View(func(txn *bbolt.Tx) error {
		rawBucket := txn.Bucket(rawEventStore)

		// actually iterate
		for _, q := range queries {
			cursor := txn.Bucket(q.bucket).Cursor()

			it := newIterator(q, cursor)
			it.seek(q.startingPoint)

			for {
				// we already have a k and a v and an err from the cursor setup, so check and use these
				if !bytes.HasPrefix(it.key, q.prefix) {
					// either iteration has errored or we reached the end of this prefix
					break // stop this cursor and move to the next one
				}

				createdAt := binary.BigEndian.Uint32(it.key[len(it.key)-4:])
				if createdAt < since {
					break
				}

				// fetch actual event (we need it regardless because we need the pubkey for the hll)
				bin := rawBucket.Get(it.currIdPtr)
				if bin == nil {
					continue
				}

				if extraKinds == nil && extraTagValues == nil {
					// nothing extra to check
					count++
					hll.AddBytes(betterbinary.GetPubKey(bin))
				} else {
					// check it against kinds without decoding the entire thing
					if !slices.Contains(extraKinds, betterbinary.GetKind(bin)) {
						it.next()
						continue
					}

					evt := &nostr.Event{}
					if err := betterbinary.Unmarshal(bin, evt); err != nil {
						it.next()
						continue
					}

					// if there is still a tag to be checked, do it now
					if !evt.Tags.ContainsAny(extraTagKey, extraTagValues) {
						it.next()
						continue
					}

					count++
					hll.Add(evt.PubKey)
				}
			}
		}

		return nil
	})

	return count, hll, err
}

// countEventsHLLCached will just return a cached value from disk (and presumably we don't even have the events required to compute this anymore).
func (b *BoltBackend) countEventsHLLCached(filter nostr.Filter) (uint32, *hyperloglog.HyperLogLog, error) {
	cacheKey := make([]byte, 2+8)
	binary.BigEndian.PutUint16(cacheKey[0:2], uint16(filter.Kinds[0]))
	switch filter.Kinds[0] {
	case 3:
		xhex.Decode(cacheKey[2:2+8], []byte(filter.Tags["p"][0][0:8*2]))
	case 7:
		xhex.Decode(cacheKey[2:2+8], []byte(filter.Tags["e"][0][0:8*2]))
	case 1111:
		xhex.Decode(cacheKey[2:2+8], []byte(filter.Tags["E"][0][0:8*2]))
	}

	var count uint32
	var hll *hyperloglog.HyperLogLog

	err := b.DB.View(func(txn *bbolt.Tx) error {
		val := txn.Bucket(hllCache).Get(cacheKey)
		if val == nil {
			return nil
		}
		hll = hyperloglog.NewWithRegisters(val, 0) // offset doesn't matter here
		count = uint32(hll.Count())
		return nil
	})

	return count, hll, err
}

func (b *BoltBackend) updateHyperLogLogCachedValues(txn *bbolt.Tx, evt nostr.Event) error {
	cacheKey := make([]byte, 2+8)
	binary.BigEndian.PutUint16(cacheKey[0:2], uint16(evt.Kind))
	hllBucket := txn.Bucket(hllCache)

	for ref, offset := range nip45.HyperLogLogEventPubkeyOffsetsAndReferencesForEvent(evt) {
		// setup cache key (reusing buffer)
		xhex.Decode(cacheKey[2:2+8], []byte(ref[0:8*2]))

		// fetch hll value from cache db
		hll := hyperloglog.New(offset)
		val := hllBucket.Get(cacheKey)
		if val != nil {
			hll.SetRegisters(val)
		}

		// add this event
		hll.Add(evt.PubKey)

		// save values back again
		if err := hllBucket.Put(cacheKey, hll.GetRegisters()); err != nil {
			return err
		}
	}

	return nil
}
