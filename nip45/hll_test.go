package nip45

import (
	"math/rand/v2"
	"strconv"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip45/hyperloglog"
	"github.com/stretchr/testify/require"
)

func randomHex(rng *rand.Rand) string {
	var b [32]byte
	for i := range b {
		b[i] = uint8(rng.UintN(256))
	}
	return nostr.HexEncodeToString(b[:])
}

func randomPubkey(rng *rand.Rand) [32]byte {
	var pk [32]byte
	for i := range pk {
		pk[i] = uint8(rng.UintN(256))
	}
	return pk
}

func offsetFromHex(rng *rand.Rand, s string) int {
	p, _ := strconv.ParseInt(s[32:33], 16, 64)
	return int(p + 8)
}

func TestHyperLogLogTargetsForEvent_Kind1_Reply(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 0))
	pk := randomPubkey(rng)
	eid := randomHex(rng)

	evt := nostr.Event{
		Kind:   1,
		PubKey: pk,
		Tags:   nostr.Tags{{"e", eid}},
	}

	var refs []string
	var offsets []int
	for ref, offset := range HyperLogLogTargetsForEvent(evt) {
		refs = append(refs, ref)
		offsets = append(offsets, offset)
	}

	require.Equal(t, []string{eid}, refs)
	require.Equal(t, []int{offsetFromHex(rng, eid)}, offsets)
}

func TestHyperLogLogTargetsForEvent_Kind1_Quote(t *testing.T) {
	rng := rand.New(rand.NewPCG(2, 0))
	pk := randomPubkey(rng)
	qid := randomHex(rng)

	evt := nostr.Event{
		Kind:   1,
		PubKey: pk,
		Tags:   nostr.Tags{{"q", qid}},
	}

	var refs []string
	for ref := range HyperLogLogTargetsForEvent(evt) {
		refs = append(refs, ref)
	}

	require.Equal(t, []string{qid}, refs)
}

func TestHyperLogLogTargetsForEvent_Kind1_Both(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 0))
	pk := randomPubkey(rng)
	eid := randomHex(rng)
	qid := randomHex(rng)

	evt := nostr.Event{
		Kind:   1,
		PubKey: pk,
		Tags:   nostr.Tags{{"e", eid}, {"q", qid}},
	}

	var refs []string
	for ref := range HyperLogLogTargetsForEvent(evt) {
		refs = append(refs, ref)
	}

	require.ElementsMatch(t, []string{eid, qid}, refs)
}

func TestHyperLogLogTargetsForEvent_Kind3(t *testing.T) {
	rng := rand.New(rand.NewPCG(4, 0))
	pk := randomPubkey(rng)
	follow1 := randomHex(rng)
	follow2 := randomHex(rng)

	evt := nostr.Event{
		Kind:   3,
		PubKey: pk,
		Tags:   nostr.Tags{{"p", follow1}, {"p", follow2}},
	}

	var refs []string
	for ref := range HyperLogLogTargetsForEvent(evt) {
		refs = append(refs, ref)
	}

	require.ElementsMatch(t, []string{follow1, follow2}, refs)
}

func TestHyperLogLogTargetsForEvent_Kind7(t *testing.T) {
	rng := rand.New(rand.NewPCG(6, 0))
	pk := randomPubkey(rng)
	eid := randomHex(rng)

	// kind 7 uses last #e
	evt := nostr.Event{
		Kind:   7,
		PubKey: pk,
		Tags:   nostr.Tags{{"e", eid}},
	}

	var refs []string
	for ref := range HyperLogLogTargetsForEvent(evt) {
		refs = append(refs, ref)
	}

	require.Equal(t, []string{eid}, refs)
}

func TestHyperLogLogTargetsForEvent_Kind1111(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 0))
	pk := randomPubkey(rng)
	eid := randomHex(rng)

	evt := nostr.Event{
		Kind:   1111,
		PubKey: pk,
		Tags:   nostr.Tags{{"E", eid}},
	}

	var refs []string
	for ref := range HyperLogLogTargetsForEvent(evt) {
		refs = append(refs, ref)
	}

	require.Equal(t, []string{eid}, refs)
}

