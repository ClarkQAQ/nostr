package sdk

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/slicestore"
	"fiatjaf.com/nostr/khatru"
)

func TestStreamLiveFeed(t *testing.T) {
	ctx := context.Background()

	// start 3 local relays using httptest
	relay1 := khatru.NewRelay()
	relay2 := khatru.NewRelay()
	relay3 := khatru.NewRelay()

	dbs := make([]*slicestore.SliceStore, 3)
	for i, r := range []*khatru.Relay{relay1, relay2, relay3} {
		dbs[i] = &slicestore.SliceStore{}
		dbs[i].Init()
		r.UseEventstore(dbs[i], 4000)
	}

	server1 := httptest.NewServer(relay1)
	server2 := httptest.NewServer(relay2)
	server3 := httptest.NewServer(relay3)
	defer server1.Close()
	defer server2.Close()
	defer server3.Close()
	for _, db := range dbs {
		defer db.Close()
	}

	// generate two random keypairs for testing
	sk1 := nostr.Generate()
	pk1 := nostr.GetPublicKey(sk1)
	sk2 := nostr.Generate()
	pk2 := nostr.GetPublicKey(sk2)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	url1 := "ws" + server1.URL[4:]
	url2 := "ws" + server2.URL[4:]
	url3 := "ws" + server3.URL[4:]

	// first publish relay lists to relay1 for both users
	relayListEvt1 := nostr.Event{
		PubKey:    pk1,
		CreatedAt: nostr.Now(),
		Kind:      10002,
		Tags: nostr.Tags{
			{"r", url2, "write"},
			{"r", url3, "write"},
		},
		Content: "",
	}
	relayListEvt1.Sign(sk1)

	relayListEvt2 := nostr.Event{
		PubKey:    pk2,
		CreatedAt: nostr.Now(),
		Kind:      10002,
		Tags: nostr.Tags{
			{"r", url2, "write"},
			{"r", url3, "write"},
		},
		Content: "",
	}
	relayListEvt2.Sign(sk2)

	// publish relay lists to relay1
	relay, err := nostr.RelayConnect(ctx, url1, nostr.RelayOptions{})
	if err != nil {
		t.Fatalf("failed to connect to relay1: %v", err)
	}
	if err := relay.Publish(ctx, relayListEvt1); err != nil {
		t.Fatalf("failed to publish relay list 1: %v", err)
	}
	if err := relay.Publish(ctx, relayListEvt2); err != nil {
		t.Fatalf("failed to publish relay list 2: %v", err)
	}
	relay.Close()

	// create a new system instance pointing only to relay1 as the "indexer"
	sys := NewSystem()
	sys.RelayListRelays = NewRelayStream(url1)
	defer sys.Close()

	// prepublish some events
	evt1 := nostr.Event{
		PubKey:    pk1,
		CreatedAt: nostr.Now(),
		Kind:      1,
		Tags:      nostr.Tags{},
		Content:   "hello from user 1",
	}
	evt1.Sign(sk1)

	evt2 := nostr.Event{
		PubKey:    pk2,
		CreatedAt: nostr.Now(),
		Kind:      1,
		Tags:      nostr.Tags{},
		Content:   "hello from user 2",
	}
	evt2.Sign(sk2)

	// publish events concurrently to relays 2 and 3
	go sys.Pool.PublishMany(ctx, []string{url2, url3}, evt1)
	go sys.Pool.PublishMany(ctx, []string{url2, url3}, evt2)

	// start streaming events for both pubkeys
	events, err := sys.StreamLiveFeed(ctx, []nostr.PubKey{pk1, pk2}, []nostr.Kind{1})
	if err != nil {
		t.Fatalf("failed to start streaming: %v", err)
	}

	{
		// wait for the prepublished events
		receivedEvt1 := false
		receivedEvt2 := false

		timeout := time.After(5 * time.Second)
		for !receivedEvt1 || !receivedEvt2 {
			select {
			case evt := <-events:
				if evt.ID == evt1.ID {
					receivedEvt1 = true
				}
				if evt.ID == evt2.ID {
					receivedEvt2 = true
				}
			case <-timeout:
				t.Fatal("timeout waiting for events")
			}
		}
	}

	{
		// publish some live events
		evt1 := nostr.Event{
			PubKey:    pk1,
			CreatedAt: nostr.Now(),
			Kind:      1,
			Tags:      nostr.Tags{},
			Content:   "hello from user 1",
		}
		evt1.Sign(sk1)

		evt2 := nostr.Event{
			PubKey:    pk2,
			CreatedAt: nostr.Now(),
			Kind:      1,
			Tags:      nostr.Tags{},
			Content:   "hello from user 2",
		}
		evt2.Sign(sk2)

		// publish events concurrently to relays 2 and 3
		go sys.Pool.PublishMany(ctx, []string{url2, url3}, evt1)
		go sys.Pool.PublishMany(ctx, []string{url2, url3}, evt2)

		// wait for events
		receivedEvt1 := false
		receivedEvt2 := false

		timeout := time.After(5 * time.Second)
		for !receivedEvt1 || !receivedEvt2 {
			select {
			case evt := <-events:
				if evt.ID == evt1.ID {
					receivedEvt1 = true
				}
				if evt.ID == evt2.ID {
					receivedEvt2 = true
				}
			case <-timeout:
				t.Fatal("timeout waiting for events")
			}
		}
	}
}
