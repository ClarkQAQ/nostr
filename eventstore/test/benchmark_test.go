package test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"fiatjaf.com/nostr/eventstore/badgerdb"
	"fiatjaf.com/nostr/eventstore/boltdb"
	"fiatjaf.com/nostr/eventstore/slicestore"
)

func BenchmarkSliceStore(b *testing.B) {
	s := &slicestore.SliceStore{}
	if e := s.Init(context.Background()); e != nil {
		b.Fatal(e)
	}
	runBenchmarkOn(b, s)
}

func BenchmarkBoltDB(b *testing.B) {
	os.RemoveAll(dbpath + "boltdb")
	l := &boltdb.BoltBackend{Path: dbpath + "boltdb"}
	if e := l.Init(context.Background()); e != nil {
		b.Fatal(e)
	}

	runBenchmarkOn(b, l)
}

func BenchmarkBadgerDB(b *testing.B) {
	os.RemoveAll(dbpath + "badger")
	l, e := badgerdb.NewBadgerBackend(dbpath + "badger")
	if e != nil {
		b.Fatal(e)
	}
	if e := l.Init(context.Background()); e != nil {
		b.Fatal(e)
	}

	runBenchmarkOn(b, l)
}

func runBenchmarkOn(b *testing.B, db eventstore.Store) {
	for i := 0; i < 10000; i++ {
		eTag := make([]byte, 32)
		binary.BigEndian.PutUint16(eTag, uint16(i))

		ref := nostr.GetPublicKey(sk3)
		if i%3 == 0 {
			ref = nostr.GetPublicKey(sk4)
		}

		evt := nostr.Event{
			CreatedAt: nostr.Timestamp(i*10 + 2),
			Content:   fmt.Sprintf("hello %d", i),
			Tags: nostr.Tags{
				{"t", fmt.Sprintf("t%d", i)},
				{"e", nostr.HexEncodeToString(eTag)},
				{"p", ref.Hex()},
			},
			Kind: nostr.Kind(i % 10),
		}
		sk := sk3
		if i%3 == 0 {
			sk = sk4
		}
		if e := evt.Sign(sk); e != nil {
			b.Fatal(e)
		}
		if e := db.SaveEvent(context.Background(), evt); e != nil {
			b.Fatal(e)
		}
	}

	filters := make([]nostr.Filter, 0, 10)
	filters = append(filters, nostr.Filter{Kinds: []nostr.Kind{1, 4, 8, 16}})
	pk3 := nostr.GetPublicKey(sk3)
	filters = append(filters, nostr.Filter{Authors: []nostr.PubKey{pk3, nostr.Generate().Public()}})
	filters = append(filters, nostr.Filter{Authors: []nostr.PubKey{pk3, nostr.Generate().Public()}, Kinds: []nostr.Kind{3, 4}})
	filters = append(filters, nostr.Filter{})
	filters = append(filters, nostr.Filter{Limit: 20})
	filters = append(filters, nostr.Filter{Kinds: []nostr.Kind{8, 9}, Tags: nostr.TagMap{"p": []string{pk3.Hex()}}})
	pk4 := nostr.GetPublicKey(sk4)
	filters = append(filters, nostr.Filter{Kinds: []nostr.Kind{8, 9}, Tags: nostr.TagMap{"p": []string{pk3.Hex(), pk4.Hex()}}})
	filters = append(filters, nostr.Filter{Kinds: []nostr.Kind{8, 9}, Tags: nostr.TagMap{"p": []string{pk3.Hex(), pk4.Hex()}}})
	eTags := make([]string, 20)
	for i := 0; i < 20; i++ {
		eTag := make([]byte, 32)
		binary.BigEndian.PutUint16(eTag, uint16(i))
		eTags[i] = nostr.HexEncodeToString(eTag)
	}
	filters = append(filters, nostr.Filter{Kinds: []nostr.Kind{9}, Tags: nostr.TagMap{"e": eTags}})
	filters = append(filters, nostr.Filter{Kinds: []nostr.Kind{5}, Tags: nostr.TagMap{"e": eTags, "t": []string{"t5"}}})
	filters = append(filters, nostr.Filter{Tags: nostr.TagMap{"e": eTags}})
	filters = append(filters, nostr.Filter{Tags: nostr.TagMap{"e": eTags}, Limit: 50})

	b.Run("filter", func(b *testing.B) {
		for q, filter := range filters {
			b.Run(fmt.Sprintf("q-%d", q), func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					_ = db.QueryEvents(context.Background(), filter, 500)
				}
			})
		}
	})

	b.Run("insert", func(b *testing.B) {
		evt := nostr.Event{Kind: 788, CreatedAt: nostr.Now(), Content: "blergh", Tags: nostr.Tags{{"t", "spam"}}}
		if e := evt.Sign(sk4); e != nil {
			b.Fatal(e)
		}
		for i := 0; i < b.N; i++ {
			if e := db.SaveEvent(context.Background(), evt); e != nil && !errors.Is(e, eventstore.ErrDupEvent) {
				b.Fatal(e)
			}
		}
	})
}
