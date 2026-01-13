package pebbledb

import (
	"context"
	"encoding/binary"
	"iter"
	"log"
	"slices"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/codec/betterbinary"
	"fiatjaf.com/nostr/eventstore/internal"
	"fiatjaf.com/nostr/eventstore/searcher"
	"github.com/cockroachdb/pebble/v2"
)

// queryPlan represents a planned query with bounds.
type queryPlan struct {
	index      int
	lowerBound []byte // Inclusive lower bound for iteration
	upperBound []byte // Exclusive upper bound for iteration
}

// iterState tracks the state of an index iterator.
type iterState struct {
	iter      *pebble.Iterator
	plan      queryPlan
	exhausted bool
	currentID nostr.ID
	currentTs nostr.Timestamp
}

// QueryEvents returns events matching the filter in reverse chronological order.
func (b *PebbleBackend) QueryEvents(ctx context.Context, filter nostr.Filter, maxLimit int) iter.Seq[nostr.Event] {
	return func(yield func(nostr.Event) bool) {
		// Handle ID-based queries specially (direct lookup)
		if filter.IDs != nil {
			b.queryByIDs(filter.IDs, yield)
			return
		}

		// Calculate effective limit
		if tlimit := filter.GetTheoreticalLimit(); tlimit == 0 {
			return
		} else if tlimit < maxLimit {
			maxLimit = tlimit
		}

		// Plan and execute the query
		if err := b.query(filter, maxLimit, yield); err != nil {
			log.Printf("pebble: query error: %s\n", err)
		}
	}
}

// queryByIDs fetches events directly by their IDs.
func (b *PebbleBackend) queryByIDs(ids []nostr.ID, yield func(nostr.Event) bool) {
	for _, id := range ids {
		rawKey := makeRawKey(id)
		bin, closer, err := b.DB.Get(rawKey)
		if err != nil {
			continue
		}

		// Copy before closing
		binCopy := make([]byte, len(bin))
		copy(binCopy, bin)
		closer.Close()

		var evt nostr.Event
		if err := betterbinary.Unmarshal(binCopy, &evt); err != nil {
			continue
		}

		if !yield(evt) {
			return
		}
	}
}

