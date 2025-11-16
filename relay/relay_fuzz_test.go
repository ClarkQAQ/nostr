package relay

import (
	"context"
	"math"
	"math/rand/v2"
	"net/http/httptest"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/boltdb"
	"github.com/stretchr/testify/require"
)

func FuzzReplaceableEvents(f *testing.F) {
	f.Add(uint(1), uint(2))

	f.Fuzz(func(t *testing.T, seed uint, nevents uint) {
		if nevents == 0 {
			return
		}

		relay := NewRelay()
		store := &boltdb.BoltBackend{Path: "/tmp/fuzz"}
		store.Init()
		relay.UseEventstore(store, 4000)

		defer store.Close()

		// start test server
		server := httptest.NewServer(relay)
		defer server.Close()

		// create test keys
		sk1 := nostr.Generate()
		pk1 := nostr.GetPublicKey(sk1)

		// helper to create signed events
		createEvent := func(sk nostr.SecretKey, kind nostr.Kind, content string, tags nostr.Tags) nostr.Event {
			pk := nostr.GetPublicKey(sk)
			evt := nostr.Event{
				PubKey:    pk,
				CreatedAt: nostr.Now(),
				Kind:      kind,
				Tags:      tags,
				Content:   content,
			}
			evt.Sign(sk)
			return evt
		}

		url := "ws" + server.URL[4:]
		client1, err := nostr.RelayConnect(context.Background(), url, nostr.RelayOptions{})
		if err != nil {
			t.Skip("failed to connect client1")
		}
		defer client1.Close()

		client2, err := nostr.RelayConnect(context.Background(), url, nostr.RelayOptions{})
		if err != nil {
			t.Skip("failed to connect client2")
		}
		defer client2.Close()

		t.Run("replaceable events", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			rnd := rand.New(rand.NewPCG(uint64(seed), 0))

			newest := nostr.Timestamp(0)
			for range nevents {
				evt := createEvent(sk1, 0, `{"name":"blblbl"}`, nil)
				evt.CreatedAt = nostr.Timestamp(rnd.Int64() % math.MaxUint32)
				evt.Sign(sk1)
				err = client1.Publish(ctx, evt)
				if err != nil {
					t.Fatalf("failed to publish event: %v", err)
				}

				if evt.CreatedAt > newest {
					newest = evt.CreatedAt
				}
			}

			// query to verify only the newest event exists
			sub, err := client2.Subscribe(ctx, nostr.Filter{
				Authors: []nostr.PubKey{pk1},
				Kinds:   []nostr.Kind{0},
			}, nostr.SubscriptionOptions{})
			if err != nil {
				t.Fatalf("failed to subscribe: %v", err)
			}
			defer sub.Unsub()

			// should only get one event back (the newest one)
			var receivedEvents []nostr.Event
			for {
				select {
				case evt := <-sub.Events:
					receivedEvents = append(receivedEvents, evt)
				case <-sub.EndOfStoredEvents:
					require.Len(t, receivedEvents, 1)
					require.Equal(t, newest, receivedEvents[0].CreatedAt)
					return
				case <-ctx.Done():
					t.Fatal("timeout waiting for events")
				}
			}
		})
	})
}
