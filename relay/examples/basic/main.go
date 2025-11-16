package main

import (
	"fmt"
	"net/http"
	"os"

	"fiatjaf.com/nostr/eventstore/boltdb"
	"fiatjaf.com/nostr/relay"
)

func main() {
	r := relay.NewRelay()

	db := &boltdb.BoltBackend{Path: "/tmp/relay-tmp"}
	_ = os.MkdirAll(db.Path, 0o755)
	if err := db.Init(); err != nil {
		panic(err)
	}

	r.UseEventstore(db, 400)

	fmt.Println("running on :3334")
	_ = http.ListenAndServe(":3334", r)
}