// query executes a planned query against the database.
func (b *PebbleBackend) query(filter nostr.Filter, limit int, yield func(nostr.Event) bool) error {
	// Plan the query
	plans, extraAuthors, extraKinds, extraTagKey, extraTagValues, extraSearch, sinceTs, untilTs := b.planQuery(filter)

	// Use snapshot for consistent reads
	snap := b.DB.NewSnapshot()
	defer snap.Close()

	// Create iterators for each query plan
	iters := make([]*iterState, 0, len(plans))
	defer func() {
		for _, it := range iters {
			if it.iter != nil {
				it.iter.Close()
			}
		}
	}()

	// Initialize iterators
	for _, plan := range plans {
		piter, err := snap.NewIter(&pebble.IterOptions{
			LowerBound: plan.lowerBound,
			UpperBound: plan.upperBound,
		})
		if err != nil {
			continue
		}

		it := &iterState{iter: piter, plan: plan}

		// Position at first valid entry
		if !piter.First() {
			piter.Close()
			continue
		}

		// Extract initial position
		it.currentID = extractIDFromIndexKey(piter.Key())
		it.currentTs = extractTimestampFromIndexKey(piter.Key())

		iters = append(iters, it)
	}

	if len(iters) == 0 {
		return nil
	}

	// Track seen IDs to avoid duplicates
	emitted := 0

	// Main iteration loop - merge results from all iterators
	for len(iters) > 0 && (limit == 0 || emitted < limit) {
		// Find iterator with newest timestamp
		bestIdx := 0
		for i := 1; i < len(iters); i++ {
			if iters[i].currentTs > iters[bestIdx].currentTs {
				bestIdx = i
			}
		}

		best := iters[bestIdx]
		id := best.currentID
		ts := best.currentTs

		// Advance the best iterator
		if !best.iter.Next() {
			best.exhausted = true
		} else {
			best.currentID = extractIDFromIndexKey(best.iter.Key())
			best.currentTs = extractTimestampFromIndexKey(best.iter.Key())
		}

		// Check timestamp bounds
		if sinceTs > 0 && ts < sinceTs {
			best.exhausted = true
		}
		if untilTs > 0 && ts > untilTs {
			// Skip this event but don't exhaust iterator
			if best.exhausted {
				iters = removeIter(iters, bestIdx)
			}
			continue
		}

		// Remove exhausted iterators
		if best.exhausted {
			iters = removeIter(iters, bestIdx)
		}

		// Fetch the actual event
		rawKey := makeRawKey(id)
		bin, closer, err := snap.Get(rawKey)
		if err != nil {
			continue
		}

		// Fast path: check pubkey/kind without full unmarshal
		if extraAuthors != nil {
			pk := betterbinary.GetPubKey(bin)
			if !slices.Contains(extraAuthors, pk) {
				closer.Close()
				continue
			}
		}
		if extraKinds != nil {
			k := betterbinary.GetKind(bin)
			if !slices.Contains(extraKinds, k) {
				closer.Close()
				continue
			}
		}

		// Full unmarshal for remaining checks
		var evt nostr.Event
		if err := betterbinary.Unmarshal(bin, &evt); err != nil {
			closer.Close()
			continue
		}

		closer.Close()

		// Check extra tag filter
		if extraTagValues != nil && !evt.Tags.ContainsAny(extraTagKey, extraTagValues) {
			continue
		}

		// Check search filter
		if extraSearch != nil && !extraSearch.Contains(evt.Content) {
			continue
		}

		// Emit the event
		if !yield(evt) {
			return nil
		}
		emitted++
	}

	return nil
}

