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
	return w.Store.QueryEvents(filter, w.MaxLimit)
}

func (w StorePublisher) Publish(ctx context.Context, evt nostr.Event) error {
	if evt.Kind.IsEphemeral() {
		// do not store ephemeral events
		return nil
	}

	if evt.Kind.IsRegular() {
		// regular events are just saved directly
		if err := w.SaveEvent(evt); err != nil && err != eventstore.ErrDupEvent {
			return fmt.Errorf("failed to save: %w", err)
		}
		return nil
	}

	// others are replaced
	if e := w.Store.ReplaceEvent(evt); e != nil {
		return fmt.Errorf("failed to replace: %w", e)
	}

	return nil
}
