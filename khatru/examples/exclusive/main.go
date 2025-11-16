package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/boltdb"
	"fiatjaf.com/nostr/khatru"
	"fiatjaf.com/nostr/khatru/policies"
)

func main() {
	relay := khatru.NewRelay()

	db := &boltdb.BoltBackend{Path: "/tmp/exclusive"}
	if e := os.MkdirAll(db.Path, 0o755); e != nil {
		panic(e)
	}
	if err := db.Init(); err != nil {
		panic(err)
	}

	relay.UseEventstore(db, 400)

	relay.OnEvent = policies.PreventTooManyIndexableTags(10, nil, nil)
	relay.OnRequest = policies.NoComplexFilters

	relay.OnEventSaved = func(ctx context.Context, event nostr.Event) {
	}

	fmt.Println("running on :3334")
	if e := http.ListenAndServe(":3334", relay); e != nil {
		panic(e)
	}
}
