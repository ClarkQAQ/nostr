package pebbledb

import (
	"context"
	"encoding/hex"
	"fmt"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
)

func newTestBackend(t *testing.T) eventstore.Store {
	backend, e := NewPebbleBackend(t.TempDir())
	if e != nil {
		t.Fatalf("failed to open backend: %v", e)
	}

	if e := backend.Init(context.Background()); e != nil {
		t.Fatalf("failed to init backend: %v", e)
	}

	return backend
}

func makeEvent(kind int, key nostr.SecretKey, createdAt int64, tags nostr.Tags, content string) nostr.Event {
	evt := nostr.Event{
		PubKey:    key.Public(),
		CreatedAt: nostr.Timestamp(createdAt),
		Kind:      nostr.Kind(kind),
		Tags:      tags,
		Content:   content,
	}

	if e := evt.Sign(key); e != nil {
		panic(fmt.Errorf("failed to sign event: %w", e))
	}

	return evt
}

func TestCombinedQueries(t *testing.T) {
	b := newTestBackend(t)
	defer b.Close()

	k1 := nostr.Generate()
	k2 := nostr.Generate()

	e1 := makeEvent(1, k1, 1000, nostr.Tags{{"t", "bitcoin"}}, "e1")
	e2 := makeEvent(1, k1, 2000, nostr.Tags{{"t", "nostr"}}, "e2")
	e3 := makeEvent(1, k2, 3000, nostr.Tags{{"t", "bitcoin"}}, "e3")
	e4 := makeEvent(2, k1, 4000, nostr.Tags{{"t", "bitcoin"}}, "e4")

	for _, e := range []nostr.Event{e1, e2, e3, e4} {
		if err := b.SaveEvent(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name   string
		filter nostr.Filter
		want   int
	}{
		{
			name:   "pk1 AND k1",
			filter: nostr.Filter{Authors: []nostr.PubKey{e1.PubKey}, Kinds: []nostr.Kind{1}},
			want:   2,
		},
		{
			name:   "pk1 AND bitcoin",
			filter: nostr.Filter{Authors: []nostr.PubKey{e1.PubKey}, Tags: nostr.TagMap{"t": []string{"bitcoin"}}},
			want:   2,
		},
		{
			name:   "k1 AND bitcoin",
			filter: nostr.Filter{Kinds: []nostr.Kind{1}, Tags: nostr.TagMap{"t": []string{"bitcoin"}}},
			want:   2,
		},
		{
			name:   "pk1 AND k1 AND bitcoin",
			filter: nostr.Filter{Authors: []nostr.PubKey{e1.PubKey}, Kinds: []nostr.Kind{1}, Tags: nostr.TagMap{"t": []string{"bitcoin"}}},
			want:   1,
		},
		{
			name:   "pk1 OR pk2",
			filter: nostr.Filter{Authors: []nostr.PubKey{e1.PubKey, e3.PubKey}},
			want:   4,
		},
		{
			name:   "k1 OR k2",
			filter: nostr.Filter{Kinds: []nostr.Kind{1, 2}},
			want:   4,
		},
		{
			name:   "bitcoin OR nostr",
			filter: nostr.Filter{Tags: nostr.TagMap{"t": []string{"bitcoin", "nostr"}}},
			want:   4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count := 0
			for range b.QueryEvents(context.Background(), tt.filter, 100) {
				count++
			}
			if count != tt.want {
				t.Errorf("got %d, want %d", count, tt.want)
			}
		})
	}
}

func TestTimeRangeQueries(t *testing.T) {
	b := newTestBackend(t)
	defer b.Close()

	k := nostr.Generate()

	for i := 0; i < 10; i++ {
		ts := int64(100 + i*10)
		evt := makeEvent(1, k, ts, nil, fmt.Sprintf("val-%d", i))
		if e := b.SaveEvent(context.Background(), evt); e != nil {
			t.Fatalf("failed to save event: %v", e)
		}
	}

	tests := []struct {
		since int64
		until int64
		want  int
	}{
		{since: 0, until: 0, want: 10},
		{since: 150, until: 0, want: 5},
		{since: 0, until: 140, want: 5},
		{since: 130, until: 160, want: 4},
	}

	for _, tt := range tests {
		f := nostr.Filter{
			Authors: []nostr.PubKey{k.Public()},
		}
		if tt.since > 0 {
			f.Since = nostr.Timestamp(tt.since)
		}
		if tt.until > 0 {
			f.Until = nostr.Timestamp(tt.until)
		}

		count := 0
		for range b.QueryEvents(context.Background(), f, 100) {
			count++
		}
		if count != tt.want {
			t.Errorf("range %d-%d: got %d, want %d", tt.since, tt.until, count, tt.want)
		}
	}
}

func TestComplexTagQueries(t *testing.T) {
	b := newTestBackend(t)
	defer b.Close()

	k := nostr.Generate()

	e1 := makeEvent(1, k, 100, nostr.Tags{{"x", "a"}, {"y", "b"}, {"z", "c"}}, "")
	e2 := makeEvent(1, k, 200, nostr.Tags{{"x", "a"}, {"y", "c"}}, "")
	e3 := makeEvent(1, k, 300, nostr.Tags{{"x", "z"}}, "")

	if e := b.SaveEvent(context.Background(), e1); e != nil {
		t.Fatalf("failed to save event: %v", e)
	}
	if e := b.SaveEvent(context.Background(), e2); e != nil {
		t.Fatalf("failed to save event: %v", e)
	}
	if e := b.SaveEvent(context.Background(), e3); e != nil {
		t.Fatalf("failed to save event: %v", e)
	}

	f1 := nostr.Filter{Tags: nostr.TagMap{"x": []string{"a"}, "y": []string{"b"}, "z": []string{"c"}}}
	count := 0
	for range b.QueryEvents(context.Background(), f1, 100) {
		count++
	}
	if count != 1 {
		t.Errorf("multi-tag AND failed, got %d", count)
	}

	f2 := nostr.Filter{Tags: nostr.TagMap{"x": []string{"a"}}}
	count = 0
	for range b.QueryEvents(context.Background(), f2, 100) {
		count++
	}
	if count != 2 {
		t.Errorf("single tag query failed, got %d", count)
	}
}

