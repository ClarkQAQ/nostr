package pebbledb

import (
	"context"

	"fiatjaf.com/nostr/eventstore"
	"github.com/cockroachdb/pebble/v2"
)

// Key prefixes for different index types.
// All index keys end with: <inverted_ts:8><id:32> for efficient reverse chronological iteration.
//
// Key Schema Design (optimized for Pebble's LSM-tree):
//
//	Raw event storage:
//	  r<id:32> -> binary event data
//
//	Indexes (keys-only, values are empty):
//	  c<inverted_ts:8><id:32>                         - by created_at (global timeline)
//	  k<kind:2><inverted_ts:8><id:32>                 - by kind
//	  p<pubkey:32><inverted_ts:8><id:32>              - by pubkey (author)
//	  x<pubkey:32><kind:2><inverted_ts:8><id:32>      - by pubkey+kind (compound)
//	  t<tag_letter:1><tag_value:32><inverted_ts:8><id:32>   - 32-byte hex tags (e, p, etc)
//	  a<tag_letter:1><kind:2><pubkey:32><d:30><inverted_ts:8><id:32> - addressable refs
//	  m<tag_letter:1><md5:16><inverted_ts:8><id:32>   - md5 hashed tags (general)
//
// Key design rationale:
//   - inverted_ts = MaxUint64 - timestamp: enables forward iteration in reverse chronological order
//   - All indexes end with id for uniqueness and to support deduplication
//   - Using 2-byte kind (uint16) instead of 8 bytes saves space since Kind is uint16
//   - Tag indexes include tag letter for namespacing different tag types
var (
	prefixRaw        = []byte{'r'} // raw event: r<id32> -> event binary
	prefixCreatedAt  = []byte{'c'} // by created_at: c<inverted_ts8><id32>
	prefixKind       = []byte{'k'} // by kind: k<kind2><inverted_ts8><id32>
	prefixPubkey     = []byte{'p'} // by pubkey: p<pk32><inverted_ts8><id32>
	prefixPubkeyKind = []byte{'x'} // by pubkey+kind: x<pk32><kind2><inverted_ts8><id32>
	prefixTag32      = []byte{'t'} // 32-byte hex tags: t<letter1><tagVal32><inverted_ts8><id32>
	prefixTagAddr    = []byte{'a'} // addressable refs: a<letter1><kind2><pk32><d30><inverted_ts8><id32>
	prefixTag        = []byte{'m'} // md5 hashed tags: m<letter1><md516><inverted_ts8><id32>
)

var _ eventstore.Store = (*PebbleBackend)(nil)

// PebbleBackend implements eventstore.Store using CockroachDB's Pebble.
type PebbleBackend struct {
	Path string
	DB   *pebble.DB

	// MaxOpenFiles limits file descriptors. 0 = default (1000).
	MaxOpenFiles int

	// CacheSize in bytes for block cache. 0 = default (128MB).
	CacheSize int64

	// MemTableSize in bytes. 0 = default (4MB).
	MemTableSize uint64

	// DisableWAL disables write-ahead log for maximum write performance.
	// WARNING: Data may be lost on crash.
	DisableWAL bool

	// Options allows full customization of Pebble options.
	Options func(*pebble.Options)

	// internal: shared write options
	writeOpts *pebble.WriteOptions
	syncOpts  *pebble.WriteOptions
}

// NewPebbleBackend creates a new PebbleBackend with the given path.
// Call Init() to open the database.
func NewPebbleBackend(path string) (*PebbleBackend, error) {
	return &PebbleBackend{Path: path}, nil
}

func (b *PebbleBackend) Init(ctx context.Context) error {
	cache := pebble.NewCache(128 << 20) // 128MB default
	if b.CacheSize > 0 {
		cache = pebble.NewCache(b.CacheSize)
	}
	defer cache.Unref()

	opts := &pebble.Options{
		Cache: cache,

		// Disable logging
		Logger:          &NoopLoggerAndTracer{},
		LoggerAndTracer: &NoopLoggerAndTracer{},

		// Memory table configuration
		MemTableSize:                4 << 20, // 4MB default
		MemTableStopWritesThreshold: 16,

		// L0 compaction thresholds - balanced for mixed workload
		L0CompactionThreshold: 4,
		L0StopWritesThreshold: 12,

		// Base level size
		LBaseMaxBytes: 64 << 20, // 64MB

		// Use newest format for best performance
		FormatMajorVersion: pebble.FormatNewest,

		// Disable WAL if configured (dangerous but fast)
		DisableWAL: b.DisableWAL,
	}

	if b.MemTableSize > 0 {
		opts.MemTableSize = b.MemTableSize
	}

	if b.MaxOpenFiles > 0 {
		opts.MaxOpenFiles = b.MaxOpenFiles
	}

	// Allow user customization
	if b.Options != nil {
		b.Options(opts)
	}

	// opts = GetHighPerformanceOptions()

	db, err := pebble.Open(b.Path, opts)
	if err != nil {
		return err
	}

	b.DB = db

	// Pre-create write options
	b.writeOpts = &pebble.WriteOptions{Sync: false}
	b.syncOpts = &pebble.WriteOptions{Sync: true}

	return nil
}

func (b *PebbleBackend) Close() {
	if b.DB != nil {
		b.DB.Close()
		b.DB = nil
	}
}

type NoopLoggerAndTracer struct{}

var _ pebble.LoggerAndTracer = NoopLoggerAndTracer{}

func (l NoopLoggerAndTracer) Infof(format string, args ...interface{}) {}

func (l NoopLoggerAndTracer) Errorf(format string, args ...interface{}) {}

func (l NoopLoggerAndTracer) Fatalf(format string, args ...interface{}) {}

func (l NoopLoggerAndTracer) Eventf(ctx context.Context, format string, args ...interface{}) {
}

func (l NoopLoggerAndTracer) IsTracingEnabled(ctx context.Context) bool {
	return false
}
