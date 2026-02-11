package mmm

import (
	"bytes"
	"encoding/binary"
	"slices"

	"fiatjaf.com/nostr"
	"github.com/PowerDNS/lmdb-go/lmdb"
)

func (b *MultiMmapManager) Rescan() error {
	b.writeMutex.Lock()
	defer b.writeMutex.Unlock()

	return b.lmdbEnv.Update(func(mmmtxn *lmdb.Txn) error {
		cursor, err := mmmtxn.OpenCursor(b.indexId)
		if err != nil {
			return err
		}
		defer cursor.Close()

		var toPurge [][]byte // a list of idPrefix entries
		for key, val, err := cursor.Get(nil, nil, lmdb.First); err == nil; key, val, err = cursor.Get(key, val, lmdb.Next) {
			pos := positionFromBytes(val[0:12])

			// for every event in this index
			var borked bool

			// we try to load it
			var evt nostr.Event
			if err := b.loadEvent(pos, &evt); err == nil && bytes.Equal(evt.ID[0:8], key) {
				// all good
				borked = false
			} else {
				// it's borked
				borked = true
			}

			var layersToRemove []uint16

			// then for every layer referenced in there we check
			for s := 12; s < len(val); s += 2 {
				layerId := binary.BigEndian.Uint16(val[s : s+2])
				layer := b.layers.ByID(layerId)
				if layer == nil {
					continue
				}

				if err := layer.lmdbEnv.Update(func(txn *lmdb.Txn) error {
					txn.RawRead = true

					if borked {
						// for borked events we have to do a bruteforce check
						if layer.hasAtPosition(txn, pos) {
							// expected -- delete anyway since it's borked
							if err := layer.bruteDeleteIndexes(txn, pos); err != nil {
								return err
							}
						} else {
							// this stuff is doubly borked -- let's do nothing
							return nil
						}
					} else {
						// otherwise we do a more reasonable check
						if layer.hasAtTimestampAndPosition(txn, evt.CreatedAt, pos) {
							// expected, all good
						} else {
							// can't find it in this layer, so update source reference to remove this
							// and clear it from this layer (if any traces remain)
							if err := layer.deleteIndexes(txn, evt, val[0:12]); err != nil {
								return err
							}

							// we'll remove references to this later
							// (no need to do anything in the borked case as everything will be deleted)
							layersToRemove = append(layersToRemove, layerId)
						}
					}

					return nil
				}); err != nil {
					return err
				}
			}

			if borked {
				toPurge = append(toPurge, key)
			} else if len(layersToRemove) > 0 {
				for s := 12; s < len(val); {
					if slices.Contains(layersToRemove, binary.BigEndian.Uint16(val[s:s+2])) {
						// swap-delete
						copy(val[s:s+2], val[len(val)-2:])
						val = val[0 : len(val)-2]
					} else {
						s += 2
					}
				}

				if len(val) > 12 {
					if err := mmmtxn.Put(b.indexId, key, val, 0); err != nil {
						return err
					}
				} else {
					toPurge = append(toPurge, key)
				}
			}
		}

		for _, idPrefix := range toPurge {
			// just delete from the ids index,
			// no need to deal with the freeranges list as it will be recalculated afterwards.
			// this also ensures any brokenly overlapping overwritten events don't have to be sacrificed.
			if err := mmmtxn.Del(b.indexId, idPrefix, nil); err != nil {
				return err
			}
		}

		if err := b.gatherFreeRanges(mmmtxn); err != nil {
			return err
		}

		return nil
	})
}

func (il *IndexingLayer) hasAtTimestampAndPosition(iltxn *lmdb.Txn, ts nostr.Timestamp, pos position) (exists bool) {
	cursor, err := iltxn.OpenCursor(il.indexCreatedAt)
	if err != nil {
		panic(err)
	}
	defer cursor.Close()

	key := make([]byte, 4)
	binary.BigEndian.PutUint32(key[0:4], uint32(ts))

	if _, val, err := cursor.Get(key, nil, lmdb.SetKey); err == nil {
		if positionFromBytes(val[0:12]) == pos {
			exists = true
		}
	}

	return exists
}

func (il *IndexingLayer) hasAtPosition(iltxn *lmdb.Txn, pos position) (exists bool) {
	cursor, err := iltxn.OpenCursor(il.indexCreatedAt)
	if err != nil {
		panic(err)
	}
	defer cursor.Close()

	for key, val, err := cursor.Get(nil, nil, lmdb.First); err == nil; key, val, err = cursor.Get(key, val, lmdb.Next) {
		if positionFromBytes(val[0:12]) == pos {
			exists = true
			break
		}
	}

	return exists
}

func (il *IndexingLayer) bruteDeleteIndexes(iltxn *lmdb.Txn, pos position) error {
	type entry struct {
		key []byte
		val []byte
	}

	toDelete := make([]entry, 0, 8)

	for _, index := range []lmdb.DBI{
		il.indexCreatedAt,
		il.indexKind,
		il.indexPubkey,
		il.indexPubkeyKind,
		il.indexPTagKind,
		il.indexTag,
		il.indexTag32,
		il.indexTagAddr,
	} {
		cursor, err := iltxn.OpenCursor(index)
		if err != nil {
			return err
		}

		for key, val, err := cursor.Get(nil, nil, lmdb.First); err == nil; key, val, err = cursor.Get(key, val, lmdb.Next) {
			if positionFromBytes(val[0:12]) == pos {
				toDelete = append(toDelete, entry{key, val})
			}
		}

		cursor.Close()

		for _, entry := range toDelete {
			if err := iltxn.Del(index, entry.key, entry.val); err != nil {
				return err
			}
		}

		toDelete = toDelete[:0]
	}

	return nil
}
