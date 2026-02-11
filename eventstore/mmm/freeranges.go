package mmm

import (
	"cmp"
	"fmt"
	"slices"

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
