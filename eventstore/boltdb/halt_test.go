package boltdb

import (
	"context"
	"log"
	"net/http"
	"os"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/relay"
	"github.com/stretchr/testify/require"
)

func TestHaltingProblem(t *testing.T) {
	go func() {
		if err := os.RemoveAll("/tmp/bolthalttest"); err != nil {
			log.Fatal(err)
			return
		}
		db := BoltBackend{Path: "/tmp/bolthalttest"}
		if err := db.Init(context.Background()); err != nil {
			panic(err)
		}

		relay := relay.NewRelay()
		relay.UseEventstore(&db, 500)

		server := &http.Server{Addr: ":54898", Handler: relay}
		server.ListenAndServe()
	}()

	time.Sleep(time.Millisecond * 200)
	client, err := nostr.RelayConnect(t.Context(), "http://127.0.0.1:54898", nostr.RelayOptions{})
	require.NoError(t, err)
	sk := nostr.Generate()
	var id nostr.ID

	{
		evt := nostr.Event{
			CreatedAt: nostr.Now(),
			Content:   "",
			Kind:      nostr.Kind(1),
		}
		evt.Sign(sk)
		err := client.Publish(context.Background(), evt)
		require.NoError(t, err)
		id = evt.ID
		t.Logf("event published: %s\n", id.Hex())
	}

	{
		evt := nostr.Event{
			CreatedAt: nostr.Now(),
			Tags: nostr.Tags{
				nostr.Tag{"e", id.Hex()},
			},
			Kind: nostr.Kind(5),
		}
		evt.Sign(sk)

		err := client.Publish(context.Background(), evt)
		require.NoError(t, err)
		t.Logf("event deleted\n")
	}
}
