package nip29

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"fiatjaf.com/nostr"
)

type GroupAddress struct {
	Relay string
	ID    string
}

func (gid GroupAddress) String() string {
	p, _ := url.Parse(gid.Relay)
	return fmt.Sprintf("%s'%s", p.Host, gid.ID)
}

func (gid GroupAddress) IsValid() bool {
	return gid.Relay != "" && gid.ID != ""
}

func (gid GroupAddress) Equals(gid2 GroupAddress) bool {
	return gid.Relay == gid2.Relay && gid.ID == gid2.ID
}

func ParseGroupAddress(raw string) (GroupAddress, error) {
	spl := strings.Split(raw, "'")
	if len(spl) != 2 {
		return GroupAddress{}, fmt.Errorf("invalid group id")
	}
	return GroupAddress{ID: spl[1], Relay: nostr.NormalizeURL(spl[0])}, nil
}

type Group struct {
	Address GroupAddress

	Name                string
	Picture             string
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

	Roles       []*Role
	InviteCodes []string

	LastMetadataUpdate            nostr.Timestamp
	LastAdminsUpdate              nostr.Timestamp
	LastMembersUpdate             nostr.Timestamp
	LastRolesUpdate               nostr.Timestamp
	LastLiveKitParticipantsUpdate nostr.Timestamp
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
func NewGroup(gadstr string) (Group, error) {
	gad, err := ParseGroupAddress(gadstr)
	if err != nil {
		return Group{}, fmt.Errorf("invalid group id '%s': %w", gadstr, err)
	}

	return Group{
		Address:             gad,
		Name:                gad.ID,
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
