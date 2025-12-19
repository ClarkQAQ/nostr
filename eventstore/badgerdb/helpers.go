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
	it        *badger.Iterator
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

func newIterator(query query, it *badger.Iterator) *iterator {
	return &iterator{
		query:     query,
		it:        it,
		key:       make([]byte, 0, 64),
		currIdPtr: make([]byte, 8),
	}
}

func (it *iterator) pull(n int, since uint32) {
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

		// extract createdAt (4 bytes before the last 8 bytes idPtr)
		keyLen := len(key)
		if keyLen < 12 {
			it.it.Next()
			continue
		}

		createdAt := binary.BigEndian.Uint32(key[keyLen-12 : keyLen-8])
		if createdAt < since {
			it.exhausted = true
			return
		}

		// got a key
		idPtr := make([]byte, 8)
		copy(idPtr, key[keyLen-8:])
		it.idPtrs = append(it.idPtrs, idPtr)
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
	if keyLen >= 8 {
		it.key = make([]byte, keyLen-8)
		copy(it.key, key[:keyLen-8])
		copy(it.currIdPtr, key[keyLen-8:])
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
	prefix  []byte
	fullkey []byte
}

func (b *BadgerBackend) getIndexKeysForEvent(evt nostr.Event) iter.Seq[key] {
	return func(yield func(key) bool) {
		idPtr := evt.ID[16:24]
		ts := make([]byte, 4)
		binary.BigEndian.PutUint32(ts, uint32(evt.CreatedAt))

		{
			// ~ by pubkey+date
			// p:<pk8>:<ts4>:<idPtr8>
			k := make([]byte, 1+8+4+8)
			k[0] = prefixPubkey[0]
			copy(k[1:1+8], evt.PubKey[0:8])
			copy(k[1+8:1+8+4], ts)
			copy(k[1+8+4:1+8+4+8], idPtr)
			if !yield(key{prefix: prefixPubkey, fullkey: k}) {
				return
			}
		}

		{
			// ~ by kind+date
			// k:<kind2>:<ts4>:<idPtr8>
			k := make([]byte, 1+2+4+8)
			k[0] = prefixKind[0]
			binary.BigEndian.PutUint16(k[1:1+2], uint16(evt.Kind))
			copy(k[1+2:1+2+4], ts)
			copy(k[1+2+4:1+2+4+8], idPtr)
			if !yield(key{prefix: prefixKind, fullkey: k}) {
				return
			}
		}

		{
			// ~ by pubkey+kind+date
			// x:<pk8>:<kind2>:<ts4>:<idPtr8>
			k := make([]byte, 1+8+2+4+8)
			k[0] = prefixPubkeyKind[0]
			copy(k[1:1+8], evt.PubKey[0:8])
			binary.BigEndian.PutUint16(k[1+8:1+8+2], uint16(evt.Kind))
			copy(k[1+8+2:1+8+2+4], ts)
			copy(k[1+8+2+4:1+8+2+4+8], idPtr)
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
			// keys always end with 4 bytes of created_at + 8 bytes of the id ptr

			binary.BigEndian.PutUint32(k[len(k)-8-4:], uint32(evt.CreatedAt))
			copy(k[len(k)-8:], idPtr)
			if !yield(key{prefix: prefix, fullkey: k}) {
				return
			}
		}

		{
			// ~ by date only
			// c:<ts4>:<idPtr8>
			k := make([]byte, 1+4+8)
			k[0] = prefixCreatedAt[0]
			copy(k[1:1+4], ts)
			copy(k[1+4:1+4+8], idPtr)
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
		// but we actually only use the first 8 bytes, with letter (tag name) prefix
		// t:<letter1>:<tagVal8>:<ts4>:<idPtr8>
		k = make([]byte, 1+1+8+4+8)
		if err := xhex.Decode(k[2:2+8], []byte(tagValue[0:8*2])); err == nil {
			k[0] = prefixTag32[0]
			k[1] = letterPrefix
			return prefixTag32, k
		}
	}

	// if it looks like an "a" tag, index it in this special format, with letter (tag name) prefix
	// a:<letter1>:<kind2>:<pk8>:<d30>:<ts4>:<idPtr8>
	spl := strings.Split(tagValue, ":")
	if len(spl) == 3 && len(spl[1]) == 64 {
		k = make([]byte, 1+1+2+8+30+4+8)
		if err := xhex.Decode(k[1+1+2:1+1+2+8], []byte(spl[1][0:8*2])); err == nil {
			if kind, err := strconv.ParseUint(spl[0], 10, 16); err == nil {
				k[0] = prefixTagAddr[0]
				k[1] = letterPrefix
				binary.BigEndian.PutUint16(k[1+1:1+1+2], uint16(kind))
				// limit "d" identifier to 30 bytes (so we don't have to grow our byte slice)
				copy(k[1+1+2+8:1+1+2+8+30], spl[2])
				return prefixTagAddr, k
			}
		}
	}

	// index whatever else as a md5 hash of the contents, with letter (tag name) prefix
	// m:<letter1>:<md516>:<ts4>:<idPtr8>
	h := md5.New()
	h.Write([]byte(tagValue))
	k = make([]byte, 1+1+16+4+8)
	k[0] = prefixTag[0]
	k[1] = letterPrefix
	copy(k[2:2+16], h.Sum(nil))

	return prefixTag, k
}
