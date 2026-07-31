package nip29

import (
	"slices"

	"fiatjaf.com/nostr"
)

type Role struct {
	Name        string
	Description string
}

type KindRange []nostr.Kind

var ModerationEventKinds = KindRange{
	nostr.KindSimpleGroupPutUser,
	nostr.KindSimpleGroupRemoveUser,
	nostr.KindSimpleGroupEditMetadata,
	nostr.KindSimpleGroupDeleteEvent,
	nostr.KindSimpleGroupCreateGroup,
	nostr.KindSimpleGroupDeleteGroup,
	nostr.KindSimpleGroupCreateInvite,
	nostr.KindSimpleGroupUpdatePinList,
}

var MetadataEventKinds = KindRange{
	nostr.KindSimpleGroupMetadata,
	nostr.KindSimpleGroupAdmins,
	nostr.KindSimpleGroupMembers,
	nostr.KindSimpleGroupRoles,
	nostr.KindSimpleGroupLiveKitParticipants,
	nostr.KindSimpleGroupPinnedEvents,
}

func (kr KindRange) Includes(kind nostr.Kind) bool {
	_, ok := slices.BinarySearch(kr, kind)
	return ok
}
