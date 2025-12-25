package boltdb

import (
	"context"
	"fmt"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/codec/betterbinary"
	"go.etcd.io/bbolt"
)

func (b *BoltBackend) DeleteEvent(ctx context.Context, id nostr.ID) error {
	return b.DB.Update(func(txn *bbolt.Tx) error {
		return b.delete(txn, id)
	})
}

func (b *BoltBackend) delete(txn *bbolt.Tx, id nostr.ID) error {
	rawBucket := txn.Bucket(rawEventStore)

	// check if we have this actually
	bin := rawBucket.Get(id[16:24])
	if bin == nil {
		// we already do not have this
		return nil
	}

	var evt nostr.Event
	if err := betterbinary.Unmarshal(bin, &evt); err != nil {
		return fmt.Errorf("failed to unmarshal raw event %x to delete: %w", id, err)
	}

	// calculate all index keys we have for this event and delete them
	for k := range b.getIndexKeysForEvent(evt) {
		err := txn.Bucket(k.bucket).Delete(k.fullkey)
		if err != nil {
			return fmt.Errorf("failed to delete index entry %s for %x: %w", b.keyName(k), evt.ID[0:8], err)
		}
	}

	// delete the raw event
	if err := rawBucket.Delete(id[16:24]); err != nil {
		return fmt.Errorf("failed to delete raw event %x: %w", evt.ID[16:24], err)
	}

	return nil
}
