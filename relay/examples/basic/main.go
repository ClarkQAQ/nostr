package main

import (
	"fmt"
	"net/http"
	"os"

	"fiatjaf.com/nostr/eventstore/badgerdb"
	"fiatjaf.com/nostr/relay"
)

func main() {
	r := relay.NewRelay()

	path, e := os.MkdirTemp("", "relay-tmp")
	if e != nil {
		panic(e)
	}
	defer os.RemoveAll(path)

	db := &badgerdb.BadgerBackend{Path: path}
	if e := db.Init(); e != nil {
		panic(e)
	}

	r.UseEventstore(db, 400)

	fmt.Println("running on :3334")
	_ = http.ListenAndServe(":3334", r)
}
