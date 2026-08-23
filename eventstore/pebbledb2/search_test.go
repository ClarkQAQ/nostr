package pebbledb

import (
	"math"
	"testing"
)

func TestSplitRange(t *testing.T) {
	check := func(dMin, dMax int64, n int) {
		t.Helper()
		segs := splitRange(dMin, dMax, n)
		if len(segs) == 0 {
			t.Fatalf("splitRange(%d,%d,%d): empty", dMin, dMax, n)
		}
		// full coverage and no overlap
		var prevHi int64 = -1
		for i, seg := range segs {
			lo, hi := seg[0], seg[1]
			if lo > hi {
				t.Fatalf("seg %d: lo %d > hi %d", i, lo, hi)
			}
			if i > 0 && lo <= prevHi {
				t.Fatalf("seg %d overlaps: lo %d <= prev hi %d", i, lo, prevHi)
			}
			if i > 0 && lo != prevHi+1 {
				t.Fatalf("seg %d gap: lo %d != prev hi %d + 1", i, lo, prevHi)
			}
			prevHi = hi
		}
		if segs[0][0] != dMin || segs[len(segs)-1][1] != dMax {
			t.Fatalf("splitRange(%d,%d,%d): coverage [%d,%d]",
				dMin, dMax, n, segs[0][0], segs[len(segs)-1][1])
		}
		if len(segs) > n {
			t.Fatalf("splitRange(%d,%d,%d): %d segs > %d", dMin, dMax, n, len(segs), n)
		}
	}

	check(0, 100, 8)
	check(0, 7, 8)
	check(0, 1, 8)
	check(0, 0, 8)
	check(1_700_000_000, 1_700_000_100, 8)
	check(0, math.MaxInt64, 8) // 2^63+1 points: must not overflow
	check(math.MinInt64, math.MaxInt64, 8)
	check(-100, -1, 4)
}

func TestSplitRangeEmpty(t *testing.T) {
	if segs := splitRange(10, 5, 8); segs != nil {
		t.Fatalf("expected nil, got %v", segs)
	}
}
