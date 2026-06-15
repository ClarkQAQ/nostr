package mmm

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"runtime"
	"slices"
	"syscall"
	"unsafe"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/codec/betterbinary"
	"github.com/PowerDNS/lmdb-go/lmdb"
)

const LARGE_FREERANGE = 142

func (b *MultiMmapManager) gatherFreeRanges(txn *lmdb.Txn) error {
	cursor, err := txn.OpenCursor(b.indexId)
	if err != nil {
		return fmt.Errorf("failed to open cursor on indexId: %w", err)
	}
	defer cursor.Close()

	usedPositions := make(positions, 0, 256)
	for key, val, err := cursor.Get(nil, nil, lmdb.First); err == nil; key, val, err = cursor.Get(key, val, lmdb.Next) {
		pos := positionFromBytes(val[0:12])
		usedPositions = append(usedPositions, pos)
	}

	// sort used positions by start
	slices.SortFunc(usedPositions, func(a, b position) int { return cmp.Compare(a.start, b.start) })

	// if there is free space at the end this will simulate it
	usedPositions = append(usedPositions, position{start: b.mmapfEnd, size: 0})

	// calculate free ranges as gaps between used positions
	b.freeRangesAll = make(positions, 0, len(usedPositions))
	b.freeRangesLarge = make([]position, 0, len(usedPositions)/10)
	var currentStart uint64 = 0
	for _, used := range usedPositions {
		if used.start > currentStart {
			// gap from currentStart to pos.start
			freeSize := used.start - currentStart
			if freeSize > 0 {
				fr := position{
					start: currentStart,
					size:  uint32(freeSize),
				}
				b.freeRangesAll = append(b.freeRangesAll, fr)
				if fr.isLarge() {
					b.freeRangesLarge = append(b.freeRangesLarge, fr)
				}
			}
		}
		currentStart = used.start + uint64(used.size)
	}

	return nil
}

func (b *MultiMmapManager) mergeNewFreeRange(newFreeRange position) {
	// use binary search to find the insertion point for the new pos
	idx, exists := slices.BinarySearchFunc(b.freeRangesAll, newFreeRange.start, func(item position, target uint64) int {
		return cmp.Compare(item.start, target)
	})
	if exists {
		panic(fmt.Errorf("can't add free range that already exists: %s", newFreeRange))
	}

	deleteStart := -1
	deleting := 0

	// check the range immediately before
	if idx > 0 {
		before := b.freeRangesAll[idx-1]
		if before.start+uint64(before.size) == newFreeRange.start {
			deleteStart = idx - 1
			deleting++
			newFreeRange.start = before.start
			newFreeRange.size = before.size + newFreeRange.size
		}
	}

	// check the range immediately after
	if idx < len(b.freeRangesAll) {
		after := b.freeRangesAll[idx]
		if newFreeRange.start+uint64(newFreeRange.size) == after.start {
			if deleteStart == -1 {
				deleteStart = idx
			}
			deleting++

			newFreeRange.size = newFreeRange.size + after.size
		}
	}

	switch deleting {
	case 0:
		// if we are not deleting anything we must insert the new free range
		b.freeRangesAll = slices.Insert(b.freeRangesAll, idx, newFreeRange)

		// if it's large add it to the list of large free ranges
		if newFreeRange.isLarge() {
			b.freeRangesLarge = append(b.freeRangesLarge, newFreeRange)
		}
	case 1:
		deleted := b.freeRangesAll[deleteStart]

		// if we're deleting a single range, don't delete it, modify it in-place instead.
		b.freeRangesAll[deleteStart] = newFreeRange

		// if the list we're modifying is in the list of large ranges modify it there too
		if deleted.isLarge() {
			for i, large := range b.freeRangesLarge {
				if large.start == deleted.start {
					b.freeRangesLarge[i] = newFreeRange
					break
				}
			}
		} else if newFreeRange.isLarge() {
			// otherwise: if after modification it's big enough we should add it to list of large ranges
			b.freeRangesLarge = append(b.freeRangesLarge, newFreeRange)
		}
	case 2:
		// now if we're deleting two ranges, delete the second instead and modify the first in place
		first := b.freeRangesAll[deleteStart]
		second := b.freeRangesAll[deleteStart+1]

		b.freeRangesAll = slices.Delete(b.freeRangesAll, deleteStart+1, deleteStart+1+1)
		b.freeRangesAll[deleteStart] = newFreeRange

		// if the second was in the list of large lists delete it from there too
		if second.isLarge() {
			for i, large := range b.freeRangesLarge {
				if large.start == second.start {
					b.freeRangesLarge[i] = b.freeRangesLarge[len(b.freeRangesLarge)-1]
					b.freeRangesLarge = b.freeRangesLarge[0 : len(b.freeRangesLarge)-1]
					break
				}
			}
		}

		// if the list we're modifying (the first) is already in the list of large ranges modify it there too
		if first.isLarge() {
			for i, large := range b.freeRangesLarge {
				if large.start == first.start {
					b.freeRangesLarge[i] = newFreeRange
					break
				}
			}
		} else if newFreeRange.isLarge() {
			// otherwise if after modification has become big enough we should add it to list of large ranges
			b.freeRangesLarge = append(b.freeRangesLarge, newFreeRange)
		}
	}
}

