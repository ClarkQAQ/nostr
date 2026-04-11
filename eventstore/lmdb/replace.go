package lmdb

import (
	"fmt"

	"fiatjaf.com/nostr"
	"github.com/PowerDNS/lmdb-go/lmdb"
)

func (b *LMDBBackend) ReplaceEvent(evt nostr.Event) (deleted []nostr.Event, err error) {
	err = b.lmdbEnv.Update(func(txn *lmdb.Txn) error {
		// check if we already have this id
		_, existsErr := txn.Get(b.indexId, evt.ID[0:8])
		if existsErr == nil {
			return nil
		}
		if operr, ok := existsErr.(*lmdb.OpError); !ok || operr.Errno != lmdb.NotFound {
			return existsErr
		}

		filter := nostr.Filter{Kinds: []nostr.Kind{evt.Kind}, Authors: []nostr.PubKey{evt.PubKey}}
		if evt.Kind.IsAddressable() {
			// when addressable, add the "d" tag to the filter
			filter.Tags = nostr.TagMap{"d": []string{evt.Tags.GetD()}}
		}

		// now we fetch the past events, whatever they are, delete them and then save the new
		shouldStore := true
		if qerr := b.query(txn, filter, 10 /* could be just 1 */, func(previous nostr.Event) bool {
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

	return deleted, err
}
