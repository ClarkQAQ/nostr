package test

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"github.com/stretchr/testify/require"
)

func runFirstTestOn(t *testing.T, db eventstore.Store) {
	err := db.Init(context.Background())
	require.NoError(t, err)

	allEvents := make([]nostr.Event, 0, 10)

	// insert
	for i := 0; i < 10; i++ {
		evt := nostr.Event{
			CreatedAt: nostr.Timestamp(i*10 + 2),
			Content:   fmt.Sprintf("hello %d", i),
			Tags: nostr.Tags{
				{"t", fmt.Sprintf("%d", i)},
				{"e", "0" + strconv.Itoa(i) + strings.Repeat("0", 62)},
			},
			Kind: 1,
		}
		sk := sk3
		if i%3 == 0 {
			sk = sk4
		}
		if i%2 == 0 {
			evt.Kind = 9
		}
		_ = evt.Sign(sk)
		allEvents = append(allEvents, evt)
		err = db.SaveEvent(context.Background(), evt)
		require.NoError(t, err)
	}

	// query
	{
		results := slices.Collect(db.QueryEvents(context.Background(), nostr.Filter{}, 500))
		require.Len(t, results, len(allEvents))
		require.ElementsMatch(t,
			allEvents,
			results,
			"open-ended query results error")
	}

	{
		for i := 0; i < 10; i++ {
			results := slices.Collect(db.QueryEvents(context.Background(), nostr.Filter{Since: nostr.Timestamp(i*10 + 1)}, 500))
			require.ElementsMatch(t,
				allEvents[i:],
				results,
				"since query results error %d", i)
		}
	}

	{
		results := slices.Collect(db.QueryEvents(context.Background(), nostr.Filter{IDs: []nostr.ID{allEvents[7].ID, allEvents[9].ID}}, 500))
		require.Len(t, results, 2)
		require.ElementsMatch(t,
			[]nostr.Event{allEvents[7], allEvents[9]},
			results,
			"id query error")
	}

	{
		results := slices.Collect(db.QueryEvents(context.Background(), nostr.Filter{Kinds: []nostr.Kind{1}}, 500))
		require.ElementsMatch(t,
			[]nostr.Event{allEvents[1], allEvents[3], allEvents[5], allEvents[7], allEvents[9]},
			results,
			"kind query error")
	}

	{
		results := slices.Collect(db.QueryEvents(context.Background(), nostr.Filter{Kinds: []nostr.Kind{9}}, 500))
		require.ElementsMatch(t,
			[]nostr.Event{allEvents[0], allEvents[2], allEvents[4], allEvents[6], allEvents[8]},
			results,
			"second kind query error")
	}

	{
		pk4 := nostr.GetPublicKey(sk4)
		results := slices.Collect(db.QueryEvents(context.Background(), nostr.Filter{Authors: []nostr.PubKey{pk4}}, 500))
		require.ElementsMatch(t,
			[]nostr.Event{allEvents[0], allEvents[3], allEvents[6], allEvents[9]},
			results,
			"pubkey query error")
	}

	{
		pk3 := nostr.GetPublicKey(sk3)
		results := slices.Collect(db.QueryEvents(context.Background(), nostr.Filter{Kinds: []nostr.Kind{9}, Authors: []nostr.PubKey{pk3}}, 500))
		require.ElementsMatch(t,
			[]nostr.Event{allEvents[2], allEvents[4], allEvents[8]},
			results,
			"pubkey kind query error")
	}

	{
		pk3 := nostr.GetPublicKey(sk3)
		pk4 := nostr.GetPublicKey(sk4)
		results := slices.Collect(db.QueryEvents(context.Background(), nostr.Filter{Kinds: []nostr.Kind{9, 5, 7}, Authors: []nostr.PubKey{pk3, pk4}}, 500))
		require.ElementsMatch(t,
			[]nostr.Event{allEvents[0], allEvents[2], allEvents[4], allEvents[6], allEvents[8]},
			results,
			"2 pubkeys and kind query error")
	}

	{
		results := slices.Collect(db.QueryEvents(context.Background(), nostr.Filter{Tags: nostr.TagMap{"t": []string{"2", "4", "6"}}}, 500))
		require.ElementsMatch(t,
			[]nostr.Event{allEvents[2], allEvents[4], allEvents[6]},
			results,
			"tag query error")
	}

	// delete
	require.NoError(t, db.DeleteEvent(context.Background(), allEvents[4].ID), "delete 1 error")
	require.NoError(t, db.DeleteEvent(context.Background(), allEvents[5].ID), "delete 2 error")

	// query again
	{
		results := slices.Collect(db.QueryEvents(context.Background(), nostr.Filter{}, 500))
		require.ElementsMatch(t,
			slices.Concat(allEvents[0:4], allEvents[6:]),
			results,
			"second open-ended query error")
	}

	{
		results := slices.Collect(db.QueryEvents(context.Background(), nostr.Filter{Tags: nostr.TagMap{"t": []string{"2", "6"}}}, 500))
		require.ElementsMatch(t,
			[]nostr.Event{allEvents[2], allEvents[6]},
			results,
			"second tag query error")
	}

	{
		results := slices.Collect(db.QueryEvents(context.Background(), nostr.Filter{Tags: nostr.TagMap{"e": []string{allEvents[3].Tags[1][1]}}}, 500))
		require.ElementsMatch(t,
			[]nostr.Event{allEvents[3]},
			results,
			"'e' tag query error")
	}

	{
		for i := 0; i < 4; i++ {
			results := slices.Collect(db.QueryEvents(context.Background(), nostr.Filter{Until: nostr.Timestamp(i*10 + 1)}, 500))

			require.ElementsMatch(t,
				allEvents[:i],
				results,
				"until query results error %d", i)
		}
	}

	// test p-tag querying
	{
		p := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
		p2 := "2eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"

		newEvents := []nostr.Event{
			{Tags: nostr.Tags{nostr.Tag{"p", p}}, Kind: 1984, CreatedAt: nostr.Timestamp(100), Content: "first"},
			{Tags: nostr.Tags{nostr.Tag{"p", p}, nostr.Tag{"t", "x"}}, Kind: 1984, CreatedAt: nostr.Timestamp(101), Content: "middle"},
			{Tags: nostr.Tags{nostr.Tag{"p", p}}, Kind: 1984, CreatedAt: nostr.Timestamp(102), Content: "last"},
			{Tags: nostr.Tags{nostr.Tag{"p", p}}, Kind: 1111, CreatedAt: nostr.Timestamp(101), Content: "bulufas"},
			{Tags: nostr.Tags{nostr.Tag{"p", p}}, Kind: 1111, CreatedAt: nostr.Timestamp(102), Content: "safulub"},
			{Tags: nostr.Tags{nostr.Tag{"p", p}}, Kind: 1, CreatedAt: nostr.Timestamp(103), Content: "bololo"},
			{Tags: nostr.Tags{nostr.Tag{"p", p2}}, Kind: 1, CreatedAt: nostr.Timestamp(104), Content: "wololo"},
			{Tags: nostr.Tags{nostr.Tag{"p", p}, nostr.Tag{"p", p2}}, Kind: 1, CreatedAt: nostr.Timestamp(104), Content: "trololo"},
		}

		sk := nostr.Generate()
		for i := range newEvents {
			_ = newEvents[i].Sign(sk)
			require.NoError(t, db.SaveEvent(context.Background(), newEvents[i]))
		}

		{
			results := slices.Collect(db.QueryEvents(context.Background(), nostr.Filter{Tags: nostr.TagMap{"p": []string{p}}, Kinds: []nostr.Kind{1984}, Limit: 2}, 500))
			require.ElementsMatch(t,
				[]nostr.Event{newEvents[2], newEvents[1]},
				results,
				"'p' tag 1 query error",
			)
		}

		{
			results := slices.Collect(db.QueryEvents(context.Background(), nostr.Filter{Tags: nostr.TagMap{"p": []string{p}, "t": []string{"x"}}, Limit: 4}, 500))
			require.ElementsMatch(t,
				// the results won't be in canonical time order because this query is too awful, needs a kind
				[]nostr.Event{newEvents[1]},
				results,
				"'p' tag 2 query error")
		}

		{
			results := slices.Collect(db.QueryEvents(context.Background(), nostr.Filter{Tags: nostr.TagMap{"p": []string{p, p2}}, Kinds: []nostr.Kind{1}, Limit: 4}, 500))
			for _, idx := range []int{5, 6, 7} {
				require.True(t,
					slices.ContainsFunc(
						results,
						func(evt nostr.Event) bool { return evt.ID == newEvents[idx].ID },
					),
					"'p' tag 3 query error")
			}
		}
	}
}
