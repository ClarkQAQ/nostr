package blossom

import (
	"context"
	"iter"
	"net/url"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nipb7/blossom"
)

type BlobIndex interface {
	Keep(ctx context.Context, blob blossom.BlobDescriptor, pubkey nostr.PubKey) error
	List(ctx context.Context, pubkey nostr.PubKey, publicURL *url.URL) iter.Seq[blossom.BlobDescriptor]
	Get(ctx context.Context, sha256 string, publicURL *url.URL) (*blossom.BlobDescriptor, error)
	Delete(ctx context.Context, sha256 string, pubkey nostr.PubKey) error
}

var (
	_ BlobIndex = (*EventStoreBlobIndexWrapper)(nil)
	_ BlobIndex = (*MemoryBlobIndex)(nil)
)
