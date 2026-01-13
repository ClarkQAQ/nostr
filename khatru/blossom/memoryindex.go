package blossom

import (
	"context"
	"errors"
	"iter"
	"slices"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nipb0/blossom"
	"github.com/puzpuzpuz/xsync/v3"
)

type ownedBlob struct {
	blob   blossom.BlobDescriptor
	owners []nostr.PubKey
}

type MemoryBlobIndex struct {
	m *xsync.MapOf[string, ownedBlob]
}

func NewMemoryBlobIndex() MemoryBlobIndex {
	return MemoryBlobIndex{
		m: xsync.NewMapOf[string, ownedBlob](),
	}
}

func (x MemoryBlobIndex) Keep(ctx context.Context, blob blossom.BlobDescriptor, pubkey nostr.PubKey) error {
	x.m.Compute(blob.SHA256, func(oldValue ownedBlob, loaded bool) (newValue ownedBlob, delete bool) {
		if loaded {
			newValue = oldValue
			if !slices.Contains(newValue.owners, pubkey) {
				newValue.owners = append(newValue.owners, pubkey)
			}
		} else {
			newValue = ownedBlob{
				blob:   blob,
				owners: []nostr.PubKey{pubkey},
			}
		}

		return newValue, false
	})

	return nil
}

func (x MemoryBlobIndex) List(ctx context.Context, pubkey nostr.PubKey) iter.Seq[blossom.BlobDescriptor] {
	return func(yield func(blossom.BlobDescriptor) bool) {
		x.m.Range(func(key string, value ownedBlob) bool {
			if slices.Contains(value.owners, pubkey) {
				if !yield(value.blob) {
					return false
				}
			}
			return true
		})
	}
}

func (x MemoryBlobIndex) Get(ctx context.Context, sha256 string) (*blossom.BlobDescriptor, error) {
	if val, ok := x.m.Load(sha256); ok {
		return &val.blob, nil
	}
	return nil, errors.New("not found")
}

func (x MemoryBlobIndex) Delete(ctx context.Context, sha256 string, pubkey nostr.PubKey) error {
	x.m.Compute(sha256, func(oldValue ownedBlob, loaded bool) (newValue ownedBlob, delete bool) {
		if loaded {
			if idx := slices.Index(oldValue.owners, pubkey); idx != -1 {
				if len(oldValue.owners) == 1 {
					// this is the only owner, remove the blob
					return oldValue, true
				} else {
					// remove this owner
					oldValue.owners[idx] = oldValue.owners[len(oldValue.owners)-1]
					oldValue.owners = oldValue.owners[0 : len(oldValue.owners)-1]
					return oldValue, false
				}
			}
		}

		return oldValue, true
	})

	return nil
}
