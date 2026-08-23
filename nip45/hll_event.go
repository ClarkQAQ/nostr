package nip45

import (
	"iter"
	"strconv"

	"fiatjaf.com/nostr"
)

// HLLTarget is one HyperLogLog update target extracted from an event: the
// tag key and reference value identifying the sketch, plus the deterministic
// pubkey bit offset used when adding to it.
type HLLTarget struct {
	TagKey string
	Ref    string
	Offset int
}

// HyperLogLogTargetsForEventWithTags yields every (tag key, reference, offset)
// triple an event contributes to, following NIP-45. The tag key lets backends
// namespace their sketches (an "#e" sketch and a "#q" sketch for the same
// reference are distinct).
func HyperLogLogTargetsForEventWithTags(evt nostr.Event) iter.Seq[HLLTarget] {
	return func(yield func(HLLTarget) bool) {
		mk := func(tagKey, v string) HLLTarget {
			p, _ := strconv.ParseInt(v[32:33], 16, 64)
			return HLLTarget{TagKey: tagKey, Ref: v, Offset: int(p + 8)}
		}
		emit := func(t HLLTarget) bool { return yield(t) }

		switch evt.Kind {
		case 1:
			// reply count (last #e)
			if lastE := evt.Tags.FindLast("e"); lastE != nil {
				if v := lastE[1]; nostr.IsValid32ByteHex(v) {
					if !emit(mk("e", v)) {
						return
					}
				}
			}
			// quote count (#q)
			for qTag := range evt.Tags.FindAll("q") {
				if v := qTag[1]; nostr.IsValid32ByteHex(v) {
					if !emit(mk("q", v)) {
						return
					}
				}
			}
		case 3:
			// follower counts
			for _, tag := range evt.Tags {
				if len(tag) >= 2 && tag[0] == "p" && nostr.IsValid32ByteHex(tag[1]) {
					if !emit(mk("p", tag[1])) {
						return
					}
				}
			}
		case 6:
			// repost count (assume just one #e)
			if lastE := evt.Tags.Find("e"); lastE != nil {
				if v := lastE[1]; nostr.IsValid32ByteHex(v) {
					if !emit(mk("e", v)) {
						return
					}
				}
			}
		case 7:
			// reaction count (assume just one #e)
			if lastE := evt.Tags.Find("e"); lastE != nil {
				if v := lastE[1]; nostr.IsValid32ByteHex(v) {
					if !emit(mk("e", v)) {
						return
					}
				}
			}
		case 1111:
			// comment count (#E, #e)
			if eTag := evt.Tags.Find("E"); eTag != nil {
				if v := eTag[1]; nostr.IsValid32ByteHex(v) {
					if !emit(mk("E", v)) {
						return
					}
				}
			}
			for eTag := range evt.Tags.FindAll("e") {
				if v := eTag[1]; nostr.IsValid32ByteHex(v) {
					if !emit(mk("e", v)) {
						return
					}
				}
			}
			// quote count (#q)
			for qTag := range evt.Tags.FindAll("q") {
				if v := qTag[1]; nostr.IsValid32ByteHex(v) {
					if !emit(mk("q", v)) {
						return
					}
				}
			}
		}
	}
}

// HyperLogLogTargetsForEvent yields the reference/offset pairs an event
// contributes to, following NIP-45. Kept for backward compatibility; backends
// that namespace sketches by tag key should use
// HyperLogLogTargetsForEventWithTags.
func HyperLogLogTargetsForEvent(evt nostr.Event) iter.Seq2[string, int] {
	return func(yield func(string, int) bool) {
		for t := range HyperLogLogTargetsForEventWithTags(evt) {
			if !yield(t.Ref, t.Offset) {
				return
			}
		}
	}
}
