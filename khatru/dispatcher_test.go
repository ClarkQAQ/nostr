package khatru

import (
	"slices"
	"strconv"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/require"
)

func TestDispatcherCandidates(t *testing.T) {
	d := newDispatcher()

	d.addSubscription(subscription{
		id: "...",
		filter: nostr.Filter{
			Kinds: []nostr.Kind{9},
			Tags:  nostr.TagMap{"h": []string{"aaa"}},
		},
	})
	d.addSubscription(subscription{
		id: "...",
		filter: nostr.Filter{
			Kinds: []nostr.Kind{11},
			Tags:  nostr.TagMap{"h": []string{"aaa"}},
		},
	})
	d.addSubscription(subscription{
		id: "...",
		filter: nostr.Filter{
			Kinds: []nostr.Kind{9, 11, 1111},
			Tags:  nostr.TagMap{"h": []string{"aaa"}},
		},
	})
	d.addSubscription(subscription{
		id: "...",
		filter: nostr.Filter{
			Kinds: []nostr.Kind{9, 11, 1111},
			Tags:  nostr.TagMap{"h": []string{"bbb"}},
		},
	})
	d.addSubscription(subscription{
		id: "...",
		filter: nostr.Filter{
			Kinds: []nostr.Kind{9, 11, 1111},
			Authors: []nostr.PubKey{
				nostr.MustPubKeyFromHex("87f5650744bed197fcb170ae05fd8d1948a24b2aac34cedf7bdb1c47d6d93273"),
			},
		},
	})

	matched := 0
	for range d.candidates(nostr.Event{
		PubKey:    nostr.MustPubKeyFromHex("87f5650744bed197fcb170ae05fd8d1948a24b2aac34cedf7bdb1c47d6d93273"),
		ID:        nostr.MustIDFromHex("87f5650744bed197fcb170ae05fd8d1948a24b2aac34cedf7bdb1c47d6d93273"),
		Kind:      9,
		CreatedAt: nostr.Now(),
		Content:   "hello",
		Tags: nostr.Tags{
			{"h", "aaa"},
		},
	}) {
		matched++
	}

	require.Equal(t, 3, matched)
}

func FuzzDispatcherCandidates(f *testing.F) {
	f.Add(1, 1, uint8(8), uint8(16))
	f.Add(2, 3, uint8(32), uint8(32))

	f.Fuzz(func(t *testing.T, seed int, advance int, ops uint8, checks uint8) {
		d := newDispatcher()
		state := fuzzState{value: seed, advance: advance}

		active := make(map[int]subscription)
		activeSSIDs := make([]int, 0, int(ops))
		nextSubID := 0

		steps := int(ops) + 1
		for range steps {
			if len(activeSSIDs) == 0 || state.next(10) != 0 {
				nextSubID++
				sub := subscription{
					id:     strconv.Itoa(nextSubID),
					filter: fuzzDispatcherFilter(&state),
				}

				ssid := d.addSubscription(sub)

				active[ssid] = sub
				activeSSIDs = append(activeSSIDs, ssid)
			} else {
				idx := state.next(len(activeSSIDs))
				ssid := activeSSIDs[idx]

				d.removeSubscription(ssid)

				delete(active, ssid)
				activeSSIDs = append(activeSSIDs[:idx], activeSSIDs[idx+1:]...)
			}

			for range int(checks%7) + 1 {
				event := fuzzDispatcherEvent(&state)

				expected := expectedDispatcherCandidates(active, event)
				actual := collectedDispatcherCandidates(&d, event)

				require.Equalf(t, expected, actual, "seed=%d advance=%d event=%s active=%v", seed, advance, event.String(), active)
			}
		}

		for _, ssid := range activeSSIDs {
			d.removeSubscription(ssid)
			delete(active, ssid)
		}

		require.Empty(t, collectedDispatcherCandidates(&d, fuzzDispatcherEvent(&state)))
	})
}

