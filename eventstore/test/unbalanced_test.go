package test

import (
	"context"
	"encoding/binary"
	"fmt"
	"slices"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"github.com/stretchr/testify/require"
)

// this is testing what happens when most results come from the same abstract query -- but not all
func unbalancedTest(t *testing.T, db eventstore.Store) {
	_ = db.Init(context.Background())

	const total = 10000
	const limit = 160
	const authors = 1400

	bigfilter := nostr.Filter{
		Authors: make([]nostr.PubKey, authors),
		Limit:   limit,
	}
	for i := 0; i < authors; i++ {
		sk := make([]byte, 32)
		binary.BigEndian.PutUint32(sk, uint32(i%(authors*2))+1)
		pk := nostr.GetPublicKey([32]byte(sk))
		bigfilter.Authors[i] = pk
	}
	// fmt.Println("filter", bigfilter)

	expected := make([]nostr.Event, 0, total)
	for i := 0; i < total; i++ {
		skseed := uint32(i%(authors*2)) + 1
		sk := make([]byte, 32)
		binary.BigEndian.PutUint32(sk, skseed)

		evt := nostr.Event{
			CreatedAt: nostr.Timestamp(skseed)*1000 + nostr.Timestamp(i),
			Content:   fmt.Sprintf("unbalanced %d", i),
			Tags:      nostr.Tags{},
			Kind:      1,
		}
		err := evt.Sign([32]byte(sk))
		require.NoError(t, err)

		err = db.SaveEvent(context.Background(), evt)
		require.NoError(t, err)

		if bigfilter.Matches(evt) {
			expected = append(expected, evt)
		}
	}

	slices.SortFunc(expected, nostr.CompareEventReverse)
	if len(expected) > limit {
		expected = expected[0:limit]
	}
	require.Len(t, expected, limit)

	res := eventstore.CollectEvents(db.QueryEvents(context.Background(), bigfilter, 500))

	require.Equal(t, limit, len(res))
	require.True(t, slices.IsSortedFunc(res, nostr.CompareEventReverse))
	require.Equal(t, expected[0], res[0])

	// fmt.Println(" expected   result")
	// ets := getTimestamps(expected)
	// rts := getTimestamps(res)
	// for i := range ets {
	// 	fmt.Println(" ", ets[i], "  ", rts[i], "           ", i)
	// }

	require.Equal(t, expected[limit-1], res[limit-1])
	require.Equal(t, expected[0:limit], res)
}
