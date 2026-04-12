package boltdb

import (
	"context"
	"fmt"
	"iter"

	"fiatjaf.com/nostr"
	"go.etcd.io/bbolt"
)

func (b *BoltBackend) ReplaceEvent(ctx context.Context, evt nostr.Event) (deleted []nostr.Event, err error) {
	e := b.DB.Update(func(txn *bbolt.Tx) error {
		rawBucket := txn.Bucket(rawEventStore)

		// check if we already have this id
		bin := rawBucket.Get(evt.ID[16:24])
		if bin != nil {
			return nil
		}

		filter := nostr.Filter{Kinds: []nostr.Kind{evt.Kind}, Authors: []nostr.PubKey{evt.PubKey}}
		if evt.Kind.IsAddressable() {
			// when addressable, add the "d" tag to the filter
			filter.Tags = nostr.TagMap{"d": []string{evt.Tags.GetD()}}
		}

		// now we fetch the past events, whatever they are, delete them and then save the new
		var qerr error
		var results iter.Seq[nostr.Event] = func(yield func(nostr.Event) bool) {
			qerr = b.query(txn, filter, 10 /* in theory limit could be just 1 and this should work */, yield)
		}
		if qerr != nil {
			return fmt.Errorf("failed to query past events with %s: %w", filter, qerr)
		}

		shouldStore := true
		for previous := range results {
			if nostr.IsOlder(previous, evt) {
				if err := b.delete(txn, previous.ID); err != nil {
					return fmt.Errorf("failed to delete event %s for replacing: %w", previous.ID, err)
				}
				deleted = append(deleted, previous)
			} else {
				// there is a newer event already stored, so we won't store this
				shouldStore = false
			}
		}
		if shouldStore {
			return b.save(txn, evt)
		}

		return nil
	})

	return deleted, e
}
