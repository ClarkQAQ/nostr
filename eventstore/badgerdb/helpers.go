package badgerdb

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"iter"
	"slices"
	"strconv"
	"strings"

	"fiatjaf.com/nostr"
	"github.com/dgraph-io/badger/v4"
	"github.com/templexxx/xhex"
)

type iterator struct {
	query query

	// iteration stuff
	it     *badger.Iterator
	key    []byte
	currId []byte

	// this keeps track of last timestamp value pulled from this
	last uint64

	// if we shouldn't fetch more from this
	exhausted bool

	// results not yet emitted
	ids        [][]byte
	timestamps []uint64
}

func newIterator(query query, it *badger.Iterator) *iterator {
	return &iterator{
		query:  query,
		it:     it,
		key:    make([]byte, 0, 64),
		currId: make([]byte, 32),
	}
}

func (it *iterator) pull(n int, since uint64) {
	query := it.query

	for range n {
		if !it.it.Valid() {
			it.exhausted = true
			return
		}

		item := it.it.Item()
		key := item.Key()

		// check if we're still within prefix
		if !bytes.HasPrefix(key, query.prefix) {
			it.exhausted = true
			return
		}

		// extract createdAt (8 bytes before the last 32 bytes id)
		keyLen := len(key)
		if keyLen < 8+32 {
			it.it.Next()
			continue
		}

		createdAt := binary.BigEndian.Uint64(key[keyLen-8-32 : keyLen-32])
		if createdAt < since {
			it.exhausted = true
			return
		}

		// got a key
		id := make([]byte, 32)
		copy(id, key[keyLen-32:])
		it.ids = append(it.ids, id)
		it.timestamps = append(it.timestamps, createdAt)
		it.last = createdAt

		// advance the iterator for the next call
		it.it.Next()
	}
}

func (it *iterator) seek(startingPoint []byte) {
	it.it.Seek(startingPoint)
	if !it.it.Valid() {
		it.exhausted = true
		return
	}

	// In reverse iteration, Seek goes to >= key, we need to position at the right spot
	item := it.it.Item()
	key := item.Key()

	// If the key we landed on is greater than our starting point, we're in the right spot
	// (in reverse iteration, we want keys <= startingPoint)
	if bytes.Compare(key, startingPoint) > 0 {
		// Skip to prev which should be <= startingPoint
		it.it.Next() // in reverse mode, Next() goes backwards
		if !it.it.Valid() {
			it.exhausted = true
			return
		}
	}

	// Update current position
	item = it.it.Item()
	key = item.Key()
	keyLen := len(key)
	if keyLen >= 32 {
		it.key = make([]byte, keyLen-32)
		copy(it.key, key[:keyLen-32])
		copy(it.currId, key[keyLen-32:])
	}
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
func (its iterators) threshold(k int) uint64 {
	highest := its[0].last
	for i := 1; i < k; i++ {
		if its[i].last > highest {
			highest = its[i].last
		}
	}
	return highest
}

type key struct {
	prefix  []byte
	fullkey []byte
}

func (b *BadgerBackend) getIndexKeysForEvent(evt nostr.Event) iter.Seq[key] {
	return func(yield func(key) bool) {
		id := evt.ID[:]
		ts := make([]byte, 8)
		binary.BigEndian.PutUint64(ts, uint64(evt.CreatedAt))

		{
			// ~ by pubkey+date
			// p:<pk32>:<ts8>:<id32>
			k := make([]byte, 1+32+8+32)
			k[0] = prefixPubkey[0]
			copy(k[1:1+32], evt.PubKey[:])
			copy(k[1+32:1+32+8], ts)
			copy(k[1+32+8:1+32+8+32], id)
			if !yield(key{prefix: prefixPubkey, fullkey: k}) {
				return
			}
		}

		{
			// ~ by kind+date
			// k:<kind8>:<ts8>:<id32>
			k := make([]byte, 1+8+8+32)
			k[0] = prefixKind[0]
			binary.BigEndian.PutUint64(k[1:1+8], uint64(evt.Kind))
			copy(k[1+8:1+8+8], ts)
			copy(k[1+8+8:1+8+8+32], id)
			if !yield(key{prefix: prefixKind, fullkey: k}) {
				return
			}
		}

		{
			// ~ by pubkey+kind+date
			// x:<pk32>:<kind8>:<ts8>:<id32>
			k := make([]byte, 1+32+8+8+32)
			k[0] = prefixPubkeyKind[0]
			copy(k[1:1+32], evt.PubKey[:])
			binary.BigEndian.PutUint64(k[1+32:1+32+8], uint64(evt.Kind))
			copy(k[1+32+8:1+32+8+8], ts)
			copy(k[1+32+8+8:1+32+8+8+32], id)
			if !yield(key{prefix: prefixPubkeyKind, fullkey: k}) {
				return
			}
		}

		// ~ by tagvalue+date
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
			prefix, k := b.getTagIndexPrefix(tag[0], tag[1])
			// keys always end with 8 bytes of created_at + 32 bytes of the id

			binary.BigEndian.PutUint64(k[len(k)-8-32:], uint64(evt.CreatedAt))
			copy(k[len(k)-32:], id)
			if !yield(key{prefix: prefix, fullkey: k}) {
				return
			}
		}

		{
			// ~ by date only
			// c:<ts8>:<id32>
			k := make([]byte, 1+8+32)
			k[0] = prefixCreatedAt[0]
			copy(k[1:1+8], ts)
			copy(k[1+8:1+8+32], id)
			if !yield(key{prefix: prefixCreatedAt, fullkey: k}) {
				return
			}
		}
	}
}

func (b *BadgerBackend) getTagIndexPrefix(tagName string, tagValue string) (prefix []byte, k []byte) {
	letterPrefix := byte(int(tagName[0]) % 256)

	// if it's 32 bytes as hex, save it as bytes
	if len(tagValue) == 64 {
		// t:<letter1>:<tagVal32>:<ts8>:<id32>
		k = make([]byte, 1+1+32+8+32)
		if err := xhex.Decode(k[2:2+32], []byte(tagValue[0:64])); err == nil {
			k[0] = prefixTag32[0]
			k[1] = letterPrefix
			return prefixTag32, k
		}
	}

	// if it looks like an "a" tag, index it in this special format, with letter (tag name) prefix
	// a:<letter1>:<kind8>:<pk32>:<d30>:<ts8>:<id32>
	spl := strings.Split(tagValue, ":")
	if len(spl) == 3 && len(spl[1]) == 64 {
		k = make([]byte, 1+1+8+32+30+8+32)
		if err := xhex.Decode(k[1+1+8:1+1+8+32], []byte(spl[1][0:64])); err == nil {
			if kind, err := strconv.ParseUint(spl[0], 10, 64); err == nil {
				k[0] = prefixTagAddr[0]
				k[1] = letterPrefix
				binary.BigEndian.PutUint64(k[1+1:1+1+8], uint64(kind))
				// limit "d" identifier to 30 bytes (so we don't have to grow our byte slice)
				copy(k[1+1+8+32:1+1+8+32+30], spl[2])
				return prefixTagAddr, k
			}
		}
	}

	// index whatever else as a md5 hash of the contents, with letter (tag name) prefix
	// m:<letter1>:<md516>:<ts8>:<id32>
	h := md5.New()
	h.Write([]byte(tagValue))
	k = make([]byte, 1+1+16+8+32)
	k[0] = prefixTag[0]
	k[1] = letterPrefix
	copy(k[2:2+16], h.Sum(nil))

	return prefixTag, k
}
