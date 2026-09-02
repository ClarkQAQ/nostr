package nip29

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
)

type GroupAddress struct {
	// URL of the relay that is hosting the group
	Relay string

	// Group identifier ("d"/"h" tag)
	ID string

	// Public key of the relay, used to publish kind:39000/etc events
	Self nostr.PubKey
}

func (gid GroupAddress) String() string {
	p, _ := url.Parse(gid.Relay)
	return fmt.Sprintf("%s'%s", p.Host, gid.ID)
}

func (gid GroupAddress) Code() string {
	return nip19.EncodeNaddr(gid.Self, 39000, gid.ID, []string{gid.Relay})
}

func (gid GroupAddress) IsValid() bool {
	return gid.Relay != "" && gid.ID != ""
}

func (gid GroupAddress) Equals(gid2 GroupAddress) bool {
	return gid.Relay == gid2.Relay && gid.ID == gid2.ID
}

type Group struct {
	Address GroupAddress

	Name                string
	Picture             string
	Banner              string
	About               string
	Members             map[nostr.PubKey][]*Role
	LiveKitParticipants []nostr.PubKey

	// indicates that only members can read group messages
	Private bool

	// indicates that only members can write messages to the group
	Restricted bool

	// indicates that join requests are ignored unless they include an invite code
	Closed bool

	// indicates that relays should hide group metadata from non-members
	Hidden bool

	// indicates that the group supports audio/video live chat
	LiveKit bool

	// indicates which event kinds this group supports
	SupportedKinds []nostr.Kind

	// arbitrary string indicating the parent group
	Parent string

	// ordered list of identifiers of child groups
	Children []string

	Roles       []*Role
	InviteCodes []string

	Pinned []nostr.Pointer

	LastMetadataUpdate            nostr.Timestamp
	LastAdminsUpdate              nostr.Timestamp
	LastMembersUpdate             nostr.Timestamp
	LastRolesUpdate               nostr.Timestamp
	LastLiveKitParticipantsUpdate nostr.Timestamp
	LastPinnedEventsUpdate        nostr.Timestamp
}

func (group Group) String() string {
	maybePrivate := ""
	maybeRestricted := ""
	maybeHidden := ""
	maybeClosed := ""

	if group.Private {
		maybePrivate = " private"
	}
	if group.Restricted {
		maybeRestricted = " restricted"
	}
	if group.Hidden {
		maybeHidden = " hidden"
	}
	if group.Closed {
		maybeClosed = " closed"
	}

	maybeLiveKit := ""
	if group.LiveKit {
		maybeLiveKit = " livekit"
	}

	members := make([]string, len(group.Members))
	i := 0
	for pubkey, roles := range group.Members {
		members[i] = pubkey.Hex()
		if len(roles) > 0 {
			members[i] += ":"
		}
		for _, role := range roles {
			members[i] += role.Name
			if slices.Contains(group.Roles, role) {
				members[i] += "*"
			}
			members[i] += "/"
		}
		members[i] = strings.TrimRight(members[i], "/")
		i++
	}

	return fmt.Sprintf(`<Group %s name="%s"%s%s%s%s%s picture="%s" about="%s" members=[%v]>`,
		group.Address,
		group.Name,
		maybePrivate,
		maybeRestricted,
		maybeHidden,
		maybeClosed,
		maybeLiveKit,
		group.Picture,
		group.About,
		strings.Join(members, " "),
	)
}

// NewGroup takes a group address in the form "<id>'<relay-hostname>"
func NewGroup(relayHost, groupId string) (Group, error) {
	relayHost = nostr.NormalizeURL(relayHost)

	return Group{
		Address: GroupAddress{
			Relay: relayHost,
			ID:    groupId,
		},
		Name:                groupId,
		Members:             make(map[nostr.PubKey][]*Role),
		LiveKitParticipants: make([]nostr.PubKey, 0),
	}, nil
}

