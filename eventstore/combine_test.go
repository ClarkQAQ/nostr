package eventstore

import (
	"cmp"
	"slices"
	"testing"

	"fiatjaf.com/nostr"
)

func FuzzSortedMerge(f *testing.F) {
	f.Add(uint(4), uint(4), uint(3), uint(7), uint8(2), uint8(1))
	f.Add(uint(0), uint(4), uint(3), uint(7), uint8(2), uint8(1))
	f.Fuzz(func(t *testing.T, len1, len2 uint, start1, start2 uint, diff1, diff2 uint8) {
		maxxx := max(len1*uint(diff1), len2*uint(diff2))
		start1 += maxxx
		start2 += maxxx

		merged := SortedMerge(
			func(yield func(nostr.Event) bool) {
				for range len1 {
					if !yield(nostr.Event{CreatedAt: nostr.Timestamp(start1)}) {
						return
					}
					start1 -= uint(diff1)
				}
			},
			func(yield func(nostr.Event) bool) {
				for range len2 {
					if !yield(nostr.Event{CreatedAt: nostr.Timestamp(start2)}) {
						return
					}
					start2 -= uint(diff2)
				}
			},
		)
		result := slices.Collect(merged)

		// assert length
		if len(result) != int(len1+len2) {
			t.Fatalf("expected %d events, got %d", len1+len2, len(result))
		}

		// assert sorted descending
		_ = slices.IsSortedFunc(result, func(a, b nostr.Event) int { return -1 * cmp.Compare(a.CreatedAt, b.CreatedAt) })
		for i := 1; i < len(result); i++ {
			if result[i].CreatedAt > result[i-1].CreatedAt {
				t.Fatalf("events not sorted descending at index %d: %d > %d", i, result[i].CreatedAt, result[i-1].CreatedAt)
			}
		}
	})
}