func (b *MultiMmapManager) Defragment(n int) error {
	if b.ReadOnly {
		return ReadOnly
	}

	b.writeMutex.Lock()
	defer b.writeMutex.Unlock()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if n > len(b.freeRangesAll)-1 {
		n = len(b.freeRangesAll) - 1
	}
	if n <= 0 {
		return nil
	}

	mmmtxn, err := b.lmdbEnv.BeginTxn(nil, 0)
	if err != nil {
		return fmt.Errorf("failed to begin mmm transaction: %w", err)
	}
	defer mmmtxn.Abort()

	type layerTxn struct {
		il  *IndexingLayer
		txn *lmdb.Txn
	}
	layerTxns := make(map[uint16]*layerTxn)
	defer func() {
		for _, lt := range layerTxns {
			lt.txn.Abort()
		}
	}()

	// iterate only through `n` free ranges, as specified
	for i := 0; i < n; i++ {
		fr := b.freeRangesAll[i]

		// where the free range ends, the events start (any number of them)
		eventsStart := fr.start + uint64(fr.size)
		eventsEnd := b.freeRangesAll[i+1].start // and they end when the next free range starts

		c := uint64(0) // this tracks our relative position inside the events section
		for (eventsStart + c) < eventsEnd {
			var evt nostr.Event
			if err := betterbinary.Unmarshal(b.mmapf[(eventsStart+c):eventsEnd], &evt); err != nil {
				id := betterbinary.GetID(b.mmapf[(eventsStart + c):eventsEnd])
				return fmt.Errorf("failed to read event (%x) from mmap: %w", id[:], err)
			}

			// now that we have an event we'll update its pos on the id index and on every layer:
			oldVal, err := mmmtxn.Get(b.indexId, evt.ID[0:8])
			if err != nil {
				return fmt.Errorf("failed to read val (%x) from index: %w", evt.ID[:], err)
			}

			// current position
			pos := positionFromBytes(oldVal[0:12])

			// new position (from the beginning of the free range before + relative position)
			pos.start = fr.start + uint64(c)

			// update this cursor
			c += uint64(pos.size)

			// prepare and save id index
			newVal := make([]byte, len(oldVal))
			writeBytesFromPosition(newVal, pos)
			copy(newVal[12:], oldVal[12:])
			if err := mmmtxn.Put(b.indexId, evt.ID[0:8], newVal, 0); err != nil {
				return fmt.Errorf("failed to write new pos to id index: %w", err)
			}

			for s := 12; s < len(oldVal); s += 2 {
				layer := binary.BigEndian.Uint16(oldVal[s : s+2])
				lt, ok := layerTxns[layer]
				if !ok {
					il := b.layers.ByID(layer)
					if il == nil {
						fmt.Println(b.layers)
						panic(fmt.Errorf("missing layer %d", layer))
					}
					txn, err := il.lmdbEnv.BeginTxn(nil, 0)
					if err != nil {
						return fmt.Errorf("failed to begin layer txn for layer %d: %w", il.id, err)
					}
					txn.RawRead = true
					lt = &layerTxn{il: il, txn: txn}
					layerTxns[il.id] = lt
				}

				for k := range lt.il.getIndexKeysForEvent(evt) {
					if err := lt.txn.Del(k.dbi, k.key, oldVal[0:12]); err != nil {
						return fmt.Errorf("failed to delete old index entry for %x: %w", evt.ID[:], err)
					}
					if err := lt.txn.Put(k.dbi, k.key, newVal[0:12], 0); err != nil {
						return fmt.Errorf("failed to insert new index entry for %x: %w", evt.ID[:], err)
					}
				}
			}
		}

		// now that we have updated all the pointers, just copy all the bytes between the two free ranges
		copy(b.mmapf[fr.start:], b.mmapf[fr.start+uint64(fr.size):eventsEnd])

		// delete this free range if it's one of the big ones
		if fr.isLarge() {
			for l, lfr := range b.freeRangesLarge {
				if lfr.start == fr.start {
					b.freeRangesLarge[l] = b.freeRangesLarge[len(b.freeRangesLarge)-1]
					b.freeRangesLarge = b.freeRangesLarge[0 : len(b.freeRangesLarge)-1]
					break
				}
			}
		}

		// now we have some space left at the end of this events section that is a free range
		remainingSpaceStart := fr.start + c

		// it must be merged with the next free range
		updated := position{
			start: remainingSpaceStart,
			size:  b.freeRangesAll[i+1].size + uint32(eventsEnd) - uint32(remainingSpaceStart),
		}
		nextWasLarge := b.freeRangesAll[i+1].isLarge()
		b.freeRangesAll[i+1] = updated

		if nextWasLarge {
			for l, lfr := range b.freeRangesLarge {
				if lfr.start == eventsEnd {
					b.freeRangesLarge[l] = updated
					break
				}
			}
		} else if updated.isLarge() {
			// if it wasn't large but now is, add it to the list of large free ranges
			b.freeRangesLarge = append(b.freeRangesLarge, updated)
		}
	}

	// msync
	_, _, errno := syscall.Syscall(syscall.SYS_MSYNC,
		uintptr(unsafe.Pointer(&b.mmapf[0])), uintptr(len(b.mmapf)), syscall.MS_SYNC)
	if errno != 0 {
		return fmt.Errorf("msync failed: %w", syscall.Errno(errno))
	}

	// commit transactions
	if err := mmmtxn.Commit(); err != nil {
		return fmt.Errorf("failed to commit mmm transaction: %w", err)
	}
	for lid, lt := range layerTxns {
		if err := lt.txn.Commit(); err != nil {
			return fmt.Errorf("failed to commit layer %d transaction: %w", lid, err)
		}
	}

	// delete the free ranges in bulk
	b.freeRangesAll = slices.Delete(b.freeRangesAll, 0, n)

	return nil
}
