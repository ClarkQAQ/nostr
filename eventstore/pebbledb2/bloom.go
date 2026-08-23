package pebbledb

// Content signature index ('F' keyspace, see keys.go).
//
// Every event with content of at least 3 bytes gets a 128-byte bloom filter
// built from the byte trigrams of its ASCII-lowercased content. A search
// pattern that is a substring of the content must have all of its own
// trigrams present in the content, so testing the pattern's trigrams against
// the bloom never produces false negatives — it only skips bodies that
// cannot possibly match. Short content (fewer than 3 bytes) gets no key, and
// cannot match any 3+ byte pattern anyway.

const (
	bloomBytes = 128
	bloomBits  = bloomBytes * 8
	bloomK     = 4

	// maxSearchGrams caps how many pattern trigrams are tested against each
	// bloom. Testing a subset keeps the zero-false-negative property while
	// bounding the per-candidate work for very long patterns.
	maxSearchGrams = 16
)

const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211

	// fnvOffset64Alt is a second offset basis for the double-hash second
	// probe, so each trigram spreads over bloomK independent bits.
	fnvOffset64Alt = 0x9e3779b97f4a7c15
)

func fnv1a64(seed uint64, b []byte) uint64 {
	h := seed
	for _, c := range b {
		h ^= uint64(c)
		h *= fnvPrime64
	}
	return h
}

// bloomSetCount sets the bloomK bits for one trigram and reports whether any
// bit was newly set (false when all bits were already set).
func bloomSetCount(bloom []byte, g [3]byte) bool {
	h1 := fnv1a64(fnvOffset64, g[:])
	h2 := fnv1a64(fnvOffset64Alt, g[:])
	added := false
	for k := 0; k < bloomK; k++ {
		bit := (h1 + uint64(k)*h2) % bloomBits
		mask := byte(1 << (bit & 7))
		if bloom[bit>>3]&mask == 0 {
			bloom[bit>>3] |= mask
			added = true
		}
	}
	return added
}

// contentBloom builds the 128-byte bloom signature for an event's content,
// or returns nil when the content is shorter than 3 bytes (no key written).
// Hashing stops early once the bloom saturates — every bit set means every
// test passes, so saturation only costs body reads, never correctness.
func contentBloom(content string) []byte {
	if len(content) < 3 {
		return nil
	}
	bloom := make([]byte, bloomBytes)
	lower := make([]byte, len(content))
	for i := 0; i < len(content); i++ {
		c := content[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		lower[i] = c
	}
	setBits := 0
	for i := 0; i+3 <= len(lower); i++ {
		g := [3]byte{lower[i], lower[i+1], lower[i+2]}
		if bloomSetCount(bloom, g) {
			setBits++
			if setBits >= bloomBits {
				break
			}
		}
	}
	return bloom
}

// buildSearchGrams extracts the deduplicated byte trigrams of the lowercased
// search pattern, capped at maxSearchGrams. A nil result means the pattern is
// too short for signature prefiltering and the generic scan handles it.
func buildSearchGrams(pattern string) [][3]byte {
	if len(pattern) < 3 {
		return nil
	}
	out := make([][3]byte, 0, min(maxSearchGrams, len(pattern)-2))
	seen := make(map[[3]byte]struct{}, min(maxSearchGrams, len(pattern)-2))
	for i := 0; i+3 <= len(pattern) && len(out) < maxSearchGrams; i++ {
		b0, b1, b2 := pattern[i], pattern[i+1], pattern[i+2]
		if b0 >= 'A' && b0 <= 'Z' {
			b0 += 32
		}
		if b1 >= 'A' && b1 <= 'Z' {
			b1 += 32
		}
		if b2 >= 'A' && b2 <= 'Z' {
			b2 += 32
		}
		g := [3]byte{b0, b1, b2}
		if _, dup := seen[g]; dup {
			continue
		}
		seen[g] = struct{}{}
		out = append(out, g)
	}
	return out
}

// bloomMaybeContains reports whether the bloom may contain all of the pattern
// trigrams. It never returns false for content that actually contains the
// pattern; false positives are resolved by reading the body. An unexpected
// value length is conservatively treated as "possibly contains".
func bloomMaybeContains(bloom []byte, grams [][3]byte) bool {
	if len(bloom) != bloomBytes {
		return true
	}
	for _, g := range grams {
		h1 := fnv1a64(fnvOffset64, g[:])
		h2 := fnv1a64(fnvOffset64Alt, g[:])
		for k := 0; k < bloomK; k++ {
			bit := (h1 + uint64(k)*h2) % bloomBits
			if bloom[bit>>3]&(1<<(bit&7)) == 0 {
				return false
			}
		}
	}
	return true
}
