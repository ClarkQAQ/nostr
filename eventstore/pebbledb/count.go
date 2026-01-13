package pebbledb

import (
	"context"
	"slices"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/codec/betterbinary"
	"github.com/cockroachdb/pebble/v2"
)

// CountEvents counts all events matching the filter.
func (b *PebbleBackend) CountEvents(ctx context.Context, filter nostr.Filter) (uint32, error) {
	// Plan the query
	plans, extraAuthors, extraKinds, extraTagKey, extraTagValues, extraSearch, sinceTs, untilTs := b.planQuery(filter)

	// Use snapshot for consistent reads
	snap := b.DB.NewSnapshot()
	defer snap.Close()

	count := uint32(0)

	// Iterate over all plans
	for _, plan := range plans {
		iter, err := snap.NewIter(&pebble.IterOptions{
			LowerBound: plan.lowerBound,
			UpperBound: plan.upperBound,
		})
		if err != nil {
			continue
		}

		for iter.First(); iter.Valid(); iter.Next() {
			// Extract ID and timestamp from key
			id := extractIDFromIndexKey(iter.Key())
			ts := extractTimestampFromIndexKey(iter.Key())

			// Check timestamp bounds
			if sinceTs > 0 && ts < sinceTs {
				break // Keys are in reverse chronological order, so we can stop
			}
			if untilTs > 0 && ts > untilTs {
				continue
			}

			// If we need extra filtering, fetch the event
			if extraAuthors != nil || extraKinds != nil || extraTagValues != nil || extraSearch != nil {
				rawKey := makeRawKey(id)
				bin, closer, err := snap.Get(rawKey)
				if err != nil {
					continue
				}

				// Copy before closing
				binCopy := make([]byte, len(bin))
				copy(binCopy, bin)
				closer.Close()

				// Fast path: check pubkey/kind without full unmarshal
				if extraAuthors != nil {
					pk := betterbinary.GetPubKey(binCopy)
					if !slices.Contains(extraAuthors, pk) {
						continue
					}
				}
				if extraKinds != nil {
					k := betterbinary.GetKind(binCopy)
					if !slices.Contains(extraKinds, k) {
						continue
					}
				}

				// Need full unmarshal for tag/search checks
				if extraTagValues != nil || extraSearch != nil {
					var evt nostr.Event
					if err := betterbinary.Unmarshal(binCopy, &evt); err != nil {
						continue
					}

					if extraTagValues != nil && !evt.Tags.ContainsAny(extraTagKey, extraTagValues) {
						continue
					}

					if extraSearch != nil && !extraSearch.Contains(evt.Content) {
						continue
					}
				}
			}

			count++

			// Check limit
			if filter.Limit > 0 && int(count) >= filter.Limit {
				iter.Close()
				return count, nil
			}
		}

		iter.Close()
	}

	return count, nil
}