func TestHyperLogLogFilterIsEligible(t *testing.T) {
	tests := []struct {
		name     string
		filter   nostr.Filter
		eligible bool
	}{
		{"reaction count (#e + kind 7)", nostr.Filter{Tags: nostr.TagMap{"e": {"id"}}, Kinds: []nostr.Kind{7}}, true},
		{"repost count (#e + kind 6)", nostr.Filter{Tags: nostr.TagMap{"e": {"id"}}, Kinds: []nostr.Kind{6}}, true},
		{"reply count (#e + kind 1)", nostr.Filter{Tags: nostr.TagMap{"e": {"id"}}, Kinds: []nostr.Kind{1}}, true},
		{"quote count (#q + kinds 1,1111)", nostr.Filter{Tags: nostr.TagMap{"q": {"id"}}, Kinds: []nostr.Kind{1, 1111}}, true},
		{"comment count (#E + kind 1111)", nostr.Filter{Tags: nostr.TagMap{"E": {"id"}}, Kinds: []nostr.Kind{1111}}, true},
		{"follower count (#p + kind 3)", nostr.Filter{Tags: nostr.TagMap{"p": {"id"}}, Kinds: []nostr.Kind{3}}, true},
		{"wrong kind for #e", nostr.Filter{Tags: nostr.TagMap{"e": {"id"}}, Kinds: []nostr.Kind{5}}, false},
		{"wrong kind for #p", nostr.Filter{Tags: nostr.TagMap{"p": {"id"}}, Kinds: []nostr.Kind{1}}, false},
		{"has IDs", nostr.Filter{IDs: []nostr.ID{{1}}, Tags: nostr.TagMap{"e": {"id"}}, Kinds: []nostr.Kind{7}}, false},
		{"has Since", nostr.Filter{Since: 1000, Tags: nostr.TagMap{"e": {"id"}}, Kinds: []nostr.Kind{7}}, false},
		{"has Until", nostr.Filter{Until: 1000, Tags: nostr.TagMap{"e": {"id"}}, Kinds: []nostr.Kind{7}}, false},
		{"has Authors", nostr.Filter{Authors: []nostr.PubKey{{}}, Tags: nostr.TagMap{"e": {"id"}}, Kinds: []nostr.Kind{7}}, false},
		{"has Search", nostr.Filter{Search: "foo", Tags: nostr.TagMap{"e": {"id"}}, Kinds: []nostr.Kind{7}}, false},
		{"multiple tags", nostr.Filter{Tags: nostr.TagMap{"e": {"id"}, "p": {"pk"}}, Kinds: []nostr.Kind{7}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.eligible, HyperLogLogFilterIsEligible(tt.filter))
		})
	}
}

func TestHyperLogLogEventPubkeyOffsetForFilter(t *testing.T) {
	rng := rand.New(rand.NewPCG(8, 0))
	eid := randomHex(rng)

	offset := HyperLogLogEventPubkeyOffsetForFilter(nostr.Filter{
		Tags:  nostr.TagMap{"e": {eid}},
		Kinds: []nostr.Kind{7},
	})

	expected := offsetFromHex(rng, eid)
	require.Equal(t, expected, offset)
}

func TestHyperLogLogOffsetConsistency(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 0))
	pk := randomPubkey(rng)
	eid := randomHex(rng)

	evt := nostr.Event{
		Kind:   7,
		PubKey: pk,
		Tags:   nostr.Tags{{"e", eid}},
	}

	filter := nostr.Filter{
		Tags:  nostr.TagMap{"e": {eid}},
		Kinds: []nostr.Kind{7},
	}

	filterOffset := HyperLogLogEventPubkeyOffsetForFilter(filter)

	var eventOffset int
	for ref, offset := range HyperLogLogTargetsForEvent(evt) {
		if ref == eid {
			eventOffset = offset
		}
	}

	require.Equal(t, filterOffset, eventOffset)
}

func TestHyperLogLogWithGeneratedEvents(t *testing.T) {
	for _, count := range []int{10, 100, 1000, 5000} {
		count := count
		t.Run("", func(t *testing.T) {
			t.Parallel()
			// each parallel subtest gets its own source; the shared rng used
			// before was a data race
			rng := rand.New(rand.NewPCG(10, uint64(count)))
			ref := randomHex(rng)
			offset := offsetFromHex(rng, ref)
			hll := hyperloglog.New(offset)

			for range count {
				pk := randomPubkey(rng)
				evt := nostr.Event{
					Kind:   7,
					PubKey: pk,
					Tags:   nostr.Tags{{"e", ref}},
				}

				for target, targetOffset := range HyperLogLogTargetsForEvent(evt) {
					if target == ref {
						hll.Add(evt.PubKey)
						_ = targetOffset
					}
				}
			}

			c := hll.Count()
			c100 := int(c * 100)
			require.Greater(t, c100, count*85, "count=%d hll=%d", count, c)
			require.Less(t, c100, count*115, "count=%d hll=%d", count, c)
		})
	}
}
