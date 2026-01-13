package pebbledb

import (
	"context"
	"encoding/binary"
	"fmt"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/codec/betterbinary"
	"fiatjaf.com/nostr/eventstore/internal"
	"github.com/cockroachdb/pebble/v2"
)

// ReplaceEvent atomically replaces a replaceable or addressable event.
// For replaceable events: replaces any existing event with same (author, kind).
// For addressable events: replaces any existing event with same (author, kind, d-tag).
func (b *PebbleBackend) ReplaceEvent(ctx context.Context, evt nostr.Event) error {
	// Use a snapshot for consistent reads during the replace operation
	snap := b.DB.NewSnapshot()
	defer snap.Close()

	// Find existing events to potentially replace
	var eventsToDelete []nostr.Event
	shouldStore := true

	// Build query prefix for pubkey+kind
	// x<pk:32><kind:2>
	prefix := make([]byte, 1+32+2)
	prefix[0] = prefixPubkeyKind[0]
	copy(prefix[1:1+32], evt.PubKey[:])
	binary.BigEndian.PutUint16(prefix[1+32:1+32+2], uint16(evt.Kind))

	// For addressable events, we also need to check the d-tag
	var dTag string
	if evt.Kind.IsAddressable() {
		dTag = evt.Tags.GetD()
	}

	// Iterate over matching events
	iter, err := snap.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: incrementPrefix(prefix),
	})
	if err != nil {
		return fmt.Errorf("failed to create iterator: %w", err)
	}
	defer iter.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		// Extract ID from index key
		id := extractIDFromIndexKey(iter.Key())

		// Get the actual event
		rawKey := makeRawKey(id)
		bin, closer, err := snap.Get(rawKey)
		if err != nil {
			if err == pebble.ErrNotFound {
				continue
			}
			return fmt.Errorf("failed to get event %s: %w", id, err)
		}

		// Copy before closing
		binCopy := make([]byte, len(bin))
		copy(binCopy, bin)
		closer.Close()

		var existing nostr.Event
		if err := betterbinary.Unmarshal(binCopy, &existing); err != nil {
			continue
		}

		// For addressable events, check d-tag matches
		if evt.Kind.IsAddressable() {
			existingD := existing.Tags.GetD()
			if existingD != dTag {
				continue
			}
		}

		// Compare timestamps to decide what to do
		if internal.IsOlder(existing, evt) {
			// Existing is older, mark for deletion
			eventsToDelete = append(eventsToDelete, existing)
		} else {
			// Existing is newer or same, don't store the new event
			shouldStore = false
		}
	}

	if err := iter.Error(); err != nil {
		return fmt.Errorf("iterator error: %w", err)
	}

	// Now perform the replace operation using a batch
	batch := b.DB.NewBatch()
	defer batch.Close()

	// Delete old events
	for _, old := range eventsToDelete {
		rawKey := makeRawKey(old.ID)
		if err := batch.Delete(rawKey, nil); err != nil {
			return fmt.Errorf("failed to delete old event %s: %w", old.ID, err)
		}
		for idx := range getIndexKeysForEvent(old) {
			if err := batch.Delete(idx.fullKey, nil); err != nil {
				return fmt.Errorf("failed to delete index for old event %s: %w", old.ID, err)
			}
		}
	}

	// Save new event if it should be stored
	if shouldStore {
		// Encode event to binary
		bin := make([]byte, betterbinary.Measure(evt))
		if err := betterbinary.Marshal(evt, bin); err != nil {
			return err
		}

		// Store raw event
		rawKey := makeRawKey(evt.ID)
		if err := batch.Set(rawKey, bin, nil); err != nil {
			return err
		}

		// Store all index keys
		for idx := range getIndexKeysForEvent(evt) {
			if err := batch.Set(idx.fullKey, nil, nil); err != nil {
				return err
			}
		}
	}

	return batch.Commit(b.writeOpts)
}

// incrementPrefix returns a prefix that is lexicographically just past the given prefix.
// Used for UpperBound in iterators.
func incrementPrefix(prefix []byte) []byte {
	result := make([]byte, len(prefix))
	copy(result, prefix)

	// Increment the last byte, handling overflow
	for i := len(result) - 1; i >= 0; i-- {
		if result[i] < 0xFF {
			result[i]++
			return result
		}
		result[i] = 0
	}

	return append(result, 0)
}
