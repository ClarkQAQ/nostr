package checks

import (
	"fiatjaf.com/nostr/eventstore"
	"fiatjaf.com/nostr/eventstore/bluge"
	"fiatjaf.com/nostr/eventstore/boltdb"
)

// compile-time checks to ensure all backends implement Store
var (
	_ eventstore.Store = (*boltdb.BoltBackend)(nil)
	_ eventstore.Store = (*bluge.BlugeBackend)(nil)
)