func NewGroupFromMetadataEvent(relayURL string, evt *nostr.Event) (Group, error) {
	g := Group{
		Address: GroupAddress{
			Relay: relayURL,
			ID:    evt.Tags.GetD(),
		},
		Name:                evt.Tags.GetD(),
		Members:             make(map[nostr.PubKey][]*Role),
		LiveKitParticipants: make([]nostr.PubKey, 0),
	}

	err := g.MergeInMetadataEvent(evt)
	return g, err
}

func (group Group) ToMetadataEvent() nostr.Event {
	evt := nostr.Event{
		Kind:      nostr.KindSimpleGroupMetadata,
		CreatedAt: group.LastMetadataUpdate,
		Tags: nostr.Tags{
			nostr.Tag{"d", group.Address.ID},
		},
	}
	if group.Name != "" {
		evt.Tags = append(evt.Tags, nostr.Tag{"name", group.Name})
	}
	if group.About != "" {
		evt.Tags = append(evt.Tags, nostr.Tag{"about", group.About})
	}
	if group.Picture != "" {
		evt.Tags = append(evt.Tags, nostr.Tag{"picture", group.Picture})
	}
	if group.Banner != "" {
		evt.Tags = append(evt.Tags, nostr.Tag{"banner", group.Banner})
	}

	// status
	if group.Private {
		evt.Tags = append(evt.Tags, nostr.Tag{"private"})
	}
	if group.Restricted {
		evt.Tags = append(evt.Tags, nostr.Tag{"restricted"})
	}
	if group.Hidden {
		evt.Tags = append(evt.Tags, nostr.Tag{"hidden"})
	}
	if group.Closed {
		evt.Tags = append(evt.Tags, nostr.Tag{"closed"})
	}
	if group.LiveKit {
		evt.Tags = append(evt.Tags, nostr.Tag{"livekit"})
	}

	if group.SupportedKinds != nil {
		tag := make(nostr.Tag, 1, 1+len(group.SupportedKinds))
		tag[0] = "supported_kinds"
		for _, kind := range group.SupportedKinds {
			tag = append(tag, strconv.Itoa(int(kind)))
		}
		evt.Tags = append(evt.Tags, tag)
	}

	if group.Parent != "" {
		evt.Tags = append(evt.Tags, nostr.Tag{"parent", group.Parent})
	}

	for _, child := range group.Children {
		evt.Tags = append(evt.Tags, nostr.Tag{"child", child})
	}

	return evt
}

func (group Group) ToAdminsEvent() nostr.Event {
	evt := nostr.Event{
		Kind:      nostr.KindSimpleGroupAdmins,
		CreatedAt: group.LastAdminsUpdate,
		Tags:      make(nostr.Tags, 1, 1+len(group.Members)/3),
	}
	evt.Tags[0] = nostr.Tag{"d", group.Address.ID}

	for member, roles := range group.Members {
		if len(roles) == 0 {
			// is not an admin
			continue
		}

		// is an admin
		tag := make([]string, 2, 2+len(roles))
		tag[0] = "p"
		tag[1] = member.Hex()
		for _, role := range roles {
			tag = append(tag, role.Name)
		}
		evt.Tags = append(evt.Tags, tag)
	}

	return evt
}

func (group Group) ToMembersEvent() nostr.Event {
	evt := nostr.Event{
		Kind:      nostr.KindSimpleGroupMembers,
		CreatedAt: group.LastMembersUpdate,
		Tags:      make(nostr.Tags, 1, 1+len(group.Members)),
	}
	evt.Tags[0] = nostr.Tag{"d", group.Address.ID}

	for member := range group.Members {
		// include both admins and normal members
		evt.Tags = append(evt.Tags, nostr.Tag{"p", member.Hex()})
	}

	return evt
}

func (group Group) ToRolesEvent() nostr.Event {
	evt := nostr.Event{
		Kind:      nostr.KindSimpleGroupRoles,
		CreatedAt: group.LastRolesUpdate,
		Tags:      make(nostr.Tags, 1, 1+len(group.Members)),
	}
	evt.Tags[0] = nostr.Tag{"d", group.Address.ID}

	for _, role := range group.Roles {
		// include both admins and normal members
		evt.Tags = append(evt.Tags, nostr.Tag{"role", role.Name, role.Description})
	}

	return evt
}

