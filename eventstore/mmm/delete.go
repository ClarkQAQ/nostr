package mmm

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"slices"

	"fiatjaf.com/nostr"
	"github.com/PowerDNS/lmdb-go/lmdb"
)

func (il *IndexingLayer) DeleteEvent(id nostr.ID) error {
	if il.mmmm.ReadOnly {
		return ReadOnly
	}

	il.mmmm.writeMutex.Lock()
	defer il.mmmm.writeMutex.Unlock()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// prepare transactions
	mmmtxn, err := il.mmmm.lmdbEnv.BeginTxn(nil, 0)
	if err != nil {
		return err
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
		return err
	}
	defer func() {
		// defer abort but only if we haven't committed (we'll set it to nil after committing)
		if iltxn != nil {
			iltxn.Abort()
		}
	}()
	iltxn.RawRead = true

	var acquiredFreeRangeFromDelete *position
	if pos, shouldPurge, err := il.delete(mmmtxn, iltxn, id); err != nil {
		return fmt.Errorf("failed to delete event %s: %w", id, err)
	} else if shouldPurge {
		// purge
		if err := mmmtxn.Del(il.mmmm.indexId, id[0:8], nil); err != nil {
			return err
		}
		acquiredFreeRangeFromDelete = &pos
	}

	// commit in this order to minimize problematic inconsistencies
	if err := mmmtxn.Commit(); err != nil {
		return fmt.Errorf("can't commit mmmtxn: %w", err)
	}
	mmmtxn = nil
	if err := iltxn.Commit(); err != nil {
		return fmt.Errorf("can't commit iltxn: %w", err)
	}
	iltxn = nil

	// finally merge in the new free range (in this order it makes more sense, the worst that can
	// happen is that we lose this free range but we'll have it again on the next startup)
	if acquiredFreeRangeFromDelete != nil {
		il.mmmm.mergeNewFreeRange(*acquiredFreeRangeFromDelete)
	}

	return nil
}

func (il *IndexingLayer) delete(
	mmmtxn *lmdb.Txn,
	iltxn *lmdb.Txn,
	id nostr.ID,
) (pos position, shouldPurge bool, err error) {
	// first in the mmmm txn we check if we have the event still
	val, err := mmmtxn.Get(il.mmmm.indexId, id[0:8])
	if err != nil {
		if lmdb.IsNotFound(err) {
			// we already do not have this anywhere
			return position{}, false, nil
		}
		return position{}, false, fmt.Errorf("failed to check if we have the event %x: %w", id, err)
	}

	// we have this, but do we have it in the current layer?
	// val is [posb][il_idx][il_idx...]
	pos = positionFromBytes(val[0:12])

	// check references
	currentLayer := binary.BigEndian.AppendUint16(nil, il.id)
	for i := 12; i < len(val); i += 2 {
		if slices.Equal(val[i:i+2], currentLayer) {
			// we will remove the current layer if it's found
			nextval := make([]byte, len(val)-2)
			copy(nextval, val[0:i])
			copy(nextval[i:], val[i+2:])

			if err := mmmtxn.Put(il.mmmm.indexId, id[0:8], nextval, 0); err != nil {
				return pos, false, fmt.Errorf("failed to update references for %x: %w", id[:], err)
			}

			// if there are no more layers we will delete everything later
			shouldPurge = len(nextval) == 12

			break
		}
	}

	// load the event so we can compute the indexes
	var evt nostr.Event
	if err := il.mmmm.loadEvent(pos, &evt); err != nil {
		return pos, false, fmt.Errorf("failed to load event %x when deleting: %w", id[:], err)
	}

	if err := il.deleteIndexes(iltxn, evt, val[0:12]); err != nil {
		return pos, false, fmt.Errorf("failed to delete indexes for %s=>%v: %w", evt.ID, val[0:12], err)
	}

	return pos, shouldPurge, nil
}

func (il *IndexingLayer) deleteIndexes(iltxn *lmdb.Txn, event nostr.Event, posbytes []byte) error {
	// calculate all index keys we have for this event and delete them
	for k := range il.getIndexKeysForEvent(event) {
		if err := iltxn.Del(k.dbi, k.key, posbytes); err != nil && !lmdb.IsNotFound(err) {
			return fmt.Errorf("index entry %v/%x deletion failed: %w", k.dbi, k.key, err)
		}
	}

	return nil
}
