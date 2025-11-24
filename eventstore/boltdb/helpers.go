package boltdb

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"iter"
	"slices"
	"strconv"
	"strings"

	"fiatjaf.com/nostr"
	"github.com/templexxx/xhex"
	"go.etcd.io/bbolt"
)

type iterator struct {
	query query

	// iteration stuff
	cursor    *bbolt.Cursor
	key       []byte
	currIdPtr []byte

	// this keeps track of last timestamp value pulled from this
	last uint32

	// if we shouldn't fetch more from this
	exhausted bool

	// results not yet emitted
	idPtrs     [][]byte
	timestamps []uint32
}

func newIterator(query query, cursor *bbolt.Cursor) *iterator {
	return &iterator{
		query:  query,
		cursor: cursor,

		key:       make([]byte, 0, 31),
		currIdPtr: make([]byte, 8),
	}
}

func (it *iterator) pull(n int, since uint32) {
	query := it.query

	for range n {
		// in the beginning we already have a k and a v from the cursor setup, so check and use these
		if !bytes.HasPrefix(it.key, query.prefix) {
			// we reached the end of this prefix
			it.exhausted = true
			return
		}

		createdAt := binary.BigEndian.Uint32(it.key[len(it.key)-4:])
		if createdAt < since {
			it.exhausted = true
			return
		}

		// got a key
		it.idPtrs = append(it.idPtrs, append([]byte{}, it.currIdPtr...))
		it.timestamps = append(it.timestamps, createdAt)
		it.last = createdAt

		// advance the cursor for the next call
		it.next()
		if it.exhausted {
			return
		}
	}

	return
}

func (it *iterator) seek(key []byte) {
	fullkey, _ := it.cursor.Seek(key)
	if fullkey == nil || bytes.Compare(fullkey, key) == 1 {
		// we're at the end or passed our desired point, so go back one
		fullkey, _ = it.cursor.Prev()
		if fullkey == nil {
			it.exhausted = true
			return
		}
	}

	s := len(fullkey)
	if cap(it.key) >= s-8 {
		it.key = it.key[0 : s-8]
	} else {
		it.key = make([]byte, s-8)
	}
	copy(it.key, fullkey[0:s-8])
	copy(it.currIdPtr, fullkey[s-8:])
}

// goes backwards
func (it *iterator) next() {
	// move one back (we'll look into key in the next iteration)
	fullkey, _ := it.cursor.Prev()
	if fullkey == nil {
		it.exhausted = true
		return
	}

	s := len(fullkey)
	it.key = it.key[0 : s-8]
	copy(it.key, fullkey[0:s-8])
	copy(it.currIdPtr, fullkey[s-8:])
}

type iterators []*iterator

// quickselect reorders the slice just enough to make the top k elements be arranged at the end
// i.e. [1, 700, 25, 312, 44, 28] with k=3 becomes something like [700, 312, 44, 1, 25, 28]
// in this case it's hardcoded to use the 'last' field of the iterator
// copied from https://github.com/chrislee87/go-quickselect
func (its iterators) quickselect(k int) {
	if len(its) == 0 || k >= len(its) {
		return
	}

	left, right := 0, len(its)-1
	for {
		// insertion sort for small ranges
		if right-left <= 20 {
			for i := left + 1; i <= right; i++ {
				for j := i; j > 0 && its[j].last > its[j-1].last; j-- {
					its[j], its[j-1] = its[j-1], its[j]
				}
			}
			return
		}

		// median-of-three to choose pivot
		pivotIndex := left + (right-left)/2
		if its[right].last > its[left].last {
			its[right], its[left] = its[left], its[right]
		}
		if its[pivotIndex].last > its[left].last {
			its[pivotIndex], its[left] = its[left], its[pivotIndex]
		}
		if its[right].last > its[pivotIndex].last {
			its[right], its[pivotIndex] = its[pivotIndex], its[right]
		}

		// partition
		its[left], its[pivotIndex] = its[pivotIndex], its[left]
		ll := left + 1
		rr := right
		for ll <= rr {
			for ll <= right && its[ll].last > its[left].last {
				ll++
			}
			for rr >= left && its[left].last > its[rr].last {
				rr--
			}
			if ll <= rr {
				its[ll], its[rr] = its[rr], its[ll]
				ll++
				rr--
			}
		}
		its[left], its[rr] = its[rr], its[left] // swap into right place
		pivotIndex = rr

		if k == pivotIndex {
			return
		}

		if k < pivotIndex {
			right = pivotIndex - 1
		} else {
			left = pivotIndex + 1
		}
	}
}

// return the highest 'last' value among the first k items in its
func (its iterators) threshold(k int) uint32 {
	highest := its[0].last
	for i := 1; i < k; i++ {
		if its[i].last > highest {
			highest = its[i].last
		}
	}
	return highest
}

type key struct {
	bucket  []byte
	fullkey []byte
}