func (group Group) ToLiveKitParticipantsEvent() nostr.Event {
	evt := nostr.Event{
		Kind:      nostr.KindSimpleGroupLiveKitParticipants,
		CreatedAt: group.LastLiveKitParticipantsUpdate,
		Tags:      make(nostr.Tags, 1, 1+len(group.LiveKitParticipants)),
	}
	evt.Tags[0] = nostr.Tag{"d", group.Address.ID}

	for _, member := range group.LiveKitParticipants {
		tag := nostr.Tag{"participant", member.Hex()}
		evt.Tags = append(evt.Tags, tag)
	}

	return evt
}

func (group Group) ToPinnedEventsEvent() nostr.Event {
	evt := nostr.Event{
		Kind:      nostr.KindSimpleGroupPinnedEvents,
		CreatedAt: group.LastPinnedEventsUpdate,
		Tags:      make(nostr.Tags, 1, 1+len(group.Pinned)),
	}
	evt.Tags[0] = nostr.Tag{"d", group.Address.ID}

	for _, pointer := range group.Pinned {
		evt.Tags = append(evt.Tags, pointer.AsTag())
	}

	return evt
}

func (group *Group) MergeInMetadataEvent(evt *nostr.Event) error {
	if evt.Kind != nostr.KindSimpleGroupMetadata {
		return fmt.Errorf("expected kind %d, got %d", nostr.KindSimpleGroupMetadata, evt.Kind)
	}
	if evt.CreatedAt < group.LastMetadataUpdate {
		return fmt.Errorf("event is older than our last update (%d vs %d)", evt.CreatedAt, group.LastMetadataUpdate)
	}

	group.LastMetadataUpdate = evt.CreatedAt
	group.Name = group.Address.ID

	for _, tag := range evt.Tags {
		if len(tag) >= 1 {
			switch tag[0] {
			case "private":
				group.Private = true
			case "restricted":
				group.Restricted = true
			case "closed":
				group.Closed = true
			case "hidden":
				group.Hidden = true
			case "livekit":
				group.LiveKit = true
			case "supported_kinds":
				kinds := make([]nostr.Kind, 0, len(tag)-1)
				for _, raw := range tag[1:] {
					kind, err := strconv.Atoi(raw)
					if err != nil {
						continue
					}
					kinds = append(kinds, nostr.Kind(kind))
				}
				group.SupportedKinds = kinds
			default:
				if len(tag) >= 2 {
					switch tag[0] {
					case "name":
						group.Name = tag[1]
					case "about":
						group.About = tag[1]
					case "picture":
						group.Picture = tag[1]
					case "banner":
						group.Banner = tag[1]
					case "parent":
						group.Parent = tag[1]
					case "child":
						group.Children = append(group.Children, tag[1])
					}
				}
			}
		}
	}

	return nil
}

func (group *Group) MergeInAdminsEvent(evt *nostr.Event) error {
	if evt.Kind != nostr.KindSimpleGroupAdmins {
		return fmt.Errorf("expected kind %d, got %d", nostr.KindSimpleGroupAdmins, evt.Kind)
	}
	if evt.CreatedAt < group.LastAdminsUpdate {
		return fmt.Errorf("event is older than our last update (%d vs %d)", evt.CreatedAt, group.LastAdminsUpdate)
	}

	group.LastAdminsUpdate = evt.CreatedAt
	for _, tag := range evt.Tags {
		if len(tag) < 3 {
			continue
		}
		if tag[0] != "p" {
			continue
		}

		member, err := nostr.PubKeyFromHex(tag[1])
		if err != nil {
			continue
		}

		for _, roleName := range tag[2:] {
			group.Members[member] = append(group.Members[member], group.GetRoleByName(roleName))
		}
	}

	return nil
}

