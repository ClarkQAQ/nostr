package betterbinary

import (
	"encoding/binary"

	"fiatjaf.com/nostr"
)

func GetKind(evtb []byte) nostr.Kind {
	return nostr.Kind(binary.LittleEndian.Uint64(evtb[1:9]))
}

func GetID(evtb []byte) nostr.ID {
	return nostr.ID(evtb[17:49])
}

func GetPubKey(evtb []byte) nostr.PubKey {
	return nostr.PubKey(evtb[49:81])
}

func GetCreatedAt(evtb []byte) nostr.Timestamp {
	return nostr.Timestamp(binary.LittleEndian.Uint64(evtb[9:17]))
}
