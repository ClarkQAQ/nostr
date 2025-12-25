package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"fiatjaf.com/nostr/eventstore/boltdb"
	"fiatjaf.com/nostr/relay"
	"fiatjaf.com/nostr/relay/grasp"
)

func main() {
	relay := relay.NewRelay()

	db := &boltdb.BoltBackend{Path: "/tmp/relay-grasp-lmdb-tmp"}
	_ = os.MkdirAll(db.Path, 0o755)
	if err := db.Init(context.Background()); err != nil {
		panic(err)
	}

	relay.UseEventstore(db, 400)

	// create repository directory
	repoDir := "/tmp/relay-grasp-repos"
	_ = os.MkdirAll(repoDir, 0o755)

	// set up grasp server
	grasp.New(relay, repoDir)

	fmt.Println("running grasp example on :3334")
	_ = http.ListenAndServe(":3334", relay)
}
