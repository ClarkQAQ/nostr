package mmm

import (
	"os"
	"path/filepath"

	"fiatjaf.com/nostr/eventstore"
	"github.com/PowerDNS/lmdb-go/lmdb"
)

var _ eventstore.Store = (*IndexingLayer)(nil)

type IndexingLayer struct {
	isInitialized bool
	name          string

	mmmm *MultiMmapManager

	// this is stored in the knownLayers db as a value, and used to keep track of which layer owns each event
	id uint16

	lmdbEnv *lmdb.Env

	settings        lmdb.DBI
	indexCreatedAt  lmdb.DBI
	indexKind       lmdb.DBI
	indexPubkey     lmdb.DBI
	indexPubkeyKind lmdb.DBI
	indexTag        lmdb.DBI
	indexTag32      lmdb.DBI
	indexTagAddr    lmdb.DBI
	indexPTagKind   lmdb.DBI
}

type IndexingLayers []*IndexingLayer

func (ils IndexingLayers) ByID(ilid uint16) *IndexingLayer {
	for _, il := range ils {
		if il.id == ilid {
			return il
		}
	}
	return nil
}

const multiIndexCreationFlags uint = lmdb.Create | lmdb.DupSort

func (il *IndexingLayer) Init() error {
	if il.isInitialized {
		return nil
	}
	il.isInitialized = true

	path := filepath.Join(il.mmmm.Dir, il.name)

	// open lmdb
	env, err := lmdb.NewEnv()
	if err != nil {
		return err
	}

	env.SetMaxDBs(9)
	env.SetMaxReaders(1000)
	env.SetMapSize(MMAP_INFINITE_SIZE)

	// create directory if it doesn't exist and open it
	if err := os.MkdirAll(path, 0755); err != nil {
		return err
	}

	err = env.Open(path, lmdb.NoTLS, 0644)
	if err != nil {
		return err
	}
	il.lmdbEnv = env

	// open each db
	if err := il.lmdbEnv.Update(func(txn *lmdb.Txn) error {
		if dbi, err := txn.OpenDBI("settings", lmdb.Create); err != nil {
			return err
		} else {
			il.settings = dbi
		}
		if dbi, err := txn.OpenDBI("created_at", multiIndexCreationFlags); err != nil {
			return err
		} else {
			il.indexCreatedAt = dbi
		}
		if dbi, err := txn.OpenDBI("kind", multiIndexCreationFlags); err != nil {
			return err
		} else {
			il.indexKind = dbi
		}
		if dbi, err := txn.OpenDBI("pubkey", multiIndexCreationFlags); err != nil {
			return err
		} else {
			il.indexPubkey = dbi
		}
		if dbi, err := txn.OpenDBI("pubkeyKind", multiIndexCreationFlags); err != nil {
			return err
		} else {
			il.indexPubkeyKind = dbi
		}
		if dbi, err := txn.OpenDBI("tag", multiIndexCreationFlags); err != nil {
			return err
		} else {
			il.indexTag = dbi
		}
		if dbi, err := txn.OpenDBI("tag32", multiIndexCreationFlags); err != nil {
			return err
		} else {
			il.indexTag32 = dbi
		}
		if dbi, err := txn.OpenDBI("tagaddr", multiIndexCreationFlags); err != nil {
			return err
		} else {
			il.indexTagAddr = dbi
		}
		if dbi, err := txn.OpenDBI("ptagKind", multiIndexCreationFlags); err != nil {
			return err
		} else {
			il.indexPTagKind = dbi
		}
		return nil
	}); err != nil {
		return err
	}

	if err := il.migrate(); err != nil {
		return err
	}

	return nil
}

func (il *IndexingLayer) Name() string { return il.name }

func (il *IndexingLayer) Close() {
	il.lmdbEnv.Close()
}
