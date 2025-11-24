package bleve

import (
	"fiatjaf.com/nostr"
)

func (b *BleveBackend) DeleteEvent(id nostr.ID) error {
	return b.index.Delete(id.Hex())
}
