package mmm

import (
	"fmt"
	"iter"
	"runtime"

	"fiatjaf.com/nostr"
	"github.com/PowerDNS/lmdb-go/lmdb"
)

func (il *IndexingLayer) ReplaceEvent(evt nostr.Event) (deleted []nostr.Event, err error) {
	if il.mmmm.ReadOnly {
		return nil, ReadOnly
	}

	il.mmmm.writeMutex.Lock()
	defer il.mmmm.writeMutex.Unlock()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	filter := nostr.Filter{Kinds: []nostr.Kind{evt.Kind}, Authors: []nostr.PubKey{evt.PubKey}}
	if evt.Kind.IsAddressable() {
		// when addressable, add the "d" tag to the filter
		filter.Tags = nostr.TagMap{"d": []string{evt.Tags.GetD()}}
	}

	// prepare transactions
	mmmtxn, err := il.mmmm.lmdbEnv.BeginTxn(nil, 0)
	if err != nil {
		return nil, err
	}
	defer func() {
		// defer abort but only if we haven't committed (we'll set it to nil after committing)
		if mmmtxn != nil {
			mmmtxn.Abort()
		}
	}()
	mmmtxn.RawRead = true

	iltxn, err := il.lmdbEnv.BeginTxn(nil, 0)
	if err != nil {
		return nil, err
	}
	defer func() {
		// defer abort but only if we haven't committed (we'll set it to nil after committing)
		if iltxn != nil {
			iltxn.Abort()
		}
	}()
	iltxn.RawRead = true

	// check if we already have this id
	_, existsErr := mmmtxn.Get(il.mmmm.indexId, evt.ID[0:8])
	if existsErr == nil {
		return nil, nil
	}
	if !lmdb.IsNotFound(existsErr) {
		return nil, fmt.Errorf("error checking existence: %w", existsErr)
	}

	// now we fetch the past events, whatever they are, delete them and then save the new
	var qerr error
	var results iter.Seq[nostr.Event] = func(yield func(nostr.Event) bool) {
		qerr = il.query(iltxn, filter, 10 /* in theory limit could be just 1 and this should work */, yield)
	}
	if qerr != nil {
		return nil, fmt.Errorf("failed to query past events with %s: %w", filter, qerr)
	}

	var acquiredFreeRangeFromDelete *position
	shouldStore := true
	for previous := range results {
		if nostr.IsOlder(previous, evt) {
			if pos, shouldPurge, derr := il.delete(mmmtxn, iltxn, previous.ID); derr != nil {
				return nil, fmt.Errorf("failed to delete event %s for replacing: %w", previous.ID, derr)
			} else if shouldPurge {
				// purge
				if err := mmmtxn.Del(il.mmmm.indexId, previous.ID[0:8], nil); err != nil {
					return nil, err
				}
				acquiredFreeRangeFromDelete = &pos
			}
			deleted = append(deleted, previous)
		} else {
			// there is a newer event already stored, so we won't store this
			shouldStore = false
		}
	}

	if shouldStore {
		_, err := il.mmmm.storeOn(mmmtxn, iltxn, il, evt)
		if err != nil {
			return nil, err
		}
	}

	// commit in this order to minimize problematic inconsistencies
	if err := mmmtxn.Commit(); err != nil {
		return nil, fmt.Errorf("can't commit mmmtxn: %w", err)
	}
	mmmtxn = nil
	if err := iltxn.Commit(); err != nil {
		return nil, fmt.Errorf("can't commit iltxn: %w", err)
	}
	iltxn = nil

	// finally merge in the new free range (in this order it makes more sense, the worst that can
	// happen is that we lose this free range but we'll have it again on the next startup)
	if acquiredFreeRangeFromDelete != nil {
		il.mmmm.mergeNewFreeRange(*acquiredFreeRangeFromDelete)
	}

	return deleted, nil
}
