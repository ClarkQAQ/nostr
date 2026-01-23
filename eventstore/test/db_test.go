package test

import (
	"os"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"fiatjaf.com/nostr/eventstore/badgerdb"
	"fiatjaf.com/nostr/eventstore/boltdb"
	"fiatjaf.com/nostr/eventstore/pebbledb"
	"fiatjaf.com/nostr/eventstore/slicestore"
)

var (
	dbpath = "/tmp/eventstore-test"
	sk3    = nostr.MustSecretKeyFromHex("0000000000000000000000000000000000000000000000000000000000000003")
	sk4    = nostr.MustSecretKeyFromHex("0000000000000000000000000000000000000000000000000000000000000004")
)

var tests = []struct {
	name string
	run  func(*testing.T, eventstore.Store)
}{
	{"basic", basicTest},
	{"first", runFirstTestOn},
	{"second", runSecondTestOn},
	{"manyauthors", manyAuthorsTest},
	{"unbalanced", unbalancedTest},
	{"count", countTest},
}

func TestSliceStore(t *testing.T) {
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { test.run(t, &slicestore.SliceStore{}) })
	}
}

func TestBoltDB(t *testing.T) {
	for _, test := range tests {
		os.RemoveAll(dbpath + "boltdb")
		t.Run(test.name, func(t *testing.T) { test.run(t, &boltdb.BoltBackend{Path: dbpath + "boltdb"}) })
	}
}

func TestBadgerDB(t *testing.T) {
	for _, test := range tests {
		os.RemoveAll(dbpath + "badger")
		db, e := badgerdb.NewBadgerBackend(dbpath + "badger")
		if e != nil {
			t.Fatal(e)
		}

		t.Run(test.name, func(t *testing.T) { test.run(t, db) })
	}
}

func TestPebbleDB(t *testing.T) {
	for _, test := range tests {
		os.RemoveAll(dbpath + "pebble")
		db, e := pebbledb.NewPebbleBackend(dbpath + "pebble")
		if e != nil {
			t.Fatal(e)
		}

		t.Run(test.name, func(t *testing.T) {
			test.run(t, db)
			db.Close()
		})
	}
}
