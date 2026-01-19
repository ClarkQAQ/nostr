package test

import (
	"context"
	"os"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"fiatjaf.com/nostr/eventstore/boltdb"
	"fiatjaf.com/nostr/eventstore/lmdb"
	"fiatjaf.com/nostr/eventstore/mmm"
	"fiatjaf.com/nostr/eventstore/slicestore"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

var (
	dbpath = "/tmp/eventstore-test"
	sk3    = nostr.MustSecretKeyFromHex("0000000000000000000000000000000000000000000000000000000000000003")
	sk4    = nostr.MustSecretKeyFromHex("0000000000000000000000000000000000000000000000000000000000000004")
)

var ctx = context.Background()

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

func TestLMDB(t *testing.T) {
	for _, test := range tests {
		os.RemoveAll(dbpath + "lmdb")
		t.Run(test.name, func(t *testing.T) { test.run(t, &lmdb.LMDBBackend{Path: dbpath + "lmdb"}) })
	}
}

func TestBoltDB(t *testing.T) {
	for _, test := range tests {
		os.RemoveAll(dbpath + "boltdb")
		t.Run(test.name, func(t *testing.T) { test.run(t, &boltdb.BoltBackend{Path: dbpath + "boltdb"}) })
	}
}

func TestMMM(t *testing.T) {
	for _, test := range tests {
		os.RemoveAll(dbpath + "mmm")
		t.Run(test.name, func(t *testing.T) {
			logger := zerolog.Nop()

			mmmm := &mmm.MultiMmapManager{
				Dir:    dbpath + "mmm",
				Logger: &logger,
			}

			err := mmmm.Init()
			require.NoError(t, err)

			il, err := mmmm.EnsureLayer("test")
			require.NoError(t, err)

			test.run(t, il)
		})
	}
}
