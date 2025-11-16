package main

import (
	"fmt"
	"net/http"
	"os"

	"fiatjaf.com/nostr/eventstore/boltdb"
	"fiatjaf.com/nostr/khatru"
)

func main() {
	relay := khatru.NewRelay()

	db := &boltdb.BoltBackend{Path: "/tmp/khatru-tmp"}
	os.MkdirAll(db.Path, 0o755)
	if err := db.Init(); err != nil {
		panic(err)
	}

	relay.UseEventstore(db, 400)

	fmt.Println("running on :3334")
	http.ListenAndServe(":3334", relay)
}
