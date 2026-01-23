package blossom

import (
	"context"
	"fmt"
	"iter"
	"net/url"
	"strconv"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"fiatjaf.com/nostr/nipb0/blossom"
)

// EventStoreBlobIndexWrapper uses fake events to keep track of what blobs we have stored and who owns them
type EventStoreBlobIndexWrapper struct {
	eventstore.Store
}

func (es EventStoreBlobIndexWrapper) Keep(
	ctx context.Context,
	blob blossom.BlobDescriptor,
	pubkey nostr.PubKey,
) error {
	next, stop := iter.Pull(
		es.Store.QueryEvents(ctx, nostr.Filter{Authors: []nostr.PubKey{pubkey}, Kinds: []nostr.Kind{24242}, Tags: nostr.TagMap{"x": []string{blob.SHA256}}}, 1),
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
				{"size", strconv.FormatInt(blob.Size, 10)},
			},
			CreatedAt: blob.Uploaded,
		}
		evt.ID = evt.GetID()
		if e := es.Store.SaveEvent(ctx, evt); e != nil {
			return fmt.Errorf("save event: %w", e)
		}
	}

	return nil
}

func (es EventStoreBlobIndexWrapper) List(ctx context.Context, pubkey nostr.PubKey, publicURL *url.URL) iter.Seq[BlobDescriptor] {
	return func(yield func(BlobDescriptor) bool) {
		for evt := range es.Store.QueryEvents(ctx, nostr.Filter{
			Authors: []nostr.PubKey{pubkey},
			Kinds:   []nostr.Kind{24242},
		}, 1000) {
			yield(es.parseEvent(evt, publicURL))
		}
	}
}

func (es EventStoreBlobIndexWrapper) Get(ctx context.Context, sha256 string, publicURL *url.URL) (*BlobDescriptor, error) {
	next, stop := iter.Pull(
		es.Store.QueryEvents(ctx, nostr.Filter{Tags: nostr.TagMap{"x": []string{sha256}}, Kinds: []nostr.Kind{24242}, Limit: 1}, 1),
	)

	defer stop()

	if evt, found := next(); found {
		bd := es.parseEvent(evt, publicURL)
		return &bd, nil
	}

	return nil, nil
}

func (es EventStoreBlobIndexWrapper) Delete(ctx context.Context, sha256 string, pubkey nostr.PubKey) error {
	next, stop := iter.Pull(
		es.Store.QueryEvents(ctx, nostr.Filter{
			Authors: []nostr.PubKey{pubkey},
			Tags:    nostr.TagMap{"x": []string{sha256}},
			Kinds:   []nostr.Kind{24242},
			Limit:   1,
		}, 1),
	)

	defer stop()

	if evt, found := next(); found {
		return es.Store.DeleteEvent(ctx, evt.ID)
	}

	return nil
}

func (es EventStoreBlobIndexWrapper) parseEvent(evt nostr.Event, publicURL *url.URL) BlobDescriptor {
	hhash := evt.Tags[0][1]
	mimetype := evt.Tags[1][1]
	ext := ExtensionByMimeType(mimetype)
	size, _ := strconv.ParseInt(evt.Tags[2][1], 10, 64)

	return blossom.BlobDescriptor{
		Uploaded: evt.CreatedAt,
		URL:      publicURL.JoinPath(hhash + ext).String(),
		SHA256:   hhash,
		Type:     mimetype,
		Size:     size,
	}
}
