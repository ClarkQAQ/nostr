package badgerdb

import (
	"context"
	"fmt"

	"fiatjaf.com/nostr"
	"github.com/dgraph-io/badger/v4"
)

func (b *BadgerBackend) ReplaceEvent(ctx context.Context, evt nostr.Event) (deleted []nostr.Event, err error) {
	e := b.DB.Update(func(txn *badger.Txn) error {
		filter := nostr.Filter{Kinds: []nostr.Kind{evt.Kind}, Authors: []nostr.PubKey{evt.PubKey}}
		if evt.Kind.IsAddressable() {
			// when addressable, add the "d" tag to the filter
			filter.Tags = nostr.TagMap{"d": []string{evt.Tags.GetD()}}
		}

		// now we fetch the past events, whatever they are, delete them and then save the new
		shouldStore := true
		if qerr := b.query(txn, filter, 10 /* could be just 1 */, func(previous nostr.Event, _ error) bool {
			if nostr.IsOlder(previous, evt) {
				if qerr := b.delete(txn, previous.ID); qerr != nil {
					qerr = fmt.Errorf("failed to delete event %s for replacing: %w", previous.ID, qerr)
					return false
				}
				deleted = append(deleted, previous)
			} else {
				// there is a newer event already stored, so we won't store this
				shouldStore = false
			}
			return true
		}); qerr != nil {
			return fmt.Errorf("failed to query past events with %s: %w", filter, qerr)
		}
		if shouldStore {
			return b.save(txn, evt)
		}

		return nil
	})

	return deleted, e
}
