package checks

import (
	"fiatjaf.com/nostr/eventstore"
	"fiatjaf.com/nostr/eventstore/boltdb"
	"fiatjaf.com/nostr/eventstore/lmdb"
	"fiatjaf.com/nostr/eventstore/mmm"
)

// compile-time checks to ensure all backends implement Store
var (
	_ eventstore.Store = (*lmdb.LMDBBackend)(nil)
	_ eventstore.Store = (*mmm.IndexingLayer)(nil)
	_ eventstore.Store = (*boltdb.BoltBackend)(nil)
)
