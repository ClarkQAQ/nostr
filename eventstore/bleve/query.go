package bleve

import (
	"iter"
	"strconv"

	"fiatjaf.com/nostr"
	bleve "github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/query"
)

func (b *BleveBackend) QueryEvents(filter nostr.Filter, maxLimit int) iter.Seq[nostr.Event] {
	return func(yield func(nostr.Event) bool) {
		if tlimit := filter.GetTheoreticalLimit(); tlimit == 0 {
			return
		} else if tlimit < maxLimit {
			maxLimit = tlimit
		}

		if len(filter.Search) < 2 {
			return
		}

		searchQ := bleve.NewMatchQuery(filter.Search)
		searchQ.SetField(contentField)
		var q query.Query = searchQ

		conjQueries := []query.Query{searchQ}

		if len(filter.Kinds) > 0 {
			eitherKind := bleve.NewDisjunctionQuery()
			for _, kind := range filter.Kinds {
				kindQ := bleve.NewTermQuery(strconv.Itoa(int(kind)))
				kindQ.SetField(kindField)
				eitherKind.AddQuery(kindQ)
			}
			conjQueries = append(conjQueries, eitherKind)
		}

		if len(filter.Authors) > 0 {
			eitherPubkey := bleve.NewDisjunctionQuery()
			for _, pubkey := range filter.Authors {
				if len(pubkey) != 64 {
					continue
				}
				pubkeyQ := bleve.NewTermQuery(pubkey.Hex()[56:])
				pubkeyQ.SetField(pubkeyField)
				eitherPubkey.AddQuery(pubkeyQ)
			}
			conjQueries = append(conjQueries, eitherPubkey)
		}

		if filter.Since != 0 || filter.Until != 0 {
			var min *float64
			if filter.Since != 0 {
				minVal := float64(filter.Since)
				min = &minVal
			}
			var max *float64
			if filter.Until != 0 {
				maxVal := float64(filter.Until)
				max = &maxVal
			}
			dateRangeQ := bleve.NewNumericRangeInclusiveQuery(min, max, nil, nil)
			dateRangeQ.SetField(createdAtField)
			conjQueries = append(conjQueries, dateRangeQ)
		}

		if len(conjQueries) > 1 {
			q = bleve.NewConjunctionQuery(conjQueries...)
		}

		req := bleve.NewSearchRequest(q)
		req.Size = maxLimit
		req.From = 0

		result, err := b.index.Search(req)
		if err != nil {
			return
		}

		for _, hit := range result.Hits {
			id, err := nostr.IDFromHex(hit.ID)
			if err != nil {
				continue
			}
			for evt := range b.RawEventStore.QueryEvents(nostr.Filter{IDs: []nostr.ID{id}}, 1) {
				if !yield(evt) {
					return
				}
			}
		}
	}
}
