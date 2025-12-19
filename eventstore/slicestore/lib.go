package slicestore

import (
	"bytes"
	"cmp"
	"fmt"
	"iter"
	"slices"
	"sync"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"fiatjaf.com/nostr/eventstore/internal"
)

var _ eventstore.Store = (*SliceStore)(nil)

type SliceStore struct {
	sync.Mutex
	internal []nostr.Event
}

func (b *SliceStore) Init() error {
	b.internal = make([]nostr.Event, 0, 5000)
	return nil
}

func (b *SliceStore) Close() {}

func (b *SliceStore) QueryEvents(filter nostr.Filter, maxLimit int) iter.Seq[nostr.Event] {
	return func(yield func(nostr.Event) bool) {
		if tlimit := filter.GetTheoreticalLimit(); tlimit == 0 {
			return
		} else if tlimit < maxLimit {
			maxLimit = tlimit
		}

		// efficiently determine where to start and end
		start := 0
		end := len(b.internal)
		if filter.Until != 0 {
			start, _ = slices.BinarySearchFunc(b.internal, filter.Until, eventTimestampComparator)
		}
		if filter.Since != 0 {
			end, _ = slices.BinarySearchFunc(b.internal, filter.Since, eventTimestampComparator)
		}

		// ham
		if end < start {
			return
		}

		count := 0
		for _, event := range b.internal[start:end] {
			if count == maxLimit {
				break
			}

			if filter.Matches(event) {
				if !yield(event) {
					return
				}
				count++
			}
		}
	}
}

func (b *SliceStore) CountEvents(filter nostr.Filter) (uint32, error) {
	var val uint32
	for _, event := range b.internal {
		if filter.Matches(event) {
			val++
		}
	}
	return val, nil
}

func (b *SliceStore) SaveEvent(evt nostr.Event) error {
	b.Lock()
	defer b.Unlock()

	return b.save(evt)
}

func (b *SliceStore) save(evt nostr.Event) error {
	idx, found := slices.BinarySearchFunc(b.internal, evt, eventComparator)
	if found {
		return eventstore.ErrDupEvent
	}
	// let's insert at the correct place in the array
	b.internal = append(b.internal, evt) // bogus
	copy(b.internal[idx+1:], b.internal[idx:])
	b.internal[idx] = evt

	return nil
}

func (b *SliceStore) DeleteEvent(id nostr.ID) error {
	b.Lock()
	defer b.Unlock()

	return b.delete(id)
}

func (b *SliceStore) delete(id nostr.ID) error {
	var idx int = -1
	for i, event := range b.internal {
		if event.ID == id {
			idx = i
			break
		}
	}

	if idx == -1 {
		// we don't have this event
		return nil
	}

	// we have it
	copy(b.internal[idx:], b.internal[idx+1:])
	b.internal = b.internal[0 : len(b.internal)-1]
	return nil
}

func (b *SliceStore) ReplaceEvent(evt nostr.Event) error {
	b.Lock()
	defer b.Unlock()

	filter := nostr.Filter{Limit: 1, Kinds: []nostr.Kind{evt.Kind}, Authors: []nostr.PubKey{evt.PubKey}}
	if evt.Kind.IsAddressable() {
		filter.Tags = nostr.TagMap{"d": []string{evt.Tags.GetD()}}
	}

	shouldStore := true
	for previous := range b.QueryEvents(filter, 1) {
		if internal.IsOlder(previous, evt) {
			if err := b.delete(previous.ID); err != nil {
				return fmt.Errorf("failed to delete event for replacing: %w", err)
			}
		} else {
			shouldStore = false
		}
	}

	if shouldStore {
		if err := b.save(evt); err != nil && err != eventstore.ErrDupEvent {
			return fmt.Errorf("failed to save: %w", err)
		}
	}

	return nil
}

func eventTimestampComparator(e nostr.Event, t nostr.Timestamp) int {
	return cmp.Compare(t, e.CreatedAt)
}

func eventComparator(a nostr.Event, b nostr.Event) int {
	v := cmp.Compare(b.CreatedAt, a.CreatedAt)
	if v == 0 {
		v = bytes.Compare(b.ID[:], a.ID[:])
	}
	return v
}
