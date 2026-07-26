package eventstore

import (
	"iter"
	"slices"

	"fiatjaf.com/nostr"
)

// CollectEvents collects all events from a Seq2, discarding errors.
func CollectEvents(seq iter.Seq2[nostr.Event, error]) []nostr.Event {
	return slices.Collect(func(yield func(nostr.Event) bool) {
		for evt := range seq {
			yield(evt)
		}
	})
}

// EventsOnly converts an iter.Seq2[nostr.Event, error] to iter.Seq[nostr.Event], discarding errors.
func EventsOnly(seq iter.Seq2[nostr.Event, error]) iter.Seq[nostr.Event] {
	return func(yield func(nostr.Event) bool) {
		for evt := range seq {
			if !yield(evt) {
				return
			}
		}
	}
}
