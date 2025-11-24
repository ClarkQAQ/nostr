package bleve

import (
	"errors"
	"fmt"
	"sync"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	bleve "github.com/blevesearch/bleve/v2"
	bleveMapping "github.com/blevesearch/bleve/v2/mapping"
)

var _ eventstore.Store = (*BleveBackend)(nil)

type BleveBackend struct {
	sync.Mutex
	// Path is where the index will be saved
	Path string

	// RawEventStore is where we'll fetch the raw events from
	// bleve will only store ids, so the actual events must be somewhere else
	RawEventStore eventstore.Store

	index bleve.Index
}

func (b *BleveBackend) Close() {
	if b.index != nil {
		b.index.Close()
	}
}

func (b *BleveBackend) Init() error {
	if b.Path == "" {
		return fmt.Errorf("missing Path")
	}
	if b.RawEventStore == nil {
		return fmt.Errorf("missing RawEventStore")
	}

	// try to open existing index
	index, err := bleve.Open(b.Path)
	if err == bleve.ErrorIndexPathDoesNotExist {
		// create new index with default mapping
		mapping := bleveMapping.NewIndexMapping()
		index, err = bleve.New(b.Path, mapping)
		if err != nil {
			return fmt.Errorf("error creating index: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("error opening index: %w", err)
	}

	b.index = index
	return nil
}

func (b *BleveBackend) CountEvents(nostr.Filter) (uint32, error) {
	return 0, errors.New("not supported")
}