func (b *BoltBackend) keyName(key key) string {
	s := len(key.fullkey)
	return fmt.Sprintf("<dbi=%s prefix=%x ts=%x idptr=%x>",
		string(key.bucket), key.fullkey[0:s-8-4], key.fullkey[s-8-4:s-8], key.fullkey[s-8:])
}

func (b *BoltBackend) getIndexKeysForEvent(evt nostr.Event) iter.Seq[key] {
	return func(yield func(key) bool) {
		{
			// ~ by pubkey+date
			k := make([]byte, 8+4+8)
			copy(k[0:8], evt.PubKey[0:8])
			binary.BigEndian.PutUint32(k[8:8+4], uint32(evt.CreatedAt))
			copy(k[8+4:8+4+8], evt.ID[16:24])
			if !yield(key{bucket: indexPubkey, fullkey: k}) {
				return
			}
		}

		{
			// ~ by kind+date
			k := make([]byte, 2+4+8)
			binary.BigEndian.PutUint16(k[0:2], uint16(evt.Kind))
			binary.BigEndian.PutUint32(k[2:2+4], uint32(evt.CreatedAt))
			copy(k[2+4:2+4+8], evt.ID[16:24])
			if !yield(key{bucket: indexKind, fullkey: k}) {
				return
			}
		}

		{
			// ~ by pubkey+kind+date
			k := make([]byte, 8+2+4+8)
			copy(k[0:8], evt.PubKey[0:8])
			binary.BigEndian.PutUint16(k[8:8+2], uint16(evt.Kind))
			binary.BigEndian.PutUint32(k[8+2:8+2+4], uint32(evt.CreatedAt))
			copy(k[8+2+4:8+2+4+8], evt.ID[16:24])
			if !yield(key{bucket: indexPubkeyKind, fullkey: k}) {
				return
			}
		}

		// ~ by tagvalue+date
		// ~ by p-tag+kind+date
		for i, tag := range evt.Tags {
			if len(tag) < 2 || len(tag[0]) != 1 || len(tag[1]) == 0 || len(tag[1]) > 100 {
				// not indexable
				continue
			}
			firstIndex := slices.IndexFunc(evt.Tags, func(t nostr.Tag) bool {
				return len(t) >= 2 && t[0] == tag[0] && t[1] == tag[1]
			})
			if firstIndex != i {
				// duplicate
				continue
			}

			// get key prefix (with full length) and offset where to write the created_at
			bucket, k := b.getTagIndexPrefix(tag[0], tag[1])
			// keys always end with 4 bytes of created_at + 8 bytes of the id ptr

			binary.BigEndian.PutUint32(k[len(k)-8-4:], uint32(evt.CreatedAt))
			copy(k[len(k)-8:], evt.ID[16:24])
			if !yield(key{bucket: bucket, fullkey: k}) {
				return
			}
		}

		{
			// ~ by date only
			k := make([]byte, 4+8)
			binary.BigEndian.PutUint32(k[0:4], uint32(evt.CreatedAt))
			copy(k[4:4+8], evt.ID[16:24])
			if !yield(key{bucket: indexCreatedAt, fullkey: k}) {
				return
			}
		}
	}
}

func (b *BoltBackend) getTagIndexPrefix(tagName string, tagValue string) (bucket_ []byte, k_ []byte) {
	var k []byte // the key with full length for created_at and idptr at the end, but not filled with these

	letterPrefix := byte(int(tagName[0]) % 256)

	// if it's 32 bytes as hex, save it as bytes
	if len(tagValue) == 64 {
		// but we actually only use the first 8 bytes, with letter (tag name) prefix
		k = make([]byte, 1+8+4+8)
		if err := xhex.Decode(k[1:1+8], []byte(tagValue[0:8*2])); err == nil {
			k[0] = letterPrefix
			return indexTag32, k
		}
	}

	// if it looks like an "a" tag, index it in this special format, with letter (tag name) prefix
	spl := strings.Split(tagValue, ":")
	if len(spl) == 3 && len(spl[1]) == 64 {
		k = make([]byte, 1+2+8+30+4+8)
		if err := xhex.Decode(k[1+2:1+2+8], []byte(spl[1][0:8*2])); err == nil {
			if kind, err := strconv.ParseUint(spl[0], 10, 16); err == nil {
				k[0] = letterPrefix
				k[1] = byte(kind >> 8)
				k[2] = byte(kind)
				// limit "d" identifier to 30 bytes (so we don't have to grow our byte slice)
				copy(k[1+2+8:1+2+8+30], spl[2])
				return indexTagAddr, k
			}
		}
	}

	// index whatever else as a md5 hash of the contents, with letter (tag name) prefix
	h := md5.New()
	h.Write([]byte(tagValue))
	k = make([]byte, 1, 1+16+4+8)
	k[0] = letterPrefix
	k = h.Sum(k)

	return indexTag, k
}
