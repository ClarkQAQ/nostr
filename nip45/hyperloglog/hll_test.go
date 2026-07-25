package hyperloglog

import (
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

func randomPubkey(rng *rand.Rand) (pk [32]byte) {
	for i := range pk {
		pk[i] = uint8(rng.UintN(256))
	}
	return
}

func TestHyperLogLogAccuracy(t *testing.T) {
	for _, count := range []int{
		2, 4, 6, 7, 12, 15, 22, 36, 44, 47,
		64, 77, 89, 95, 104, 116, 122, 144,
		150, 199, 300, 350, 400, 500, 600,
		777, 922, 1000, 1500, 2222, 9999,
		13600, 80000, 133333, 200000,
	} {
		count := count
		t.Run("", func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewPCG(42, uint64(count)))
			hll := New(8)

			for range count {
				hll.Add(randomPubkey(rng))
			}

			c := hll.Count()
			c100 := int(c * 100)
			require.Greater(t, c100, count*85, "count=%d hll=%d", count, c)
			require.Less(t, c100, count*115, "count=%d hll=%d", count, c)
		})
	}
}

func TestHyperLogLogMerge(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(42, 0))

	n := 10000
	var all [][32]byte
	for range n {
		all = append(all, randomPubkey(rng))
	}

	full := New(8)
	halfA := New(8)
	halfB := New(8)

	for _, pk := range all {
		full.Add(pk)
	}
	for i, pk := range all {
		if i%2 == 0 {
			halfA.Add(pk)
		} else {
			halfB.Add(pk)
		}
	}
	halfA.Merge(halfB)

	require.Equal(t, full.Count(), halfA.Count())
}

func TestHyperLogLogMergeRegisters(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 0))

	n := 1000
	hllA := New(8)
	hllB := New(8)

	for range n {
		hllA.Add(randomPubkey(rng))
		hllB.Add(randomPubkey(rng))
	}

	hllC := New(8)
	hllC.MergeRegisters(hllA.GetRegisters())
	hllC.MergeRegisters(hllB.GetRegisters())

	combined := New(8)
	combined.Merge(hllA)
	combined.Merge(hllB)

	require.Equal(t, combined.Count(), hllC.Count())
	require.Equal(t, combined.GetRegisters(), hllC.GetRegisters())
}
