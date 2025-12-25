package boltdb

import (
	"context"
	"fmt"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"fiatjaf.com/nostr/eventstore/codec/betterbinary"
	"go.etcd.io/bbolt"
)

func (b *BoltBackend) SaveEvent(ctx context.Context, evt nostr.Event) error {
	return b.DB.Update(func(txn *bbolt.Tx) error {
		if b.EnableHLLCacheFor != nil {
			// modify hyperloglog caches relative to this
			useCache, skipSaving := b.EnableHLLCacheFor(evt.Kind)

			if useCache {
				err := b.updateHyperLogLogCachedValues(txn, evt)
				if err != nil {
					return fmt.Errorf("failed to update hll cache: %w", err)
				}
				if skipSaving {
					return nil
				}
			}
		}

		rawBucket := txn.Bucket(rawEventStore)

		// check if we already have this id
		bin := rawBucket.Get(evt.ID[16:24])
		if bin != nil {
			// we should get nil, otherwise we already have it so end here
			return eventstore.ErrDupEvent
		}

		return b.save(txn, evt)
	})
}

func (b *BoltBackend) save(txn *bbolt.Tx, evt nostr.Event) error {
	rawBucket := txn.Bucket(rawEventStore)

	// encode to binary form so we'll save it
	bin := make([]byte, betterbinary.Measure(evt))
	if err := betterbinary.Marshal(evt, bin); err != nil {
		return err
	}

	// raw event store
	if err := rawBucket.Put(evt.ID[16:24], bin); err != nil {
		return err
	}

	// put indexes
	for k := range b.getIndexKeysForEvent(evt) {
		err := txn.Bucket(k.bucket).Put(k.fullkey, nil)
		if err != nil {
			return err
		}
	}

	return nil
}
