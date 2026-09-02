package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"fiatjaf.com/nostr/eventstore/boltdb"
	blossomclient "fiatjaf.com/nostr/nipb7/blossom"
	"fiatjaf.com/nostr/relay"
	"fiatjaf.com/nostr/relay/blossom"
)

func main() {
	r := relay.NewRelay()

	db := &boltdb.BoltBackend{Path: "/tmp/relay-boltdb-tmp"}
	if err := db.Init(context.Background()); err != nil {
		panic(err)
	}

	r.UseEventstore(db, 400)

	bdb := &boltdb.BoltBackend{Path: "/tmp/relay-boltdb-blossom-tmp"}
	if err := bdb.Init(context.Background()); err != nil {
		panic(err)
	}
	bl := blossom.New()
	bl.Store = blossom.EventStoreBlobIndexWrapper{Store: bdb}
	bl.StoreBlob = func(ctx context.Context, sha256 string, ext string, _ int64, reader io.Reader) error {
		size, e := io.Copy(io.Discard, reader)
		if e != nil {
			return e
		}

		fmt.Println("storing", sha256, size)
		return nil
	}
	bl.LoadBlob = func(ctx context.Context, sha256 string, ext string) (io.ReadSeekCloser, *url.URL, error) {
		fmt.Println("loading", sha256)
		return blossomclient.NopSeekCloser(strings.NewReader("aaaaa")), nil, nil
	}

	fmt.Println("running on :3334")
	if e := http.ListenAndServe(":3334", r); e != nil {
		panic(e)
	}
}
