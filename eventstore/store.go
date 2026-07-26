package eventstore

import (
	"context"
	"iter"

	"fiatjaf.com/nostr"
)

// Store is a persistence layer for nostr events handled by a relay.
type Store interface {
	// Init is called at the very beginning by [Server.Start], after [Relay.Init],
	// allowing a storage to initialize its internal resources.
	Init(context.Context) error

	// Close must be called after you're done using the store, to free up resources and so on.
	Close() error

	// QueryEvents returns events that match the filter
	QueryEvents(ctx context.Context, filter nostr.Filter, maxLimit int) iter.Seq2[nostr.Event, error]

	// DeleteEvent deletes an event atomically by ID
	DeleteEvent(context.Context, nostr.ID) error

	// SaveEvent just saves an event, no side-effects.
	SaveEvent(context.Context, nostr.Event) error

	// ReplaceEvent atomically replaces a replaceable or addressable event.
	// Conceptually it is like a Query->Delete->Save, but streamlined.
	ReplaceEvent(context.Context, nostr.Event) (deleted []nostr.Event, e error)

	// CountEvents counts all events that match a given filter
	CountEvents(context.Context, nostr.Filter) (int64, error)
}
