package blossom

import (
	"context"
	"iter"

	"fiatjaf.com/nostr"
)

type BlobDescriptor struct {
	URL      string          `json:"url"`
	SHA256   string          `json:"sha256"`
	Size     int             `json:"size"`
	Type     string          `json:"type"`
	Uploaded nostr.Timestamp `json:"uploaded"`

	Owner nostr.PubKey `json:"-"`
}

type BlobIndex interface {
	Keep(ctx context.Context, blob BlobDescriptor, pubkey nostr.PubKey) error
	List(ctx context.Context, pubkey nostr.PubKey, publicURL string) iter.Seq[BlobDescriptor]
	Get(ctx context.Context, sha256 string, publicURL string) (*BlobDescriptor, error)
	Delete(ctx context.Context, sha256 string, pubkey nostr.PubKey) error
}

var (
	_ BlobIndex = (*EventStoreBlobIndexWrapper)(nil)
	_ BlobIndex = (*MemoryBlobIndex)(nil)
)
