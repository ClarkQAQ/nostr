package pebbledb

import (
	"crypto/md5"
	"encoding/binary"
	"iter"
	"math"
	"slices"
	"strconv"
	"strings"

	"fiatjaf.com/nostr"
	"github.com/templexxx/xhex"
)

const (
	// maxTimestamp is used to invert timestamps for reverse chronological iteration.
	// inverted_ts = maxTimestamp - actual_ts
	// This allows forward iteration to yield results in newest-first order.
	maxTimestamp = math.MaxUint64
)

// invertTimestamp converts a timestamp to inverted form for reverse chronological ordering.
// Forward iteration on inverted timestamps yields newest events first.
func invertTimestamp(ts nostr.Timestamp) uint64 {
	return maxTimestamp - uint64(ts)
}

// restoreTimestamp converts an inverted timestamp back to the original.
func restoreTimestamp(inverted uint64) nostr.Timestamp {
	return nostr.Timestamp(maxTimestamp - inverted)
}

// indexKey represents an index key with its prefix for iteration bounds.
type indexKey struct {
	prefix  []byte
	fullKey []byte
}

// makeRawKey creates a key for raw event storage: r<id:32>
func makeRawKey(id nostr.ID) []byte {
	key := make([]byte, 1+32)
	key[0] = prefixRaw[0]
	copy(key[1:], id[:])
	return key
}

// getIndexKeysForEvent generates all index keys for an event.
// All index keys end with <inverted_ts:8><id:32> for consistent ordering.
func getIndexKeysForEvent(evt nostr.Event) iter.Seq[indexKey] {
	return func(yield func(indexKey) bool) {
		id := evt.ID[:]
		invertedTs := make([]byte, 8)
		binary.BigEndian.PutUint64(invertedTs, invertTimestamp(evt.CreatedAt))

		// Index by created_at (global timeline)
		// c<inverted_ts:8><id:32>
		{
			key := make([]byte, 1+8+32)
			key[0] = prefixCreatedAt[0]
			copy(key[1:1+8], invertedTs)
			copy(key[1+8:], id)
			if !yield(indexKey{prefix: prefixCreatedAt, fullKey: key}) {
				return
			}
		}

		// Index by kind
		// k<kind:2><inverted_ts:8><id:32>
		{
			key := make([]byte, 1+2+8+32)
			key[0] = prefixKind[0]
			binary.BigEndian.PutUint16(key[1:1+2], uint16(evt.Kind))
			copy(key[1+2:1+2+8], invertedTs)
			copy(key[1+2+8:], id)
			if !yield(indexKey{prefix: prefixKind, fullKey: key}) {
				return
			}
		}

		// Index by pubkey
		// p<pk:32><inverted_ts:8><id:32>
		{
			key := make([]byte, 1+32+8+32)
			key[0] = prefixPubkey[0]
			copy(key[1:1+32], evt.PubKey[:])
			copy(key[1+32:1+32+8], invertedTs)
			copy(key[1+32+8:], id)
			if !yield(indexKey{prefix: prefixPubkey, fullKey: key}) {
				return
			}
		}

		// Index by pubkey+kind (compound)
		// x<pk:32><kind:2><inverted_ts:8><id:32>
		{
			key := make([]byte, 1+32+2+8+32)
			key[0] = prefixPubkeyKind[0]
			copy(key[1:1+32], evt.PubKey[:])
			binary.BigEndian.PutUint16(key[1+32:1+32+2], uint16(evt.Kind))
			copy(key[1+32+2:1+32+2+8], invertedTs)
			copy(key[1+32+2+8:], id)
			if !yield(indexKey{prefix: prefixPubkeyKind, fullKey: key}) {
				return
			}
		}

		// Index tags
		for i, tag := range evt.Tags {
			if len(tag) < 2 || len(tag[0]) != 1 || len(tag[1]) == 0 || len(tag[1]) > 100 {
				// Not indexable
				continue
			}

			// Skip duplicate tags (only index first occurrence)
			firstIndex := slices.IndexFunc(evt.Tags, func(t nostr.Tag) bool {
				return len(t) >= 2 && t[0] == tag[0] && t[1] == tag[1]
			})
			if firstIndex != i {
				continue
			}

			prefix, key := makeTagIndexKey(tag[0], tag[1], invertedTs, id)
			if !yield(indexKey{prefix: prefix, fullKey: key}) {
				return
			}
		}
	}
}

