package slicestore

import (
	"context"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/require"
)

func TestBasicStuff(t *testing.T) {
	ss := &SliceStore{}
	ss.Init(context.Background())
	defer ss.Close()

	for i := 0; i < 20; i++ {
		v := i
		kind := 11
		if i%2 == 0 {
			v = i + 10000
		}
		if i%3 == 0 {
			kind = 12
		}
		evt := nostr.Event{CreatedAt: nostr.Timestamp(v), Kind: nostr.Kind(kind)}
		evt.Sign(nostr.Generate())
		ss.SaveEvent(context.Background(), evt)
	}

	list := make([]nostr.Event, 0, 20)
	for event := range ss.QueryEvents(context.Background(), nostr.Filter{}, 500) {
		list = append(list, event)
	}
	require.Len(t, list, 20)

	if list[0].CreatedAt != 10018 || list[1].CreatedAt != 10016 || list[18].CreatedAt != 3 || list[19].CreatedAt != 1 {
		t.Fatalf("order is incorrect")
	}

	list = make([]nostr.Event, 0, 7)
	for event := range ss.QueryEvents(context.Background(), nostr.Filter{Limit: 15, Until: nostr.Timestamp(9999), Kinds: []nostr.Kind{11}}, 500) {
		list = append(list, event)
	}
	if len(list) != 7 {
		t.Fatalf("should have gotten 7, not %d", len(list))
	}

	list = make([]nostr.Event, 0, 5)
	for event := range ss.QueryEvents(context.Background(), nostr.Filter{Since: nostr.Timestamp(10009)}, 500) {
		list = append(list, event)
	}
	require.Len(t, list, 5)
}
