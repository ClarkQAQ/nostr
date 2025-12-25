package relay

import (
	"context"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/slicestore"
)

func TestBasicRelayFunctionality(t *testing.T) {
	// setup relay with in-memory store
	relay := NewRelay()
	store := &slicestore.SliceStore{}
	store.Init(context.Background())

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
	if err != nil {
		t.Fatalf("failed to connect client1: %v", err)
	}
	defer client1.Close()

	client2, err := nostr.RelayConnect(t.Context(), url, nostr.RelayOptions{})
	if err != nil {
		t.Fatalf("failed to connect client2: %v", err)
	}
	defer client2.Close()

	// test 1: store and query events
	t.Run("store and query events", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		evt1 := createEvent(sk1, 1, "hello world", nil)
		err := client1.Publish(ctx, evt1)
		if err != nil {
			t.Fatalf("failed to publish event: %v", err)
		}

		// Query the event back
		sub, err := client2.Subscribe(ctx, nostr.Filter{
			Authors: []nostr.PubKey{pk1},
			Kinds:   []nostr.Kind{1},
		}, nostr.SubscriptionOptions{})
		if err != nil {
			t.Fatalf("failed to subscribe: %v", err)
		}
		defer sub.Unsub()

		// Wait for event
		select {
		case env := <-sub.Events:
			if env.ID != evt1.ID {
				t.Errorf("got wrong event: %v", env.ID)
			}
		case <-ctx.Done():
			t.Fatal("timeout waiting for event")
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
		if err != nil {
			t.Fatalf("failed to subscribe: %v", err)
		}
		defer sub.Unsub()

		// Publish event from client2
		evt2 := createEvent(sk2, 1, "testing live events", nil)
		err = client2.Publish(ctx, evt2)
		if err != nil {
			t.Fatalf("failed to publish event: %v", err)
		}

		// Wait for event on subscription
		select {
		case env := <-sub.Events:
			if env.ID != evt2.ID {
				t.Errorf("got wrong event: %v", env.ID)
			}
		case <-ctx.Done():
			t.Fatal("timeout waiting for live event")
		}
	})

	// test 3: event deletion
	t.Run("event deletion", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		// Create an event to be deleted
		evt3 := createEvent(sk1, 1, "delete me", nil)
		err = client1.Publish(ctx, evt3)
		if err != nil {
			t.Fatalf("failed to publish event: %v", err)
		}

		// Create deletion event
		delEvent := createEvent(sk1, 5, "deleting", nostr.Tags{{"e", evt3.ID.Hex()}})
		err = client1.Publish(ctx, delEvent)
		if err != nil {
			t.Fatalf("failed to publish deletion event: %v", err)
		}

		// Try to query the deleted event
		sub, err := client2.Subscribe(ctx, nostr.Filter{
			IDs: []nostr.ID{evt3.ID},
		}, nostr.SubscriptionOptions{})
		if err != nil {
			t.Fatalf("failed to subscribe: %v", err)
		}
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
				t.Fatal("timeout waiting for EOSE")
			}
		}

	checkDeleteStored:
		// verify that the delete event itself is stored
		subDelete, err := client2.Subscribe(ctx, nostr.Filter{
			IDs: []nostr.ID{delEvent.ID},
		}, nostr.SubscriptionOptions{})
		if err != nil {
			t.Fatalf("failed to subscribe to delete event: %v", err)
		}
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
				t.Fatal("timeout waiting for EOSE on delete event")
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
		if err != nil {
			t.Fatalf("failed to publish initial event: %v", err)
		}

		// create newer event that should replace the first
		evt2 := createEvent(sk1, 0, `{"name":"newer"}`, nil)
		evt2.CreatedAt = 2004 // Newer timestamp
		evt2.Sign(sk1)
		err = client1.Publish(ctx, evt2)
		if err != nil {
			t.Fatalf("failed to publish newer event: %v", err)
		}

		// create older event that should not replace the current one
		evt3 := createEvent(sk1, 0, `{"name":"older"}`, nil)
		evt3.CreatedAt = 1500 // Older than evt2
		evt3.Sign(sk1)
		err = client1.Publish(ctx, evt3)
		if err != nil {
			t.Fatalf("failed to publish older event: %v", err)
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
				if len(receivedEvents) != 1 {
					t.Errorf("expected exactly 1 event, got %v", receivedEvents)
				}
				if len(receivedEvents) > 0 && receivedEvents[0].Content != `{"name":"newer"}` {
					t.Errorf("expected newest event content, got %s", receivedEvents[0].Content)
				}
				return
			case <-ctx.Done():
				t.Fatal("timeout waiting for events")
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
		store.Init(context.Background())

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
		if err != nil {
			t.Fatalf("failed to connect client: %v", err)
		}
		defer client.Close()

		// create event that expires in 2 seconds
		expiration := strconv.FormatInt(int64(nostr.Now()+2), 10)
		evt := createEvent(sk1, 1, "i will expire soon", nostr.Tags{{"expiration", expiration}})
		err = client.Publish(ctx, evt)
		if err != nil {
			t.Fatalf("failed to publish event: %v", err)
		}

		// verify event exists initially
		sub, err := client.Subscribe(ctx, nostr.Filter{
			IDs: []nostr.ID{evt.ID},
		}, nostr.SubscriptionOptions{})
		if err != nil {
			t.Fatalf("failed to subscribe: %v", err)
		}

		// should get the event
		select {
		case env := <-sub.Events:
			if env.ID != evt.ID {
				t.Error("got wrong event")
			}
		case <-ctx.Done():
			t.Fatal("timeout waiting for event")
		}
		sub.Unsub()

		// wait for expiration check (+1 second)
		time.Sleep(relay.expirationManager.interval + time.Second)

		// verify event no longer exists
		sub, err = client.Subscribe(ctx, nostr.Filter{
			IDs: []nostr.ID{evt.ID},
		}, nostr.SubscriptionOptions{})
		if err != nil {
			t.Fatalf("failed to subscribe: %v", err)
		}
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
				t.Fatal("timeout waiting for EOSE")
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
		if err != nil {
			t.Fatalf("failed to publish event: %v", err)
		}

		// Try to delete it with client2
		delEvent := createEvent(sk2, 5, "trying to delete", nostr.Tags{{"e", evt4.ID.Hex()}})
		err = client2.Publish(ctx, delEvent)
		if err == nil {
			t.Fatalf("should have failed to publish deletion event: %v", err)
		}

		// Verify event still exists
		sub, err := client1.Subscribe(ctx, nostr.Filter{
			IDs: []nostr.ID{evt4.ID},
		}, nostr.SubscriptionOptions{})
		if err != nil {
			t.Fatalf("failed to subscribe: %v", err)
		}
		defer sub.Unsub()

		select {
		case env := <-sub.Events:
			if env.ID != evt4.ID {
				t.Error("got wrong event")
			}
		case <-ctx.Done():
			t.Fatal("event should still exist")
		}
	})
}
