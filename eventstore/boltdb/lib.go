package boltdb

import (
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"go.etcd.io/bbolt"
)

var (
	settingsStore   = []byte("settingsStore")
	rawEventStore   = []byte("rawEventStore")
	indexCreatedAt  = []byte("indexCreatedAt")
	indexKind       = []byte("indexKind")
	indexPubkey     = []byte("indexPubkey")
	indexPubkeyKind = []byte("indexPubkeyKind")
	indexTag        = []byte("indexTag")
	indexTag32      = []byte("indexTag32")
	indexTagAddr    = []byte("indexTagAddr")
	hllCache        = []byte("hllCache")
)

var _ eventstore.Store = (*BoltBackend)(nil)

type BoltBackend struct {
	Path    string
	MapSize int64
	DB      *bbolt.DB

	ReadOnly bool

	EnableHLLCacheFor func(kind nostr.Kind) (useCache bool, skipSavingActualEvent bool)
}

func (b *BoltBackend) Init() error {
	db, err := bbolt.Open(b.Path, 0600, &bbolt.Options{
		Timeout:         2 * time.Second,
		PreLoadFreelist: true,
		FreelistType:    bbolt.FreelistMapType,
		ReadOnly:        b.ReadOnly,
	})
	if err != nil {
		return err
	}

	db.AllocSize = 64 * 1024 * 1024
	db.MaxBatchDelay = time.Millisecond * 40

	b.DB = db

	db.Update(func(txn *bbolt.Tx) error {
		if _, err := txn.CreateBucketIfNotExists(settingsStore); err != nil {
			return err
		}
		if _, err := txn.CreateBucketIfNotExists(rawEventStore); err != nil {
			return err
		}
		if _, err := txn.CreateBucketIfNotExists(indexCreatedAt); err != nil {
			return err
		}
		if _, err := txn.CreateBucketIfNotExists(indexKind); err != nil {
			return err
		}
		if _, err := txn.CreateBucketIfNotExists(indexPubkey); err != nil {
			return err
		}
		if _, err := txn.CreateBucketIfNotExists(indexPubkeyKind); err != nil {
			return err
		}
		if _, err := txn.CreateBucketIfNotExists(indexTag); err != nil {
			return err
		}
		if _, err := txn.CreateBucketIfNotExists(indexTag32); err != nil {
			return err
		}
		if _, err := txn.CreateBucketIfNotExists(indexTagAddr); err != nil {
			return err
		}
		if _, err := txn.CreateBucketIfNotExists(hllCache); err != nil {
			return err
		}
		return nil
	})

	return b.migrate()
}

func (b *BoltBackend) Close() {
	b.DB.Close()
}
