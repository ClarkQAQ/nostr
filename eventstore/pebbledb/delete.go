package pebbledb

import (
	"context"
	"fmt"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/codec/betterbinary"
	"github.com/cockroachdb/pebble/v2"
)

// DeleteEvent deletes an event and all its indexes atomically.
func (b *PebbleBackend) DeleteEvent(ctx context.Context, id nostr.ID) error {
	// Get the raw event first to compute all index keys
	rawKey := makeRawKey(id)
	bin, closer, err := b.DB.Get(rawKey)
	if err != nil {
		if err == pebble.ErrNotFound {
			// Event doesn't exist, nothing to delete
			return nil
		}
		return fmt.Errorf("failed to get event %s for deletion: %w", id, err)
	}

	// Copy the binary data before closing
	binCopy := make([]byte, len(bin))
	copy(binCopy, bin)
	closer.Close()

	// Unmarshal to get all fields needed for index keys
	var evt nostr.Event
	if err := betterbinary.Unmarshal(binCopy, &evt); err != nil {
		return fmt.Errorf("failed to unmarshal event %s for deletion: %w", id, err)
	}

	// Use batch for atomic deletion
	batch := b.DB.NewBatch()
	defer batch.Close()

	// Delete all index keys
	for idx := range getIndexKeysForEvent(evt) {
		if err := batch.Delete(idx.fullKey, nil); err != nil {
			return fmt.Errorf("failed to delete index for event %s: %w", id, err)
		}
	}

	// Delete raw event
	if err := batch.Delete(rawKey, nil); err != nil {
		return fmt.Errorf("failed to delete raw event %s: %w", id, err)
	}

	return batch.Commit(b.writeOpts)
}
