package main

import (
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/slicestore"
	"fiatjaf.com/nostr/eventstore/wrappers"
	"fiatjaf.com/nostr/nip77"
)

func main() {
	ctx := context.Background()
	db := &slicestore.SliceStore{}
	db.Init()

	sk := nostr.Generate()
	local := wrappers.StorePublisher{Store: db, MaxLimit: math.MaxInt}

	for {
		for i := 0; i < 20; i++ {
			{
				evt := nostr.Event{
					Kind:      1,
					Content:   fmt.Sprintf("same old hello %d", i),
					CreatedAt: nostr.Timestamp(i),
					Tags:      nostr.Tags{},
				}
				evt.Sign(sk)
				db.SaveEvent(evt)
			}

			{
				evt := nostr.Event{
					Kind:      1,
					Content:   fmt.Sprintf("custom hello %d", i),
					CreatedAt: nostr.Now(),
					Tags:      nostr.Tags{},
				}
				evt.Sign(sk)
				db.SaveEvent(evt)
			}
		}

		err := nip77.NegentropySync(ctx,
			"ws://localhost:7777", nostr.Filter{}, local, local, nip77.SyncEventsFromIDs)
		if err != nil {
			panic(err)
		}

		data := slices.Collect(local.QueryEvents(nostr.Filter{}))
		fmt.Println("total local events:", len(data))
		time.Sleep(time.Second * 10)
	}
}
