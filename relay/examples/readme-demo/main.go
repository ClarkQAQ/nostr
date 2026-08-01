package main

import (
	"context"
	"fmt"
	"iter"
	"log"
	"net/http"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/relay"
	"fiatjaf.com/nostr/relay/policies"
)

func main() {
	// create the relay instance
	r := relay.NewRelay()

	// set up some basic properties (will be returned on the NIP-11 endpoint)
	pk := nostr.MustPubKeyFromHex("79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798")

	r.Info.Name = "my relay"
	r.Info.PubKey = &pk
	r.Info.Description = "this is my custom relay"
	r.Info.Icon = "https://external-content.duckduckgo.com/iu/?u=https%3A%2F%2Fliquipedia.net%2Fcommons%2Fimages%2F3%2F35%2FSCProbe.jpg&f=1&nofb=1&ipt=0cbbfef25bce41da63d910e86c3c343e6c3b9d63194ca9755351bb7c2efa3359&ipo=images"

	// you must bring your own storage scheme -- if you want to have any
	store := make(map[nostr.ID]nostr.Event, 120)

	// set up the basic relay functions
	r.StoreEvent = func(ctx context.Context, event nostr.Event) error {
		store[event.ID] = event
		return nil
	}
	r.QueryStored = func(ctx context.Context, filter nostr.Filter) iter.Seq2[nostr.Event, error] {
		return func(yield func(nostr.Event, error) bool) {
			for _, evt := range store {
				if filter.Matches(evt) {
					if !yield(evt, nil) {
						return
					}
				}
			}
		}
	}
	r.DeleteEvent = func(ctx context.Context, id nostr.ID) error {
		delete(store, id)
		return nil
	}

	// there are many other configurable things you can set
	r.OnEvent = policies.SeqEvent(
		// built-in policies
		policies.ValidateKind,
		policies.RejectUnprefixedNostrReferences,

		// define your own policies
		func(ctx context.Context, event nostr.Event) (reject bool, msg string) {
			if event.PubKey == nostr.MustPubKeyFromHex("fa984bd7dbb282f07e16e7ae87b26a2a7b9b90b7246a44771f0cf5ae58018f52") {
				return true, "we don't allow this person to write here"
			}
			return false, "" // anyone else can
		},
	)

	// you can request auth by rejecting an event or a request with the prefix "auth-required: "
	r.OnRequest = policies.SeqRequest(
		// built-in policies
		policies.NoComplexFilters,

		// define your own policies
		func(ctx context.Context, filter nostr.Filter) (reject bool, msg string) {
			if authed, is := relay.GetAuthed(ctx); !is {
				log.Printf("request from %s\n", authed)
				return false, ""
			}
			return true, "auth-required: only authenticated users can read from this relay"
			// (this will cause an AUTH message to be sent and then a CLOSED message such that clients can
			//  authenticate and then request again)
		},
	)
	// check the docs for more goodies!

	mux := r.Router()
	// set up other http handlers
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/html")
		fmt.Fprintf(w, `<b>welcome</b> to my relay!`)
	})

	// start the server
	fmt.Println("running on :3334")
	http.ListenAndServe(":3334", r)
}
