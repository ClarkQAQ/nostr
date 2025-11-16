package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"fiatjaf.com/nostr/eventstore/boltdb"
	"fiatjaf.com/nostr/khatru"
	"fiatjaf.com/nostr/khatru/blossom"
)

func main() {
	relay := khatru.NewRelay()

	db := &boltdb.BoltBackend{Path: "/tmp/khatru-boltdb-tmp"}
	if err := db.Init(); err != nil {
		panic(err)
	}

	relay.UseEventstore(db, 400)

	bdb := &boltdb.BoltBackend{Path: "/tmp/khatru-boltdb-blossom-tmp"}
	if err := bdb.Init(); err != nil {
		panic(err)
	}
	bl := blossom.New()
	bl.Store = blossom.EventStoreBlobIndexWrapper{Store: bdb}
	bl.StoreBlob = func(ctx context.Context, sha256 string, ext string, body []byte) error {
		fmt.Println("storing", sha256, len(body))
		return nil
	}
	bl.LoadBlob = func(ctx context.Context, sha256 string, ext string) (io.ReadSeeker, *url.URL, error) {
		fmt.Println("loading", sha256)
		blob := strings.NewReader("aaaaa")
		return blob, nil, nil
	}

	fmt.Println("running on :3334")
	if e := http.ListenAndServe(":3334", relay); e != nil {
		panic(e)
	}
}
