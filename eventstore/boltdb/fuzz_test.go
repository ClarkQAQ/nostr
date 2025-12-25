package boltdb

import (
	"cmp"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/require"
)

func FuzzQuery(f *testing.F) {
	f.Add(uint(200), uint(50), uint(13), uint(2), uint(2), uint(0), uint(1))
	f.Fuzz(func(t *testing.T, total, limit, authors, timestampAuthorFactor, seedFactor, kinds, kindFactor uint) {
		total++
		authors++
		seedFactor++
		kindFactor++
		if kinds == 1 {
			kinds++
		}
		if limit == 0 {
			return
		}

		// ~ setup db
		if err := os.RemoveAll("/tmp/bolttest"); err != nil {
			t.Fatal(err)
			return
		}
		db := &BoltBackend{}
		db.Path = "/tmp/bolttest"
		if err := db.Init(context.Background()); err != nil {
			t.Fatal(err)
			return
		}
		defer db.Close()

		// ~ start actual test

		filter := nostr.Filter{
			Authors: make([]nostr.PubKey, authors),
			Limit:   int(limit),
		}
		var maxKind nostr.Kind = 1
		if kinds > 0 {
			filter.Kinds = make([]nostr.Kind, kinds)
			for i := range filter.Kinds {
				filter.Kinds[i] = nostr.Kind(int(kindFactor) * i)
			}
			maxKind = filter.Kinds[len(filter.Kinds)-1]
		}

		for i := 0; i < int(authors); i++ {
			var sk nostr.SecretKey
			binary.BigEndian.PutUint32(sk[:], uint32(i%int(authors*seedFactor))+1)
			pk := nostr.GetPublicKey(sk)
			filter.Authors[i] = pk
		}

		expected := make([]nostr.Event, 0, total)
		for i := 0; i < int(total); i++ {
			skseed := uint32(i%int(authors*seedFactor)) + 1
			sk := nostr.SecretKey{}
			binary.BigEndian.PutUint32(sk[:], skseed)

			evt := nostr.Event{
				CreatedAt: nostr.Timestamp(skseed)*nostr.Timestamp(timestampAuthorFactor) + nostr.Timestamp(i),
				Content:   fmt.Sprintf("unbalanced %d", i),
				Tags:      nostr.Tags{},
				Kind:      nostr.Kind(i) % maxKind,
			}
			err := evt.Sign(sk)
			require.NoError(t, err)

			err = db.SaveEvent(context.Background(), evt)
			require.NoError(t, err)

			if filter.Matches(evt) {
				expected = append(expected, evt)
			}
		}

		slices.SortFunc(expected, nostr.CompareEventReverse)
		if len(expected) > int(limit) {
			expected = expected[0:limit]
		}

		start := time.Now()

		res := slices.Collect(db.QueryEvents(context.Background(), filter, 500))
		end := time.Now()

		require.Equal(t, len(expected), len(res), "number of results is different than expected")
		require.Less(t, end.Sub(start).Milliseconds(), int64(1500), "query took too long")
		nresults := len(expected)

		getTimestamps := func(events []nostr.Event) []nostr.Timestamp {
			res := make([]nostr.Timestamp, len(events))
			for i, evt := range events {
				res[i] = evt.CreatedAt
			}
			return res
		}

		fmt.Println(" expected   result")
		for i := range expected {
			fmt.Println(" ", expected[i].CreatedAt, expected[i].ID.Hex()[0:8], "  ", res[i].CreatedAt, res[i].ID.Hex()[0:8], "           ", i)
		}

		require.Equal(t, expected[0].CreatedAt, res[0].CreatedAt, "first result is wrong")
		require.Equal(t, expected[nresults-1].CreatedAt, res[nresults-1].CreatedAt, "last result (%d) is wrong", nresults-1)
		require.Equal(t, getTimestamps(expected), getTimestamps(res))

		for _, evt := range res {
			require.True(t, filter.Matches(evt), "event %s doesn't match filter %s", evt, filter)
		}

		require.True(t, slices.IsSortedFunc(res, func(a, b nostr.Event) int { return cmp.Compare(b.CreatedAt, a.CreatedAt) }), "results are not sorted")
	})
}