// makeTagIndexKey creates the appropriate tag index key based on tag value format.
// Returns the prefix and full key.
func makeTagIndexKey(tagName string, tagValue string, invertedTs []byte, id []byte) ([]byte, []byte) {
	letterPrefix := byte(int(tagName[0]) % 256)

	// If it's 32 bytes as hex (64 chars), save it as bytes
	// t<letter:1><tagVal:32><inverted_ts:8><id:32>
	if len(tagValue) == 64 {
		key := make([]byte, 1+1+32+8+32)
		if err := xhex.Decode(key[2:2+32], []byte(tagValue)); err == nil {
			key[0] = prefixTag32[0]
			key[1] = letterPrefix
			copy(key[2+32:2+32+8], invertedTs)
			copy(key[2+32+8:], id)
			return prefixTag32, key
		}
	}

	// If it looks like an "a" tag (kind:pubkey:identifier), use special format
	// a<letter:1><kind:2><pk:32><d:30><inverted_ts:8><id:32>
	spl := strings.Split(tagValue, ":")
	if len(spl) == 3 && len(spl[1]) == 64 {
		key := make([]byte, 1+1+2+32+30+8+32)
		if err := xhex.Decode(key[1+1+2:1+1+2+32], []byte(spl[1])); err == nil {
			if kind, err := strconv.ParseUint(spl[0], 10, 16); err == nil {
				key[0] = prefixTagAddr[0]
				key[1] = letterPrefix
				binary.BigEndian.PutUint16(key[1+1:1+1+2], uint16(kind))
				// Limit "d" identifier to 30 bytes
				copy(key[1+1+2+32:1+1+2+32+30], spl[2])
				copy(key[1+1+2+32+30:1+1+2+32+30+8], invertedTs)
				copy(key[1+1+2+32+30+8:], id)
				return prefixTagAddr, key
			}
		}
	}

	// Default: MD5 hash of tag value
	// m<letter:1><md5:16><inverted_ts:8><id:32>
	h := md5.New()
	h.Write([]byte(tagValue))
	key := make([]byte, 1+1+16+8+32)
	key[0] = prefixTag[0]
	key[1] = letterPrefix
	copy(key[2:2+16], h.Sum(nil))
	copy(key[2+16:2+16+8], invertedTs)
	copy(key[2+16+8:], id)
	return prefixTag, key
}

// makeTagQueryPrefix creates the prefix for tag queries (without timestamp/id suffix).
func makeTagQueryPrefix(tagName string, tagValue string) []byte {
	letterPrefix := byte(int(tagName[0]) % 256)

	// 32-byte hex value
	if len(tagValue) == 64 {
		prefix := make([]byte, 1+1+32)
		if err := xhex.Decode(prefix[2:2+32], []byte(tagValue)); err == nil {
			prefix[0] = prefixTag32[0]
			prefix[1] = letterPrefix
			return prefix
		}
	}

	// Addressable reference
	spl := strings.Split(tagValue, ":")
	if len(spl) == 3 && len(spl[1]) == 64 {
		prefix := make([]byte, 1+1+2+32+30)
		if err := xhex.Decode(prefix[1+1+2:1+1+2+32], []byte(spl[1])); err == nil {
			if kind, err := strconv.ParseUint(spl[0], 10, 16); err == nil {
				prefix[0] = prefixTagAddr[0]
				prefix[1] = letterPrefix
				binary.BigEndian.PutUint16(prefix[1+1:1+1+2], uint16(kind))
				copy(prefix[1+1+2+32:1+1+2+32+30], spl[2])
				return prefix
			}
		}
	}

	// MD5 hash
	h := md5.New()
	h.Write([]byte(tagValue))
	prefix := make([]byte, 1+1+16)
	prefix[0] = prefixTag[0]
	prefix[1] = letterPrefix
	copy(prefix[2:2+16], h.Sum(nil))
	return prefix
}

// extractIDFromIndexKey extracts the event ID from an index key.
// All index keys end with <inverted_ts:8><id:32>.
func extractIDFromIndexKey(key []byte) nostr.ID {
	if len(key) < 32 {
		return nostr.ID{}
	}
	return nostr.ID(key[len(key)-32:])
}

// extractTimestampFromIndexKey extracts the timestamp from an index key.
// All index keys end with <inverted_ts:8><id:32>.
func extractTimestampFromIndexKey(key []byte) nostr.Timestamp {
	if len(key) < 40 {
		return 0
	}
	inverted := binary.BigEndian.Uint64(key[len(key)-40 : len(key)-32])
	return restoreTimestamp(inverted)
}
