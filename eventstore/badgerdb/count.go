package badgerdb

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"slices"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/codec/betterbinary"
	"fiatjaf.com/nostr/eventstore/searcher"
	"github.com/dgraph-io/badger/v4"
)

func (b *BadgerBackend) CountEvents(ctx context.Context, filter nostr.Filter) (uint32, error) {
	var count int = 0

	queries, extraAuthors, extraKinds, extraTagKey, extraTagValues, since, e := b.prepareQueries(filter)
	if e != nil {
		return 0, fmt.Errorf("failed to prepare queries: %w", e)
	}

	var searchSearcher *searcher.Searcher
	if filter.Search != "" {
		searchSearcher = searcher.NewSearcher(filter.Search)
	}

	if e := b.DB.View(func(txn *badger.Txn) error {
		// actually iterate
		for _, q := range queries {
			opts := badger.DefaultIteratorOptions
			opts.Reverse = true
			opts.Prefix = q.prefix
			opts.PrefetchValues = (extraAuthors != nil || extraKinds != nil || extraTagValues != nil)

			it := txn.NewIterator(opts)
			defer it.Close()

			it.Seek(q.startingPoint)

			for it.Valid() {
				item := it.Item()
				key := item.Key()

				// check if still within prefix
				if !bytes.HasPrefix(key, q.prefix) {
					break
				}

				keyLen := len(key)
				if keyLen < 8+32 {
					it.Next()
					continue
				}

				createdAt := binary.BigEndian.Uint64(key[keyLen-8-32 : keyLen-32])
				if createdAt < since {
					break
				}

				if extraAuthors != nil || extraKinds != nil || extraTagValues != nil || filter.Search != "" {
					// fetch actual event
					id := key[keyLen-32:]
					rawKey := make([]byte, 1+32)
					rawKey[0] = prefixRaw[0]
					copy(rawKey[1:], id)

					rawItem, err := txn.Get(rawKey)
					if err != nil {
						it.Next()
						continue
					}

					bin, err := rawItem.ValueCopy(nil)
					if err != nil {
						it.Next()
						continue
					}

					// check it against pubkeys without decoding the entire thing
					if extraAuthors != nil && !slices.Contains(extraAuthors, betterbinary.GetPubKey(bin)) {
						it.Next()
						continue
					}

					// check it against kinds without decoding the entire thing
					if extraKinds != nil && !slices.Contains(extraKinds, betterbinary.GetKind(bin)) {
						it.Next()
						continue
					}

					evt := &nostr.Event{}
					if extraTagValues != nil || filter.Search != "" {
						if e := betterbinary.Unmarshal(bin, evt); e != nil {
							it.Next()
							continue
						}
					}

					if extraTagValues != nil && !evt.Tags.ContainsAny(extraTagKey, extraTagValues) {
						it.Next()
						continue
					}

					if searchSearcher != nil && !searchSearcher.Contains(evt.Content) {
						it.Next()
						continue
					}
				}

				count++

				if filter.Limit > 0 && count >= filter.Limit {
					break
				}

				it.Next()
			}
		}

		return nil
	}); e != nil {
		return 0, fmt.Errorf("failed to count events: %w", e)
	}

	return uint32(count), nil
}
