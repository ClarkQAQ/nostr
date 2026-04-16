package khatru

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/slicestore"
	"fiatjaf.com/nostr/nip11"
	"github.com/stretchr/testify/require"
)

func TestWithServiceURL(t *testing.T) {
	relay := NewRelay()
	relay.Info.Icon = "icon.png"
	relay.Info.Banner = "banner.png"
	relay.Router().HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		io.WriteString(w, "fallback")
	})

	handlerA := relay.WithServiceURL("https://a.example/relay")
	handlerB := relay.WithServiceURL("https://b.example/relay")

	t.Run("uses override for nip11 base url", func(t *testing.T) {
		for _, tc := range []struct {
			name         string
			handler      http.Handler
			expectedBase string
		}{
			{name: "first interface", handler: handlerA, expectedBase: "https://a.example/relay"},
			{name: "second interface", handler: handlerB, expectedBase: "https://b.example/relay"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, "http://internal/relay", nil)
				req.Header.Set("Accept", "application/nostr+json")
				rr := httptest.NewRecorder()

				tc.handler.ServeHTTP(rr, req)

				require.Equal(t, http.StatusOK, rr.Code)

				var info nip11.RelayInformationDocument
				require.NoError(t, json.NewDecoder(rr.Body).Decode(&info))
				require.Equal(t, tc.expectedBase+"/icon.png", info.Icon)
				require.Equal(t, tc.expectedBase+"/banner.png", info.Banner)
			})
		}
	})

	t.Run("uses override for relay path matching", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://internal/not-relay", nil)
		req.Header.Set("Accept", "application/nostr+json")
		rr := httptest.NewRecorder()

		handlerA.ServeHTTP(rr, req)

		require.Equal(t, http.StatusAccepted, rr.Code)
		require.Equal(t, "fallback", rr.Body.String())
	})
}

