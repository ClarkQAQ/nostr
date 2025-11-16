package test

import (
	"encoding/binary"
	"fmt"
	"slices"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"github.com/stretchr/testify/require"
)

func manyAuthorsTest(t *testing.T, db eventstore.Store) {
	_ = db.Init()

	const total = 10000
	const limit = 500
	const authors = 1700
	kinds := []nostr.Kind{6, 7, 8}

	bigfilter := nostr.Filter{
		Authors: make([]nostr.PubKey, authors),
		Kinds:   kinds,
		Limit:   limit,
	}
	for i := 0; i < authors; i++ {
		sk := make([]byte, 32)
		binary.BigEndian.PutUint32(sk, uint32(i%(total/5))+1)
		pk := nostr.GetPublicKey([32]byte(sk))
		bigfilter.Authors[i] = pk
	}

	ordered := make([]nostr.Event, 0, total)
	for i := 0; i < total; i++ {
		sk := make([]byte, 32)
		binary.BigEndian.PutUint32(sk, uint32(i%(total/5))+1)

		evt := nostr.Event{
			CreatedAt: nostr.Timestamp(i*i) / 4,
			Content:   fmt.Sprintf("lots of stuff %d", i),
			Tags:      nostr.Tags{},
			Kind:      nostr.Kind(i % 10),
		}
		err := evt.Sign([32]byte(sk))
		require.NoError(t, err)

		err = db.SaveEvent(evt)
		require.NoError(t, err)

		if bigfilter.Matches(evt) {
			ordered = append(ordered, evt)
		}
	}

	res := slices.Collect(db.QueryEvents(bigfilter, 500))
	require.Len(t, res, limit)
	require.True(t, slices.IsSortedFunc(res, nostr.CompareEventReverse))
	slices.SortFunc(ordered, nostr.CompareEventReverse)
	require.Equal(t, ordered[0], res[0])
	require.Equal(t, ordered[limit-1], res[limit-1])
	require.Equal(t, ordered[0:limit], res)
}
