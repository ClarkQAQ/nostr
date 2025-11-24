package checks

import (
	"fiatjaf.com/nostr/eventstore"
	"fiatjaf.com/nostr/eventstore/bleve"
	"fiatjaf.com/nostr/eventstore/boltdb"
)

// compile-time checks to ensure all backends implement Store
var (
	_ eventstore.Store = (*boltdb.BoltBackend)(nil)
	_ eventstore.Store = (*bleve.BleveBackend)(nil)
)
