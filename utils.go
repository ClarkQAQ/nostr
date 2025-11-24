package nostr

import (
	"bytes"
	"cmp"
	"net/url"
	"slices"
	"unsafe"

	"github.com/templexxx/xhex"
)

// IsValidRelayURL checks if a URL is a valid relay URL (ws:// or wss://).
func IsValidRelayURL(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	if parsed.Scheme != "wss" && parsed.Scheme != "ws" {
		return false
	}
	return true
}

// HexEncodeToString encodes src into a hex string.
func HexEncodeToString(src []byte) string {
	dst := make([]byte, len(src)*2)
	xhex.Encode(dst, src)
	return unsafe.String(unsafe.SliceData(dst), len(dst))
}

// HexDecodeString decodes a hex string into bytes.
func HexDecodeString(s string) ([]byte, error) {
	src := unsafe.Slice(unsafe.StringData(s), len(s))
	if len(src)%2 != 0 {
		return nil, xhex.ErrLength
	}
	dst := make([]byte, len(src)/2)
	err := xhex.Decode(dst, src)
	if err != nil {
		return nil, err
	}
	return dst, nil
}

// IsValid32ByteHex checks if a string is a valid 32-byte hex string.
func IsValid32ByteHex(thing string) bool {
	if !isLowerHex(thing) {
		return false
	}
	if len(thing) != 64 {
		return false
	}
	_, err := HexDecodeString(thing)
	return err == nil
}

// CompareEvent is meant to to be used with slices.Sort
func CompareEvent(a, b Event) int {
	if a.CreatedAt == b.CreatedAt {
		return bytes.Compare(a.ID[:], b.ID[:])
	}
	return cmp.Compare(a.CreatedAt, b.CreatedAt)
}

// CompareEventReverse is meant to to be used with slices.Sort
func CompareEventReverse(b, a Event) int {
	if a.CreatedAt == b.CreatedAt {
		return bytes.Compare(a.ID[:], b.ID[:])
	}
	return cmp.Compare(a.CreatedAt, b.CreatedAt)
}

// AppendUnique adds items to an array only if they don't already exist in the array.
// Returns the modified array.
func AppendUnique[I comparable](arr []I, item ...I) []I {
	for _, item := range item {
		if slices.Contains(arr, item) {
			return arr
		}
		arr = append(arr, item)
	}
	return arr
}