func (group *Group) MergeInMembersEvent(evt *nostr.Event) error {
	if evt.Kind != nostr.KindSimpleGroupMembers {
		return fmt.Errorf("expected kind %d, got %d", nostr.KindSimpleGroupMembers, evt.Kind)
	}
	if evt.CreatedAt < group.LastMembersUpdate {
		return fmt.Errorf("event is older than our last update (%d vs %d)", evt.CreatedAt, group.LastMembersUpdate)
	}

	group.LastMembersUpdate = evt.CreatedAt
	for _, tag := range evt.Tags {
		if len(tag) < 2 {
			continue
		}
		if tag[0] != "p" {
			continue
		}

		member, err := nostr.PubKeyFromHex(tag[1])
		if err != nil {
			continue
		}

		_, exists := group.Members[member]
		if !exists {
			group.Members[member] = nil
		}
	}

	return nil
}

func (group *Group) MergeInRolesEvent(evt *nostr.Event) error {
	if evt.Kind != nostr.KindSimpleGroupRoles {
		return fmt.Errorf("expected kind %d, got %d", nostr.KindSimpleGroupRoles, evt.Kind)
	}
	if evt.CreatedAt < group.LastRolesUpdate {
		return fmt.Errorf("event is older than our last update (%d vs %d)", evt.CreatedAt, group.LastRolesUpdate)
	}

	group.LastRolesUpdate = evt.CreatedAt
	for _, tag := range evt.Tags {
		if len(tag) < 2 {
			continue
		}
		if tag[0] != "role" {
			continue
		}

		roleName := tag[1]
		roleDescription := ""
		if len(tag) >= 3 {
			roleDescription = tag[2]
		}

		if idx := slices.IndexFunc(group.Roles, func(role *Role) bool { return role.Name == roleName }); idx >= 0 {
			// update existing role description
			group.Roles[idx].Description = roleDescription
		} else {
			// add new role
			group.Roles = append(group.Roles, &Role{Name: roleName, Description: roleDescription})
		}
	}

	return nil
}

func (group *Group) MergeInLiveKitParticipantsEvent(evt *nostr.Event) error {
	if evt.Kind != nostr.KindSimpleGroupLiveKitParticipants {
		return fmt.Errorf("expected kind %d, got %d", nostr.KindSimpleGroupLiveKitParticipants, evt.Kind)
	}
	if evt.CreatedAt < group.LastLiveKitParticipantsUpdate {
		return fmt.Errorf("event is older than our last update (%d vs %d)", evt.CreatedAt, group.LastLiveKitParticipantsUpdate)
	}

	group.LastLiveKitParticipantsUpdate = evt.CreatedAt
	group.LiveKitParticipants = make([]nostr.PubKey, 0, len(evt.Tags))
	for _, tag := range evt.Tags {
		if len(tag) < 2 {
			continue
		}
		if tag[0] != "participant" {
			continue
		}

		member, err := nostr.PubKeyFromHex(tag[1])
		if err != nil {
			continue
		}
		if slices.Contains(group.LiveKitParticipants, member) {
			continue
		}
		group.LiveKitParticipants = append(group.LiveKitParticipants, member)
	}

	return nil
}

func (group *Group) MergeInPinnedEventsEvent(evt *nostr.Event) error {
	if evt.Kind != nostr.KindSimpleGroupPinnedEvents {
		return fmt.Errorf("expected kind %d, got %d", nostr.KindSimpleGroupPinnedEvents, evt.Kind)
	}
	if evt.CreatedAt < group.LastPinnedEventsUpdate {
		return fmt.Errorf("event is older than our last update (%d vs %d)", evt.CreatedAt, group.LastPinnedEventsUpdate)
	}

	group.LastPinnedEventsUpdate = evt.CreatedAt
	group.Pinned = nil
	for _, tag := range evt.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "e":
			pointer, err := nostr.EventPointerFromTag(tag)
			if err != nil {
				continue
			}
			group.Pinned = append(group.Pinned, pointer)
		case "a":
			pointer, err := nostr.EntityPointerFromTag(tag)
			if err != nil {
				continue
			}
			group.Pinned = append(group.Pinned, pointer)
		}
	}

	return nil
}
