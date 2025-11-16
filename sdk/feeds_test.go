package sdk

import (
	"context"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/slicestore"
	"fiatjaf.com/nostr/relay"
	"github.com/stretchr/testify/require"
)

func TestStreamLiveFeed(t *testing.T) {
	ctx := context.Background()

	// start 3 local relays
	relay1 := relay.NewRelay()
	relay2 := relay.NewRelay()
	relay3 := relay.NewRelay()

	for _, r := range []*relay.Relay{relay1, relay2, relay3} {
		db := &slicestore.SliceStore{}
		db.Init()
		r.UseEventstore(db, 4000)
		defer db.Close()
	}

	s1 := make(chan bool)
	s2 := make(chan bool)
	s3 := make(chan bool)

	go func() {
		err := relay1.Start("127.0.0.1", 48481, s1)
		require.NoError(t, err)
	}()
	go func() {
		err := relay2.Start("127.0.0.1", 48482, s2)
		require.NoError(t, err)
	}()
	go func() {
		err := relay3.Start("127.0.0.1", 48483, s3)
		require.NoError(t, err)
	}()

	defer relay1.Shutdown(ctx)
	defer relay2.Shutdown(ctx)
	defer relay3.Shutdown(ctx)

	<-s1
	<-s2
	<-s3

	// generate two random keypairs for testing
	sk1 := nostr.Generate()
	pk1 := nostr.GetPublicKey(sk1)
	sk2 := nostr.Generate()
	pk2 := nostr.GetPublicKey(sk2)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// first publish relay lists to relay1 for both users
	relayListEvt1 := nostr.Event{
		PubKey:    pk1,
		CreatedAt: nostr.Now(),
		Kind:      10002,
		Tags: nostr.Tags{
			{"r", "ws://localhost:48482", "write"},
			{"r", "ws://localhost:48483", "write"},
		},
		Content: "",
	}
	relayListEvt1.Sign(sk1)

	relayListEvt2 := nostr.Event{
		PubKey:    pk2,
		CreatedAt: nostr.Now(),
		Kind:      10002,
		Tags: nostr.Tags{
			{"r", "ws://localhost:48482", "write"},
			{"r", "ws://localhost:48483", "write"},
		},
		Content: "",
	}
	relayListEvt2.Sign(sk2)

	// publish relay lists to relay1
	relay, err := nostr.RelayConnect(ctx, "ws://localhost:48481", nostr.RelayOptions{})
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
	sys.RelayListRelays = NewRelayStream("ws://localhost:48481")
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
	go sys.Pool.PublishMany(ctx, []string{"ws://localhost:48482", "ws://localhost:48483"}, evt1)
	go sys.Pool.PublishMany(ctx, []string{"ws://localhost:48482", "ws://localhost:48483"}, evt2)

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
		go sys.Pool.PublishMany(ctx, []string{"ws://localhost:48482", "ws://localhost:48483"}, evt1)
		go sys.Pool.PublishMany(ctx, []string{"ws://localhost:48482", "ws://localhost:48483"}, evt2)

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
