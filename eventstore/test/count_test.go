package test

import (
	"context"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"github.com/stretchr/testify/require"
)

func countTest(t *testing.T, db eventstore.Store) {
	ctx := context.Background()

	err := db.Init(ctx)
	require.NoError(t, err)

	pk3 := nostr.GetPublicKey(sk3)
	pk4 := nostr.GetPublicKey(sk4)

	// create test events for counting
	events := []nostr.Event{
		{
			CreatedAt: 700,
			Content:   "count test event 1",
			Kind:      1,
			PubKey:    pk3,
			Tags: nostr.Tags{
				{"t", "plec"},
				{"g", "bbbbbb"},
				{"r", "https://z.com"},
			},
		},
		{
			CreatedAt: 701,
			Content:   "count test event 2",
			Kind:      1,
			PubKey:    pk3,
			Tags: nostr.Tags{
				{"t", "plec"},
				{"g", "aaaaaa"},
				{"r", "https://z.com"},
			},
		},
		{
			CreatedAt: 702,
			Content:   "count test event 3",
			Kind:      2,
			PubKey:    pk4,
			Tags: nostr.Tags{
				{"t", "test"},
			},
		},
	}

	// sign and save events
	for _, evt := range events {
		if evt.PubKey == pk3 {
			evt.Sign(sk3)
		} else {
			evt.Sign(sk4)
		}
		err = db.SaveEvent(ctx, evt)
		require.NoError(t, err)
	}

	// test count all events
	count, err := db.CountEvents(ctx, nostr.Filter{})
	require.NoError(t, err)
	require.Equal(t, uint32(3), count, "should count the 3 new events")

	// test count by kind 1
	count, err = db.CountEvents(ctx, nostr.Filter{
		Kinds: []nostr.Kind{1},
	})
	require.NoError(t, err)
	require.Equal(t, uint32(2), count, "should count 2 events of kind 1")

	// test count by author
	count, err = db.CountEvents(ctx, nostr.Filter{
		Authors: []nostr.PubKey{pk3},
	})
	require.NoError(t, err)
	require.Equal(t, uint32(2), count, "should count 2 events from sk3")

	// test count by author and tag
	count, err = db.CountEvents(ctx, nostr.Filter{
		Authors: []nostr.PubKey{pk4},
		Tags:    nostr.TagMap{"t": []string{"test"}},
	})
	require.NoError(t, err)
	require.Equal(t, uint32(1), count, "should count 1 event")

	count, err = db.CountEvents(ctx, nostr.Filter{
		Authors: []nostr.PubKey{pk4},
		Tags:    nostr.TagMap{"t": []string{"somethingelse"}},
	})
	require.NoError(t, err)
	require.Equal(t, uint32(0), count, "should count 0 events")

	count, err = db.CountEvents(ctx, nostr.Filter{
		Authors: []nostr.PubKey{pk3},
		Tags:    nostr.TagMap{"t": []string{"test"}},
	})
	require.NoError(t, err)
	require.Equal(t, uint32(0), count, "should count 0 events")

	// test double tag
	count, err = db.CountEvents(ctx, nostr.Filter{
		Kinds: []nostr.Kind{1, 2},
		Tags:  nostr.TagMap{"t": []string{"plec"}, "g": []string{"aaaaaa"}},
	})
	require.NoError(t, err)
	require.Equal(t, uint32(1), count, "should count 1 event")

	count, err = db.CountEvents(ctx, nostr.Filter{
		Tags: nostr.TagMap{"t": []string{"plec"}, "g": []string{"aaaaaa"}},
	})
	require.NoError(t, err)
	require.Equal(t, uint32(1), count, "should count 1 event")

	count, err = db.CountEvents(ctx, nostr.Filter{
		Kinds: []nostr.Kind{1},
		Tags:  nostr.TagMap{"t": []string{"plec"}, "g": []string{"aaaaaa"}},
	})
	require.NoError(t, err)
	require.Equal(t, uint32(1), count, "should count 1 event")

	count, err = db.CountEvents(ctx, nostr.Filter{
		Kinds: []nostr.Kind{1},
		Tags:  nostr.TagMap{"t": []string{"plec"}, "r": []string{"https://z.com"}},
	})
	require.NoError(t, err)
	require.Equal(t, uint32(2), count, "should count 2 events")

	count, err = db.CountEvents(ctx, nostr.Filter{
		Tags: nostr.TagMap{"t": []string{"plec"}, "r": []string{"https://z.com"}},
	})
	require.NoError(t, err)
	require.Equal(t, uint32(2), count, "should count 2 events")

	count, err = db.CountEvents(ctx, nostr.Filter{
		Kinds: []nostr.Kind{1, 2},
		Tags:  nostr.TagMap{"t": []string{"plec"}, "r": []string{"https://z.com"}},
	})
	require.NoError(t, err)
	require.Equal(t, uint32(2), count, "should count 2 events")

	count, err = db.CountEvents(ctx, nostr.Filter{
		Tags: nostr.TagMap{"t": []string{"banana"}, "r": []string{"https://z.com"}},
	})
	require.NoError(t, err)
	require.Equal(t, uint32(0), count, "should count 0 events")
}
