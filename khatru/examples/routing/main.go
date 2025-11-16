package main

import (
	"fmt"
	"net/http"
	"slices"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/boltdb"
	"fiatjaf.com/nostr/eventstore/slicestore"
	"fiatjaf.com/nostr/khatru"
)

func main() {
	db1 := &slicestore.SliceStore{}
	if e := db1.Init(); e != nil {
		panic(e)
	}
	r1 := khatru.NewRelay()
	r1.UseEventstore(db1, 400)

	db2 := &boltdb.BoltBackend{Path: "/tmp/t"}
	if e := db2.Init(); e != nil {
		panic(e)
	}
	r2 := khatru.NewRelay()
	r2.UseEventstore(db2, 400)

	db3 := &slicestore.SliceStore{}
	if e := db3.Init(); e != nil {
		panic(e)
	}
	r3 := khatru.NewRelay()
	r3.UseEventstore(db3, 400)

	router := khatru.NewRouter()

	router.Route().
		Req(func(filter nostr.Filter) bool {
			return slices.Contains(filter.Kinds, 30023)
		}).
		Event(func(event *nostr.Event) bool {
			return event.Kind == 30023
		}).
		Relay(r1)

	router.Route().
		Req(func(filter nostr.Filter) bool {
			return slices.Contains(filter.Kinds, 1) && slices.Contains(filter.Tags["t"], "spam")
		}).
		Event(func(event *nostr.Event) bool {
			return event.Kind == 1 && event.Tags.FindWithValue("t", "spam") != nil
		}).
		Relay(r2)

	router.Route().
		Req(func(filter nostr.Filter) bool {
			return slices.Contains(filter.Kinds, 1)
		}).
		Event(func(event *nostr.Event) bool {
			return event.Kind == 1
		}).
		Relay(r3)

	fmt.Println("running on :3334")
	if e := http.ListenAndServe(":3334", router); e != nil {
		panic(e)
	}
}
