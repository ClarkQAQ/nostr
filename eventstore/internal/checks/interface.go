package checks

import (
	"fiatjaf.com/nostr/eventstore"
	"fiatjaf.com/nostr/eventstore/badgerdb"
	"fiatjaf.com/nostr/eventstore/boltdb"
	"fiatjaf.com/nostr/eventstore/clickhousedb"
)

// compile-time checks to ensure all backends implement Store
var (
	_ eventstore.Store = (*boltdb.BoltBackend)(nil)
	_ eventstore.Store = (*badgerdb.BadgerBackend)(nil)
	_ eventstore.Store = (*clickhousedb.ClickHouseBackend)(nil)
)
