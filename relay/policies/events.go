package policies

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
	"fiatjaf.com/nostr/nip27"
	"fiatjaf.com/nostr/nip70"
	"fiatjaf.com/nostr/sdk"
)

// PreventTooManyIndexableTags returns a function that can be used as a RejectFilter that will reject
// events with more indexable (single-character) tags than the specified number.
//
// If ignoreKinds is given this restriction will not apply to these kinds (useful for allowing a bigger).
// If onlyKinds is given then all other kinds will be ignored.
func PreventTooManyIndexableTags(max int, ignoreKinds []nostr.Kind, onlyKinds []nostr.Kind) func(context.Context, nostr.Event) (bool, string) {
	slices.Sort(ignoreKinds)
	slices.Sort(onlyKinds)

	ignore := func(kind nostr.Kind) bool { return false }
	if len(ignoreKinds) > 0 {
		ignore = func(kind nostr.Kind) bool {
			_, isIgnored := slices.BinarySearch(ignoreKinds, kind)
			return isIgnored
		}
	}
	if len(onlyKinds) > 0 {
		ignore = func(kind nostr.Kind) bool {
			_, isApplicable := slices.BinarySearch(onlyKinds, kind)
			return !isApplicable
		}
	}

	return func(ctx context.Context, event nostr.Event) (reject bool, msg string) {
		if ignore(event.Kind) {
			return false, ""
		}

		ntags := 0
		for _, tag := range event.Tags {
			if len(tag) > 0 && len(tag[0]) == 1 {
				ntags++
			}
		}
		if ntags > max {
			return true, "too many indexable tags"
		}
		return false, ""
	}
}

// PreventLargeContent rejects events with content too large
func PreventLargeContent(maxContent int) func(context.Context, nostr.Event) (bool, string) {
	return func(ctx context.Context, event nostr.Event) (reject bool, msg string) {
		if len(event.Content) > maxContent {
			return true, "content is too big"
		}
		return false, ""
	}
}

// RestrictToSpecifiedKinds returns a function that can be used as a RejectFilter that will reject
// any events with kinds different than the specified ones.
func RestrictToSpecifiedKinds(allowEphemeral bool, kinds ...nostr.Kind) func(context.Context, nostr.Event) (bool, string) {
	// sort the kinds in increasing order
	slices.Sort(kinds)

	return func(ctx context.Context, event nostr.Event) (reject bool, msg string) {
		if allowEphemeral && event.Kind.IsEphemeral() {
			return false, ""
		}

		if _, allowed := slices.BinarySearch(kinds, nostr.Kind(event.Kind)); allowed {
			return false, ""
		}

		return true, fmt.Sprintf("received event kind %d not allowed", event.Kind)
	}
}

func PreventTimestampsInThePast(threshold time.Duration) func(context.Context, nostr.Event) (bool, string) {
	thresholdSeconds := nostr.Timestamp(threshold.Seconds())
	return func(ctx context.Context, event nostr.Event) (reject bool, msg string) {
		if nostr.Now()-event.CreatedAt > thresholdSeconds {
			return true, "event too old"
		}
		return false, ""
	}
}

func PreventTimestampsInTheFuture(threshold time.Duration) func(context.Context, nostr.Event) (bool, string) {
	thresholdSeconds := nostr.Timestamp(threshold.Seconds())
	return func(ctx context.Context, event nostr.Event) (reject bool, msg string) {
		if event.CreatedAt-nostr.Now() > thresholdSeconds {
			return true, "event too much in the future"
		}
		return false, ""
	}
}

func RejectEventsWithBase64Media(ctx context.Context, evt nostr.Event) (bool, string) {
	return strings.Contains(evt.Content, "data:image/") || strings.Contains(evt.Content, "data:video/"), "event with base64 media"
}

func OnlyAllowNIP70ProtectedEvents(ctx context.Context, event nostr.Event) (reject bool, msg string) {
	if nip70.IsProtected(event) {
		return false, ""
	}
	return true, "blocked: we only accept events protected with the nip70 \"-\" tag"
}

var nostrReferencesPrefix = regexp.MustCompile(`\b(nevent1|npub1|nprofile1|note1)\w*\b`)

func RejectUnprefixedNostrReferences(ctx context.Context, event nostr.Event) (bool, string) {
	content := sdk.GetMainContent(event)

	// only do it for stuff that wasn't parsed as blocks already
	// (since those are already good references or URLs)
	for block := range nip27.Parse(content) {
		if block.Pointer == nil {
			matches := nostrReferencesPrefix.FindAllStringIndex(block.Text, -1)
			for _, match := range matches {
				start := match[0]
				end := match[1]
				ref := block.Text[start:end]
				_, _, err := nip19.Decode(ref)
				if err != nil {
					// invalid reference, ignore and allow
					// (it's probably someone saying something like "oh, write something like npub1foo...")
					continue
				}

				return true, "references must be prefixed with \"nostr:\""
			}
		}
	}

	return false, ""
}
