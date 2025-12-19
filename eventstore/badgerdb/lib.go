package badgerdb

import (
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"github.com/dgraph-io/badger/v4"
)

// Key prefixes for different index types
var (
	prefixRaw        = []byte{'r'} // raw event storage: r:<idPtr8>
	prefixCreatedAt  = []byte{'c'} // by created_at: c:<ts4>:<idPtr8>
	prefixKind       = []byte{'k'} // by kind: k:<kind2>:<ts4>:<idPtr8>
	prefixPubkey     = []byte{'p'} // by pubkey: p:<pk8>:<ts4>:<idPtr8>
	prefixPubkeyKind = []byte{'x'} // by pubkey+kind: x:<pk8>:<kind2>:<ts4>:<idPtr8>
	prefixTag32      = []byte{'t'} // 32-byte hex tags: t:<letter1>:<tagVal8>:<ts4>:<idPtr8>
	prefixTagAddr    = []byte{'a'} // addressable refs: a:<letter1>:<kind2>:<pk8>:<d30>:<ts4>:<idPtr8>
	prefixTag        = []byte{'m'} // md5 hashed tags: m:<letter1>:<md516>:<ts4>:<idPtr8>
)

var _ eventstore.Store = (*BadgerBackend)(nil)

type BadgerBackend struct {
	Path string
	DB   *badger.DB

	EnableHLLCacheFor func(kind nostr.Kind) (useCache bool, skipSavingActualEvent bool)
}

// NewBadgerBackend creates a new BadgerBackend with the given path.
// Call Init() to open the database.
func NewBadgerBackend(path string) (*BadgerBackend, error) {
	return &BadgerBackend{Path: path}, nil
}

func (b *BadgerBackend) Init() error {
	opts := badger.DefaultOptions(b.Path)

	// Performance optimizations
	opts.Logger = nil                 // disable logging for performance
	opts.SyncWrites = false           // async writes for better performance
	opts.NumVersionsToKeep = 1        // we only need latest version
	opts.CompactL0OnClose = true      // compact on close
	opts.NumLevelZeroTables = 5       // default
	opts.NumLevelZeroTablesStall = 15 // default
	opts.ValueLogFileSize = 256 << 20 // 256MB value log files
	opts.NumMemtables = 5             // number of memtables
	opts.MemTableSize = 64 << 20      // 64MB memtable size
	opts.BlockCacheSize = 256 << 20   // 256MB block cache
	opts.IndexCacheSize = 128 << 20   // 128MB index cache
	opts.DetectConflicts = false      // disable conflict detection for speed

	db, err := badger.Open(opts)
	if err != nil {
		return err
	}

	b.DB = db

	// Start a background goroutine to run value log GC periodically
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if b.DB == nil {
				return
			}
			// Try to GC the value log
		again:
			err := b.DB.RunValueLogGC(0.5)
			if err == nil {
				goto again
			}
		}
	}()

	return nil
}

func (b *BadgerBackend) Close() {
	if b.DB != nil {
		b.DB.Close()
		b.DB = nil
	}
}
