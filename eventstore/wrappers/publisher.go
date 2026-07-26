package wrappers

import (
	"context"
	"fmt"
	"iter"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
)

var _ nostr.Publisher = StorePublisher{}

type StorePublisher struct {
	eventstore.Store
	MaxLimit int
}

func (w StorePublisher) QueryEvents(filter nostr.Filter) iter.Seq[nostr.Event] {
	return eventstore.EventsOnly(w.Store.QueryEvents(context.Background(), filter, w.MaxLimit))
}

func (w StorePublisher) Publish(ctx context.Context, evt nostr.Event) error {
	if evt.Kind.IsEphemeral() {
		// do not store ephemeral events
		return nil
	}

	if evt.Kind.IsRegular() {
		// regular events are just saved directly
		if err := w.SaveEvent(context.Background(), evt); err != nil && err != eventstore.ErrDupEvent {
			return fmt.Errorf("failed to save: %w", err)
		} else {
			return err
		}
	}

	// others are replaced
	if _, e := w.Store.ReplaceEvent(context.Background(), evt); e != nil {
		return fmt.Errorf("failed to replace: %w", e)
	}

	return nil
}
