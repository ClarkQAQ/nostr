package blossom

import (
	"context"
	"iter"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nipb0/blossom"
)

type BlobIndex interface {
	Keep(ctx context.Context, blob blossom.BlobDescriptor, pubkey nostr.PubKey) error
	List(ctx context.Context, pubkey nostr.PubKey) iter.Seq[blossom.BlobDescriptor]
	Get(ctx context.Context, sha256 string) (*blossom.BlobDescriptor, error)
	Delete(ctx context.Context, sha256 string, pubkey nostr.PubKey) error

	ListAllBlobs(ctx context.Context) iter.Seq2[nostr.PubKey, blossom.BlobDescriptor]
	OwnersForBlob(ctx context.Context, sha256 string) []nostr.PubKey
}

var (
	_ BlobIndex = (*EventStoreBlobIndexWrapper)(nil)
	_ BlobIndex = (*MemoryBlobIndex)(nil)
)