type fuzzState struct {
	value   int
	advance int
}

func (state *fuzzState) next(n int) int {
	if n <= 0 {
		return 0
	}
	value := state.value % n
	if value < 0 {
		value += n
	}
	state.value += state.advance
	return value
}

func fuzzDispatcherFilter(seed *fuzzState) nostr.Filter {
	filter := nostr.Filter{
		Authors: fuzzDispatcherAuthors(seed),
		Kinds:   fuzzDispatcherKinds(seed),
		Tags:    fuzzDispatcherTagMap(seed),
	}

	if seed.next(3) == 0 {
		since := nostr.Timestamp(seed.next(6))
		until := since + nostr.Timestamp(seed.next(6))
		filter.Since = since
		filter.Until = until
	} else if seed.next(4) == 0 {
		filter.Since = nostr.Timestamp(seed.next(8))
	} else if seed.next(4) == 0 {
		filter.Until = nostr.Timestamp(seed.next(8))
	}

	return filter
}

func fuzzDispatcherAuthors(seed *fuzzState) []nostr.PubKey {
	switch seed.next(4) {
	case 0:
		return nil
	case 1:
		return []nostr.PubKey{}
	}

	count := seed.next(3) + 1
	authors := make([]nostr.PubKey, 0, count)
	for range count {
		pk := nostr.PubKey{byte(seed.next(4) + 1)}
		if !slices.Contains(authors, pk) {
			authors = append(authors, pk)
		}
	}
	return authors
}

func fuzzDispatcherKinds(seed *fuzzState) []nostr.Kind {
	switch seed.next(4) {
	case 0:
		return nil
	case 1:
		return []nostr.Kind{}
	}

	count := seed.next(3) + 1
	kinds := make([]nostr.Kind, 0, count)
	for range count {
		kind := nostr.Kind(seed.next(5) + 1)
		if !slices.Contains(kinds, kind) {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

func fuzzDispatcherTagMap(seed *fuzzState) nostr.TagMap {
	if seed.next(3) == 0 {
		return nil
	}

	keys := []string{"e", "p", "t"}
	values := []string{"a", "b", "c", "d"}

	count := seed.next(3)
	if count == 0 {
		return nil
	}

	tags := make(nostr.TagMap, count)
	start := seed.next(len(keys))
	for i := range count {
		idx := (start + i) % len(keys)
		valueCount := seed.next(3) + 1
		entries := make([]string, 0, valueCount)
		for range valueCount {
			value := values[seed.next(len(values))]
			if !slices.Contains(entries, value) {
				entries = append(entries, value)
			}
		}
		tags[keys[idx]] = entries
	}

	return tags
}

func fuzzDispatcherEvent(seed *fuzzState) nostr.Event {
	tags := make(nostr.Tags, 0, seed.next(4))
	keys := []string{"e", "p", "t"}
	values := []string{"a", "b", "c", "d"}
	for range cap(tags) {
		tags = append(tags, nostr.Tag{keys[seed.next(len(keys))], values[seed.next(len(values))]})
	}

	return nostr.Event{
		PubKey:    nostr.PubKey{byte(seed.next(4) + 1)},
		Kind:      nostr.Kind(seed.next(5) + 1),
		CreatedAt: nostr.Timestamp(seed.next(8)),
		Tags:      tags,
	}
}

func expectedDispatcherCandidates(active map[int]subscription, event nostr.Event) []string {
	ids := make([]string, 0, len(active))
	for _, sub := range active {
		if sub.filter.Matches(event) {
			ids = append(ids, sub.id)
		}
	}
	slices.Sort(ids)
	return ids
}

func collectedDispatcherCandidates(d *dispatcher, event nostr.Event) []string {
	ids := make([]string, 0, d.subscriptions.Size())
	for sub := range d.candidates(event) {
		ids = append(ids, sub.id)
	}
	slices.Sort(ids)
	return ids
}
