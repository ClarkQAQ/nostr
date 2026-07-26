package nullstore

import (
	"context"
	"iter"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
)

var _ eventstore.Store = NullStore{}

type NullStore struct{}

func (b NullStore) Init(ctx context.Context) error {
	return nil
}

func (b NullStore) Close() error { return nil }

func (b NullStore) DeleteEvent(ctx context.Context, id nostr.ID) error {
	return nil
}

func (b NullStore) QueryEvents(ctx context.Context, filter nostr.Filter, maxLimit int) iter.Seq2[nostr.Event, error] {
	return func(yield func(nostr.Event, error) bool) {}
}

func (b NullStore) SaveEvent(ctx context.Context, evt nostr.Event) error {
	return nil
}

func (b NullStore) ReplaceEvent(ctx context.Context, evt nostr.Event) ([]nostr.Event, error) {
	return nil, nil
}

func (b NullStore) CountEvents(ctx context.Context, filter nostr.Filter) (int64, error) {
	return 0, nil
}