func TestBasicRelayFunctionality(t *testing.T) {
	// setup relay with in-memory store
	relay := NewRelay()
	store := &slicestore.SliceStore{}
	store.Init()

	relay.UseEventstore(store, 400)

	// start test server
	server := httptest.NewServer(relay)
	defer server.Close()

	// create test keys
	sk1 := nostr.Generate()
	pk1 := nostr.GetPublicKey(sk1)
	sk2 := nostr.Generate()
	pk2 := nostr.GetPublicKey(sk2)

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

	// connect two test clients
	url := "ws" + server.URL[4:]
	client1, err := nostr.RelayConnect(t.Context(), url, nostr.RelayOptions{})
	require.NoError(t, err, "failed to connect client1")
	defer client1.Close()

	client2, err := nostr.RelayConnect(t.Context(), url, nostr.RelayOptions{})
	require.NoError(t, err, "failed to connect client2")
	defer client2.Close()

	// test 1: store and query events
	t.Run("store and query events", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		evt1 := createEvent(sk1, 1, "hello world", nil)
		err := client1.Publish(ctx, evt1)
		require.NoError(t, err, "failed to publish event")

		// Query the event back
		sub, err := client2.Subscribe(ctx, nostr.Filter{
			Authors: []nostr.PubKey{pk1},
			Kinds:   []nostr.Kind{1},
		}, nostr.SubscriptionOptions{})
		require.NoError(t, err, "failed to subscribe")
		defer sub.Unsub()

		// Wait for event
		select {
		case env := <-sub.Events:
			if env.ID != evt1.ID {
				t.Errorf("got wrong event: %v", env.ID)
			}
		case <-ctx.Done():
			require.FailNow(t, "timeout waiting for event")
		}
	})

	// test 2: live event subscription
	t.Run("live event subscription", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		// Setup subscription first
		sub, err := client1.Subscribe(ctx, nostr.Filter{
			Authors: []nostr.PubKey{pk2},
			Kinds:   []nostr.Kind{1},
		}, nostr.SubscriptionOptions{})
		require.NoError(t, err, "failed to subscribe")
		defer sub.Unsub()

		// Publish event from client2
		evt2 := createEvent(sk2, 1, "testing live events", nil)
		err = client2.Publish(ctx, evt2)
		require.NoError(t, err, "failed to publish event")

		// Wait for event on subscription
		select {
		case env := <-sub.Events:
			if env.ID != evt2.ID {
				t.Errorf("got wrong event: %v", env.ID)
			}
		case <-ctx.Done():
			require.FailNow(t, "timeout waiting for live event")
		}
	})

	// test 3: event deletion
	t.Run("event deletion", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		// Create an event to be deleted
		evt3 := createEvent(sk1, 1, "delete me", nil)
		err = client1.Publish(ctx, evt3)
		require.NoError(t, err, "failed to publish event")

		// Create deletion event
		delEvent := createEvent(sk1, 5, "deleting", nostr.Tags{{"e", evt3.ID.Hex()}})
		err = client1.Publish(ctx, delEvent)
		require.NoError(t, err, "failed to publish deletion event")

		// Try to query the deleted event
		sub, err := client2.Subscribe(ctx, nostr.Filter{
			IDs: []nostr.ID{evt3.ID},
		}, nostr.SubscriptionOptions{})
		require.NoError(t, err, "failed to subscribe")
		defer sub.Unsub()

		// Should get EOSE without receiving the deleted event
		gotEvent := false
		for {
			select {
			case <-sub.Events:
				gotEvent = true
			case <-sub.EndOfStoredEvents:
				if gotEvent {
					t.Error("should not have received deleted event")
				}
				goto checkDeleteStored
			case <-ctx.Done():
				require.FailNow(t, "timeout waiting for EOSE")
			}
		}

	checkDeleteStored:
		// verify that the delete event itself is stored
		subDelete, err := client2.Subscribe(ctx, nostr.Filter{
			IDs: []nostr.ID{delEvent.ID},
		}, nostr.SubscriptionOptions{})
		require.NoError(t, err, "failed to subscribe to delete event")
		defer subDelete.Unsub()

		gotDeleteEvent := false
		for {
			select {
			case evt := <-subDelete.Events:
				if evt.ID == delEvent.ID {
					gotDeleteEvent = true
				}
			case <-subDelete.EndOfStoredEvents:
				if !gotDeleteEvent {
					t.Error("should have received the delete event")
				}
				return
			case <-ctx.Done():
				require.FailNow(t, "timeout waiting for EOSE on delete event")
			}
		}
	})

	// test 4: teplaceable events
	t.Run("replaceable events", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		// create initial kind:0 event
		evt1 := createEvent(sk1, 0, `{"name":"initial"}`, nil)
		evt1.CreatedAt = 1000 // Set specific timestamp for testing
		evt1.Sign(sk1)
		err = client1.Publish(ctx, evt1)
		require.NoError(t, err, "failed to publish initial event")

		// create newer event that should replace the first
		evt2 := createEvent(sk1, 0, `{"name":"newer"}`, nil)
		evt2.CreatedAt = 2004 // Newer timestamp
		evt2.Sign(sk1)
		err = client1.Publish(ctx, evt2)
		require.NoError(t, err, "failed to publish newer event")

		// create older event that should not replace the current one
		evt3 := createEvent(sk1, 0, `{"name":"older"}`, nil)
		evt3.CreatedAt = 1500 // Older than evt2
		evt3.Sign(sk1)
		err = client1.Publish(ctx, evt3)
		require.NoError(t, err, "failed to publish older event")

		// query to verify only the newest event exists
		sub, err := client2.Subscribe(ctx, nostr.Filter{
			Authors: []nostr.PubKey{pk1},
			Kinds:   []nostr.Kind{0},
		}, nostr.SubscriptionOptions{})
		require.NoError(t, err, "failed to subscribe")
		defer sub.Unsub()

		// should only get one event back (the newest one)
		var receivedEvents []nostr.Event
		for {
			select {
			case evt := <-sub.Events:
				receivedEvents = append(receivedEvents, evt)
			case <-sub.EndOfStoredEvents:
				if len(receivedEvents) != 1 {
					t.Errorf("expected exactly 1 event, got %v", receivedEvents)
				}
				if len(receivedEvents) > 0 && receivedEvents[0].Content != `{"name":"newer"}` {
					t.Errorf("expected newest event content, got %s", receivedEvents[0].Content)
				}
				return
			case <-ctx.Done():
				require.FailNow(t, "timeout waiting for events")
			}
		}
	})

	// test 5: event expiration
	t.Run("event expiration", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		// create a new relay with shorter expiration check interval
		relay := NewRelay()
		store := &slicestore.SliceStore{}
		store.Init()

		// this will automatically start the expiration manager
		relay.UseEventstore(store, 400)

		if relay.expirationManager.interval > time.Second*10 {
			t.Skip("expiration manager must be manually hardcodedly set to less than 10s for testing")
			return
		}

		// start test server
		server := httptest.NewServer(relay)
		defer server.Close()

		// connect test client
		url := "ws" + server.URL[4:]
		client, err := nostr.RelayConnect(t.Context(), url, nostr.RelayOptions{})
		require.NoError(t, err, "failed to connect client")
		defer client.Close()

		// create event that expires in 2 seconds
		expiration := strconv.FormatInt(int64(nostr.Now()+2), 10)
		evt := createEvent(sk1, 1, "i will expire soon", nostr.Tags{{"expiration", expiration}})
		err = client.Publish(ctx, evt)
		require.NoError(t, err, "failed to publish event")

		// verify event exists initially
		sub, err := client.Subscribe(ctx, nostr.Filter{
			IDs: []nostr.ID{evt.ID},
		}, nostr.SubscriptionOptions{})
		require.NoError(t, err, "failed to subscribe")

		// should get the event
		select {
		case env := <-sub.Events:
			if env.ID != evt.ID {
				t.Error("got wrong event")
			}
		case <-ctx.Done():
			require.FailNow(t, "timeout waiting for event")
		}
		sub.Unsub()

		// wait for expiration check (+1 second)
		time.Sleep(relay.expirationManager.interval + time.Second)

		// verify event no longer exists
		sub, err = client.Subscribe(ctx, nostr.Filter{
			IDs: []nostr.ID{evt.ID},
		}, nostr.SubscriptionOptions{})
		require.NoError(t, err, "failed to subscribe")
		defer sub.Unsub()

		// should get EOSE without receiving the expired event
		gotEvent := false
		for {
			select {
			case <-sub.Events:
				gotEvent = true
			case <-sub.EndOfStoredEvents:
				if gotEvent {
					t.Error("should not have received expired event")
				}
				return
			case <-ctx.Done():
				require.FailNow(t, "timeout waiting for EOSE")
			}
		}
	})

	// test 6: unauthorized deletion
	t.Run("unauthorized deletion", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		// create an event from client1
		evt4 := createEvent(sk1, 1, "try to delete me", nil)
		err = client1.Publish(ctx, evt4)
		require.NoError(t, err)

		// try to delete it with client2
		delEvent := createEvent(sk2, 5, "trying to delete", nostr.Tags{{"e", evt4.ID.Hex()}})
		err = client2.Publish(ctx, delEvent)
		require.Error(t, err)

		// verify event still exists
		sub, err := client1.Subscribe(ctx, nostr.Filter{
			IDs: []nostr.ID{evt4.ID},
		}, nostr.SubscriptionOptions{})
		require.NoError(t, err)
		defer sub.Unsub()

		select {
		case env, more := <-sub.Events:
			require.True(t, more, "should get an event, got nothing")
			require.Equal(t, env.ID, evt4.ID, "got wrong event")
		case <-ctx.Done():
			require.FailNow(t, "event should still exist")
		}
	})
}
