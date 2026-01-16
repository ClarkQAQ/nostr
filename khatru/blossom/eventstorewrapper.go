package blossom

import (
	"context"
	"iter"
	"strconv"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"fiatjaf.com/nostr/nipb0/blossom"
)

// EventStoreBlobIndexWrapper uses fake events to keep track of what blobs we have stored and who owns them
type EventStoreBlobIndexWrapper struct {
	eventstore.Store

	ServiceURL string
}

func (es EventStoreBlobIndexWrapper) Keep(
	ctx context.Context,
	blob blossom.BlobDescriptor,
	pubkey nostr.PubKey,
) error {
	next, stop := iter.Pull(
		es.Store.QueryEvents(nostr.Filter{Authors: []nostr.PubKey{pubkey}, Kinds: []nostr.Kind{24242}, Tags: nostr.TagMap{"x": []string{blob.SHA256}}}, 1),
	)
	defer stop()

	if _, exists := next(); !exists {
		// doesn't exist, save
		evt := nostr.Event{
			PubKey: pubkey,
			Kind:   24242,
			Tags: nostr.Tags{
				{"x", blob.SHA256},
				{"type", blob.Type},
				{"size", strconv.Itoa(blob.Size)},
			},
			CreatedAt: blob.Uploaded,
		}
		evt.ID = evt.GetID()
		es.Store.SaveEvent(evt)
	}

	return nil
}

func (es EventStoreBlobIndexWrapper) List(ctx context.Context, pubkey nostr.PubKey) iter.Seq[blossom.BlobDescriptor] {
	return func(yield func(blossom.BlobDescriptor) bool) {
		for evt := range es.Store.QueryEvents(nostr.Filter{
			Authors: []nostr.PubKey{pubkey},
			Kinds:   []nostr.Kind{24242},
		}, 1000) {
			if !yield(es.parseEvent(evt)) {
				return
			}
		}
	}
}

func (es EventStoreBlobIndexWrapper) Get(ctx context.Context, sha256 string) (*blossom.BlobDescriptor, error) {
	next, stop := iter.Pull(
		es.Store.QueryEvents(nostr.Filter{Tags: nostr.TagMap{"x": []string{sha256}}, Kinds: []nostr.Kind{24242}, Limit: 1}, 1),
	)

	defer stop()

	if evt, found := next(); found {
		bd := es.parseEvent(evt)
		return &bd, nil
	}

	return nil, nil
}

func (es EventStoreBlobIndexWrapper) Delete(ctx context.Context, sha256 string, pubkey nostr.PubKey) error {
	next, stop := iter.Pull(
		es.Store.QueryEvents(nostr.Filter{
			Authors: []nostr.PubKey{pubkey},
			Tags:    nostr.TagMap{"x": []string{sha256}},
			Kinds:   []nostr.Kind{24242},
			Limit:   1,
		}, 1),
	)

	defer stop()

	if evt, found := next(); found {
		return es.Store.DeleteEvent(evt.ID)
	}

	return nil
}

func (es EventStoreBlobIndexWrapper) parseEvent(evt nostr.Event) blossom.BlobDescriptor {
	hhash := evt.Tags[0][1]
	mimetype := evt.Tags[1][1]
	ext := blossom.GetExtension(mimetype)
	size, _ := strconv.Atoi(evt.Tags[2][1])

	return blossom.BlobDescriptor{
		Uploaded: evt.CreatedAt,
		URL:      es.ServiceURL + "/" + hhash + ext,
		SHA256:   hhash,
		Type:     mimetype,
		Size:     size,
	}
}
