package bleve

import (
	"fmt"
	"strconv"

	"fiatjaf.com/nostr"
)

func (b *BleveBackend) SaveEvent(evt nostr.Event) error {
	doc := map[string]interface{}{
		contentField:   evt.Content,
		kindField:      strconv.Itoa(int(evt.Kind)),
		pubkeyField:    evt.PubKey.Hex()[56:],
		createdAtField: float64(evt.CreatedAt),
	}

	if err := b.index.Index(evt.ID.Hex(), doc); err != nil {
		return fmt.Errorf("failed to index '%s' document: %w", evt.ID, err)
	}

	return nil
}
