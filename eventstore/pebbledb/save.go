package pebbledb

import (
	"context"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"fiatjaf.com/nostr/eventstore/codec/betterbinary"
	"github.com/cockroachdb/pebble/v2"
)

// SaveEvent saves an event to the store.
// Returns ErrDupEvent if the event already exists.
func (b *PebbleBackend) SaveEvent(ctx context.Context, evt nostr.Event) error {
	// Check if event already exists
	rawKey := makeRawKey(evt.ID)
	_, closer, err := b.DB.Get(rawKey)
	if err == nil {
		closer.Close()
		return eventstore.ErrDupEvent
	}
	if err != pebble.ErrNotFound {
		return err
	}

	return b.save(evt)
}

// save writes an event and all its indexes using a batch for atomicity.
func (b *PebbleBackend) save(evt nostr.Event) error {
	// Encode event to binary
	bin := make([]byte, betterbinary.Measure(evt))
	if err := betterbinary.Marshal(evt, bin); err != nil {
		return err
	}

	// Use batch for atomic write of event + indexes
	batch := b.DB.NewBatch()
	defer batch.Close()

	// Store raw event
	rawKey := makeRawKey(evt.ID)
	if err := batch.Set(rawKey, bin, nil); err != nil {
		return err
	}

	// Store all index keys (values are empty - keys-only indexing)
	for idx := range getIndexKeysForEvent(evt) {
		if err := batch.Set(idx.fullKey, nil, nil); err != nil {
			return err
		}
	}

	// Commit batch
	return batch.Commit(b.writeOpts)
}
