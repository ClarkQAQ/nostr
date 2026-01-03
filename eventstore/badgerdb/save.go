package badgerdb

import (
	"context"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"fiatjaf.com/nostr/eventstore/codec/betterbinary"
	"github.com/dgraph-io/badger/v4"
)

func (b *BadgerBackend) SaveEvent(ctx context.Context, evt nostr.Event) error {
	return b.DB.Update(func(txn *badger.Txn) error {
		// check if we already have this id
		rawKey := make([]byte, 1+32)
		rawKey[0] = prefixRaw[0]
		copy(rawKey[1:], evt.ID[:])

		_, err := txn.Get(rawKey)
		if err == nil {
			// we already have it
			return eventstore.ErrDupEvent
		}
		if err != badger.ErrKeyNotFound {
			return err
		}

		return b.save(txn, evt)
	})
}

func (b *BadgerBackend) save(txn *badger.Txn, evt nostr.Event) error {
	// encode to binary form so we'll save it
	bin := make([]byte, betterbinary.Measure(evt))
	if err := betterbinary.Marshal(evt, bin); err != nil {
		return err
	}

	// raw event store
	rawKey := make([]byte, 1+32)
	rawKey[0] = prefixRaw[0]
	copy(rawKey[1:], evt.ID[:])

	if err := txn.Set(rawKey, bin); err != nil {
		return err
	}

	// put indexes (values are empty - keys-only indexing)
	for k := range b.getIndexKeysForEvent(evt) {
		if err := txn.Set(k.fullkey, nil); err != nil {
			return err
		}
	}

	return nil
}
