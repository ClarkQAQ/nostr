package badgerdb

import (
	"context"
	"errors"
	"fmt"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/codec/betterbinary"
	"github.com/dgraph-io/badger/v4"
)

func (b *BadgerBackend) DeleteEvent(ctx context.Context, id nostr.ID) error {
	return b.DB.Update(func(txn *badger.Txn) error {
		return b.delete(txn, id)
	})
}

func (b *BadgerBackend) delete(txn *badger.Txn, id nostr.ID) error {
	// check if we have this actually
	rawKey := make([]byte, 1+32)
	rawKey[0] = prefixRaw[0]
	copy(rawKey[1:], id[:])

	item, e := txn.Get(rawKey)
	if e != nil {
		if errors.Is(e, badger.ErrKeyNotFound) {
			return nil
		}

		return fmt.Errorf("failed to get raw event %s to delete: %w", id, e)
	}

	bin, e := item.ValueCopy(nil)
	if e != nil {
		return fmt.Errorf("failed to get raw event %s to delete: %w", id, e)
	}

	var evt nostr.Event
	if e := betterbinary.Unmarshal(bin, &evt); e != nil {
		return fmt.Errorf("failed to unmarshal raw event %s to delete: %w", id, e)
	}

	// calculate all index keys we have for this event and delete them
	for k := range b.getIndexKeysForEvent(evt) {
		if e := txn.Delete(k.fullkey); e != nil {
			return fmt.Errorf("failed to delete index entry for %s: %w", evt.ID, e)
		}
	}

	// delete the raw event
	if e := txn.Delete(rawKey); e != nil {
		return fmt.Errorf("failed to delete raw event %s: %w", evt.ID, e)
	}

	return nil
}