// planQuery creates query plans based on the filter.
// Returns: plans, extraAuthors, extraKinds, extraTagKey, extraTagValues, since, until
func (b *PebbleBackend) planQuery(filter nostr.Filter) (
	plans []queryPlan,
	extraAuthors []nostr.PubKey,
	extraKinds []nostr.Kind,
	extraTagKey string,
	extraTagValues []string,
	extraSearch *searcher.Searcher,
	since nostr.Timestamp,
	until nostr.Timestamp,
) {
	// Set time bounds
	since = filter.Since
	until = filter.Until

	// With inverted timestamps: newer events have SMALLER inverted timestamps
	// Key ordering: newest (smallest inverted_ts) ... oldest (largest inverted_ts)
	//
	// For forward iteration to yield events newest-first:
	// - Start at inverted(until) if until is set, otherwise start at 0 (newest possible)
	// - End at inverted(since) if since is set, otherwise end at maxTimestamp (oldest possible)
	//
	// LowerBound is INCLUSIVE, UpperBound is EXCLUSIVE
	// So: LowerBound = inverted(until), UpperBound = inverted(since) + 1 (or use prefix increment)

	var invertedLower uint64 = 0            // Default: start from newest (inverted_ts = 0)
	var invertedUpper uint64 = maxTimestamp // Default: end at oldest (inverted_ts = max)

	if until > 0 {
		invertedLower = invertTimestamp(until)
	}
	if since > 0 {
		invertedUpper = invertTimestamp(since)
	}

	if filter.Search != "" {
		extraSearch = searcher.NewSearcher(filter.Search)
	}

	// Try tag-based query first
	if len(filter.Tags) > 0 {
		tagKey, tagValues, goodness := internal.ChooseNarrowestTag(filter)

		// Use tag index if it's selective enough or we have nothing better
		if goodness >= 2 || (len(filter.Authors) == 0 && len(filter.Kinds) == 0) {
			plans = make([]queryPlan, len(tagValues))
			for i, value := range tagValues {
				prefix := makeTagQueryPrefix(tagKey, value)
				plans[i] = queryPlan{
					index:      i,
					lowerBound: appendTimestampBound(prefix, invertedLower),
					upperBound: appendUpperBound(prefix, invertedUpper),
				}
			}

			if filter.Kinds != nil {
				extraKinds = slices.Clone(filter.Kinds)
			}
			if filter.Authors != nil {
				extraAuthors = slices.Clone(filter.Authors)
			}

			remainingTags := internal.CopyMapWithoutKey(filter.Tags, tagKey)
			if len(remainingTags) > 0 {
				extraTagKey, extraTagValues, _ = internal.ChooseNarrowestTag(nostr.Filter{Tags: remainingTags})
			}

			return
		}
	}

	if len(filter.Authors) > 0 {
		if len(filter.Kinds) == 0 {
			plans = make([]queryPlan, len(filter.Authors))
			for i, pk := range filter.Authors {
				prefix := make([]byte, 1+32)
				prefix[0] = prefixPubkey[0]
				copy(prefix[1:], pk[:])
				plans[i] = queryPlan{
					index:      i,
					lowerBound: appendTimestampBound(prefix, invertedLower),
					upperBound: appendUpperBound(prefix, invertedUpper),
				}
			}
		} else {
			plans = make([]queryPlan, len(filter.Authors)*len(filter.Kinds))
			idx := 0
			for _, pk := range filter.Authors {
				for _, kind := range filter.Kinds {
					prefix := make([]byte, 1+32+2)
					prefix[0] = prefixPubkeyKind[0]
					copy(prefix[1:1+32], pk[:])
					binary.BigEndian.PutUint16(prefix[1+32:], uint16(kind))
					plans[idx] = queryPlan{
						index:      idx,
						lowerBound: appendTimestampBound(prefix, invertedLower),
						upperBound: appendUpperBound(prefix, invertedUpper),
					}
					idx++
				}
			}
		}

		if len(filter.Tags) > 0 {
			extraTagKey, extraTagValues, _ = internal.ChooseNarrowestTag(filter)
		}

		return
	}

	if len(filter.Kinds) > 0 {
		plans = make([]queryPlan, len(filter.Kinds))
		for i, kind := range filter.Kinds {
			prefix := make([]byte, 1+2)
			prefix[0] = prefixKind[0]
			binary.BigEndian.PutUint16(prefix[1:], uint16(kind))
			plans[i] = queryPlan{
				index:      i,
				lowerBound: appendTimestampBound(prefix, invertedLower),
				upperBound: appendUpperBound(prefix, invertedUpper),
			}
		}

		if len(filter.Tags) > 0 {
			extraTagKey, extraTagValues, _ = internal.ChooseNarrowestTag(filter)
		}

		return
	}

	plans = make([]queryPlan, 1)
	prefix := make([]byte, 1)
	prefix[0] = prefixCreatedAt[0]
	plans[0] = queryPlan{
		index:      0,
		lowerBound: appendTimestampBound(prefix, invertedLower),
		upperBound: appendUpperBound(prefix, invertedUpper),
	}

	return
}

func appendTimestampBound(prefix []byte, invertedTs uint64) []byte {
	result := make([]byte, len(prefix)+8)
	copy(result, prefix)
	binary.BigEndian.PutUint64(result[len(prefix):], invertedTs)
	return result
}

func appendUpperBound(prefix []byte, invertedTs uint64) []byte {
	if invertedTs == maxTimestamp {
		return incrementPrefix(prefix)
	}
	result := make([]byte, len(prefix)+8)
	copy(result, prefix)
	binary.BigEndian.PutUint64(result[len(prefix):], invertedTs+1)
	return result
}

// removeIter removes an iterator from the slice efficiently.
func removeIter(iters []*iterState, idx int) []*iterState {
	if iters[idx].iter != nil {
		iters[idx].iter.Close()
		iters[idx].iter = nil
	}
	// Swap with last element and truncate
	iters[idx] = iters[len(iters)-1]
	return iters[:len(iters)-1]
}
