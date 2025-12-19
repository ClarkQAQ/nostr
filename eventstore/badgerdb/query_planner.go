package badgerdb

import (
	"encoding/binary"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/internal"
)

type query struct {
	i             int
	prefix        []byte
	startingPoint []byte
}

func (b *BadgerBackend) prepareQueries(filter nostr.Filter) (
	queries []query,
	extraAuthors []nostr.PubKey,
	extraKinds []nostr.Kind,
	extraTagKey string,
	extraTagValues []string,
	since uint32,
	err error,
) {
	// we will apply this to every query we return
	defer func() {
		if queries == nil {
			return
		}

		var until uint32 = 4294967295
		if filter.Until != 0 {
			if fu := uint32(filter.Until); fu < until {
				until = fu
			}
		}
		for i, q := range queries {
			sp := make([]byte, len(q.prefix)+4+8) // prefix + ts + idPtr
			copy(sp, q.prefix)
			binary.BigEndian.PutUint32(sp[len(q.prefix):len(q.prefix)+4], until)
			// fill idPtr with 0xFF to get the largest possible key for reverse iteration
			for j := len(q.prefix) + 4; j < len(sp); j++ {
				sp[j] = 0xFF
			}
			queries[i].startingPoint = sp
		}
	}()

	// this is where we'll end the iteration
	if filter.Since != 0 {
		if fs := uint32(filter.Since); fs > since {
			since = fs
		}
	}

	if len(filter.Tags) > 0 {
		// we will select ONE tag to query for and ONE extra tag to do further narrowing, if available
		tagKey, tagValues, goodness := internal.ChooseNarrowestTag(filter)

		// we won't use a tag index for this as long as we have something else to match with
		if goodness < 2 && (len(filter.Authors) > 0 || len(filter.Kinds) > 0) {
			goto pubkeyMatching
		}

		// otherwise we will use a plain tag index
		queries = make([]query, len(tagValues))
		for i, value := range tagValues {
			// get key prefix (with full length) and offset where to write the created_at
			_, k := b.getTagIndexPrefix(tagKey, value)
			// remove the last parts to get just the prefix we want here (remove ts4 and idPtr8)
			queryPrefix := k[0 : len(k)-8-4]
			queries[i] = query{i: i, prefix: queryPrefix}
		}

		// add an extra kind filter if available (only do this on plain tag index, not on ptag-kind index)
		if filter.Kinds != nil {
			extraKinds = make([]nostr.Kind, len(filter.Kinds))
			copy(extraKinds, filter.Kinds)
		}

		// add an extra author search if possible
		if filter.Authors != nil {
			extraAuthors = make([]nostr.PubKey, len(filter.Authors))
			copy(extraAuthors, filter.Authors)
		}

		// add an extra useless tag if available
		filter.Tags = internal.CopyMapWithoutKey(filter.Tags, tagKey)
		if len(filter.Tags) > 0 {
			extraTagKey, extraTagValues, _ = internal.ChooseNarrowestTag(filter)
		}

		return queries, extraAuthors, extraKinds, extraTagKey, extraTagValues, since, nil
	}

pubkeyMatching:
	if len(filter.Authors) > 0 {
		if len(filter.Kinds) == 0 {
			// will use pubkey index
			// p:<pk8>
			queries = make([]query, len(filter.Authors))
			for i, pk := range filter.Authors {
				prefix := make([]byte, 1+8)
				prefix[0] = prefixPubkey[0]
				copy(prefix[1:1+8], pk[0:8])
				queries[i] = query{i: i, prefix: prefix}
			}
		} else {
			// will use pubkeyKind index
			// x:<pk8>:<kind2>
			queries = make([]query, len(filter.Authors)*len(filter.Kinds))
			i := 0
			for _, pk := range filter.Authors {
				for _, kind := range filter.Kinds {
					prefix := make([]byte, 1+8+2)
					prefix[0] = prefixPubkeyKind[0]
					copy(prefix[1:1+8], pk[0:8])
					binary.BigEndian.PutUint16(prefix[1+8:1+8+2], uint16(kind))
					queries[i] = query{i: i, prefix: prefix}
					i++
				}
			}
		}

		// potentially with an extra useless tag filtering
		extraTagKey, extraTagValues, _ = internal.ChooseNarrowestTag(filter)
		return queries, nil, nil, extraTagKey, extraTagValues, since, nil
	}

	if len(filter.Kinds) > 0 {
		// will use a kind index
		// k:<kind2>
		queries = make([]query, len(filter.Kinds))
		for i, kind := range filter.Kinds {
			prefix := make([]byte, 1+2)
			prefix[0] = prefixKind[0]
			binary.BigEndian.PutUint16(prefix[1:1+2], uint16(kind))
			queries[i] = query{i: i, prefix: prefix}
		}

		// potentially with an extra useless tag filtering
		tagKey, tagValues, _ := internal.ChooseNarrowestTag(filter)
		return queries, nil, nil, tagKey, tagValues, since, nil
	}

	// if we got here our query will have nothing to filter with
	// c: (just the created_at prefix)
	queries = make([]query, 1)
	prefix := make([]byte, 1)
	prefix[0] = prefixCreatedAt[0]
	queries[0] = query{i: 0, prefix: prefix}
	return queries, nil, nil, "", nil, since, nil
}