func TestCountEvents(t *testing.T) {
	b := newTestBackend(t)
	defer b.Close()

	k := nostr.Generate()

	for i := 0; i < 50; i++ {
		if e := b.SaveEvent(context.Background(), makeEvent(1, k, int64(i), nil, "")); e != nil {
			t.Fatalf("failed to save event: %v", e)
		}
	}

	c, err := b.CountEvents(context.Background(), nostr.Filter{Kinds: []nostr.Kind{1}})
	if err != nil {
		t.Fatal(err)
	}
	if c != 50 {
		t.Errorf("count all: got %d", c)
	}

	c2, _ := b.CountEvents(context.Background(), nostr.Filter{Kinds: []nostr.Kind{1}, Limit: 20})
	if c2 != 20 {
		t.Errorf("count limit: got %d", c2)
	}
}

func TestDeleteEvent(t *testing.T) {
	b := newTestBackend(t)
	defer b.Close()

	k := nostr.Generate()

	evt := makeEvent(1, k, 100, nil, "del")
	if e := b.SaveEvent(context.Background(), evt); e != nil {
		t.Fatalf("failed to save event: %v", e)
	}

	id, _ := hex.DecodeString(evt.ID.Hex())
	var targetID nostr.ID
	copy(targetID[:], id)

	found := false
	for range b.QueryEvents(context.Background(), nostr.Filter{IDs: []nostr.ID{targetID}}, 100) {
		found = true
	}
	if !found {
		t.Fatal("saved event not found")
	}

	if err := b.DeleteEvent(context.Background(), targetID); err != nil {
		t.Fatal(err)
	}

	found = false
	for range b.QueryEvents(context.Background(), nostr.Filter{IDs: []nostr.ID{targetID}}, 100) {
		found = true
	}
	if found {
		t.Error("event still exists after delete")
	}

	c, _ := b.CountEvents(context.Background(), nostr.Filter{IDs: []nostr.ID{targetID}})
	if c != 0 {
		t.Error("count should be 0 after delete")
	}
}

func TestAddressableReplace(t *testing.T) {
	b := newTestBackend(t)
	defer b.Close()

	k := nostr.Generate()
	dTag := "my-identifier"

	e1 := makeEvent(30001, k, 1000, nostr.Tags{{"d", dTag}}, "ver1")
	e2 := makeEvent(30001, k, 2000, nostr.Tags{{"d", dTag}}, "ver2")
	e3 := makeEvent(30001, k, 3000, nostr.Tags{{"d", "other"}}, "other")

	if e := b.ReplaceEvent(context.Background(), e1); e != nil {
		t.Errorf("expected nil, got %v", e)
	}
	if e := b.ReplaceEvent(context.Background(), e3); e != nil {
		t.Errorf("expected nil, got %v", e)
	}

	count, _ := b.CountEvents(context.Background(), nostr.Filter{Authors: []nostr.PubKey{e1.PubKey}, Kinds: []nostr.Kind{30001}})
	if count != 2 {
		t.Errorf("expected 2 distinct parameterized events, got %d", count)
	}

	if e := b.ReplaceEvent(context.Background(), e2); e != nil {
		t.Errorf("expected nil, got %v", e)
	}

	events := []nostr.Event{}
	for ev := range b.QueryEvents(context.Background(), nostr.Filter{Tags: nostr.TagMap{"d": []string{dTag}}}, 10) {
		events = append(events, ev)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event after replace, got %d", len(events))
	}
	if events[0].Content != "ver2" {
		t.Errorf("expected content 'ver2', got '%s'", events[0].Content)
	}
}

func TestMaxLimit(t *testing.T) {
	b := newTestBackend(t)
	defer b.Close()

	k := nostr.Generate()

	for i := 0; i < 20; i++ {
		if e := b.SaveEvent(context.Background(), makeEvent(1, k, int64(100+i), nil, "")); e != nil {
			t.Fatalf("failed to save event: %v", e)
		}
	}

	count := 0
	for range b.QueryEvents(context.Background(), nostr.Filter{Kinds: []nostr.Kind{1}}, 5) {
		count++
	}
	if count != 5 {
		t.Errorf("expected maxLimit 5 to apply, got %d", count)
	}

	count = 0
	f := nostr.Filter{Kinds: []nostr.Kind{1}, Limit: 10}
	for range b.QueryEvents(context.Background(), f, 100) {
		count++
	}
	if count != 10 {
		t.Errorf("expected filter limit 10, got %d", count)
	}
}

func TestDuplicateEvent(t *testing.T) {
	b := newTestBackend(t)
	defer b.Close()

	k := nostr.Generate()
	evt := makeEvent(1, k, 100, nil, "test")

	if err := b.SaveEvent(context.Background(), evt); err != nil {
		t.Fatalf("first save failed: %v", err)
	}

	err := b.SaveEvent(context.Background(), evt)
	if err != eventstore.ErrDupEvent {
		t.Errorf("expected ErrDupEvent, got %v", err)
	}
}
