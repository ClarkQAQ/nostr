package mmm

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"slices"
	"strings"
)

type positions []position

func (poss positions) find(start uint64) (idx int) {
	idx, _ = slices.BinarySearchFunc(poss, start, func(item position, target uint64) int {
		return cmp.Compare(item.start, target)
	})
	return idx
}

func (poss positions) del(start uint64) positions {
	idx := poss.find(start)
	return slices.Delete(poss, idx, idx+1)
}

func (poss positions) String() string {
	str := strings.Builder{}
	str.Grow(10 + 20*len(poss))
	str.WriteString("positions:[")
	for _, pos := range poss {
		str.WriteString(pos.String())
	}
	str.WriteString("]")
	return str.String()
}

type position struct {
	start uint64
	size  uint32
}

func (pos position) String() string {
	return fmt.Sprintf("<%d|%d|%d>", pos.start, pos.size, pos.start+uint64(pos.size))
}

func (pos position) isLarge() bool {
	return pos.size >= LARGE_FREERANGE
}

func positionFromBytes(posb []byte) position {
	return position{
		size:  binary.BigEndian.Uint32(posb[0:4]),
		start: binary.BigEndian.Uint64(posb[4:12]),
	}
}

func writeBytesFromPosition(out []byte, pos position) {
	binary.BigEndian.PutUint32(out[0:4], pos.size)
	binary.BigEndian.PutUint64(out[4:12], pos.start)
}
