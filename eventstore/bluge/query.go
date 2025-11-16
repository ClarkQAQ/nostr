package bluge

import (
	"context"
	"iter"
	"strconv"

	"fiatjaf.com/nostr"
	"github.com/blugelabs/bluge"
	"github.com/blugelabs/bluge/search"
)

func (b *BlugeBackend) QueryEvents(filter nostr.Filter, maxLimit int) iter.Seq[nostr.Event] {
	return func(yield func(nostr.Event) bool) {
		limit := maxLimit
		if filter.LimitZero {
			return
		} else if filter.Limit < limit {
			limit = filter.Limit
		}

		if len(filter.Search) < 2 {
			return
		}

		reader, err := b.writer.Reader()
		if err != nil {
			return
		}

		searchQ := bluge.NewMatchQuery(filter.Search)
		searchQ.SetField(contentField)
		var q bluge.Query = searchQ

		complicatedQuery := bluge.NewBooleanQuery().AddMust(searchQ)

		if len(filter.Kinds) > 0 {
			eitherKind := bluge.NewBooleanQuery()
			eitherKind.SetMinShould(1)
			for _, kind := range filter.Kinds {
				kindQ := bluge.NewTermQuery(strconv.Itoa(int(kind)))
				kindQ.SetField(kindField)
				eitherKind.AddShould(kindQ)
			}
			complicatedQuery.AddMust(eitherKind)
			q = complicatedQuery
		}

		if len(filter.Authors) > 0 {
			eitherPubkey := bluge.NewBooleanQuery()
			eitherPubkey.SetMinShould(1)
			for _, pubkey := range filter.Authors {
				if len(pubkey) != 64 {
					continue
				}
				pubkeyQ := bluge.NewTermQuery(pubkey.Hex()[56:])
				pubkeyQ.SetField(pubkeyField)
				eitherPubkey.AddShould(pubkeyQ)
			}
			complicatedQuery.AddMust(eitherPubkey)
			q = complicatedQuery
		}

		if filter.Since != 0 || filter.Until != 0 {
			min := 0.0
			if filter.Since != 0 {
				min = float64(filter.Since)
			}
			max := float64(nostr.Now())
			if filter.Until != 0 {
				max = float64(filter.Until)
			}
			dateRangeQ := bluge.NewNumericRangeInclusiveQuery(min, max, true, true)
			dateRangeQ.SetField(createdAtField)
			complicatedQuery.AddMust(dateRangeQ)
			q = complicatedQuery
		}

		req := bluge.NewTopNSearch(limit, q)

		dmi, err := reader.Search(context.Background(), req)
		if err != nil {
			reader.Close()
			return
		}

		defer reader.Close()

		var next *search.DocumentMatch
		for next, err = dmi.Next(); next != nil; next, err = dmi.Next() {
			_ = next.VisitStoredFields(func(field string, value []byte) bool {
				id, err := nostr.IDFromHex(string(value))
				if err == nil {
					for evt := range b.RawEventStore.QueryEvents(nostr.Filter{IDs: []nostr.ID{id}}, 1) {
						yield(evt)
					}
				}
				return false
			})
		}
		if err != nil {
			return
		}
	}
}
