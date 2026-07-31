package nip29

import (
	"testing"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/require"
)

const (
	ALICE = "eadad094b75b4690e7ee7124522861b8d81d5ed92e81eb678e776d1164d1efe9"
	BOB   = "6ac475cdf30e2006ee5142559544e86f8f1b485a9c8c1f2da467996fb7fcdfe7"
	CAROL = "f81982b8b6ba354a1e09acfda348512ef93e5778847fb5f4b30fe6b0042f4b36"
	DEREK = "24a049c4e5c9cff1764c312b2e0fa59a02af235b37809180b3f2c7b2ec3dbdfd"
)

func TestGroupEventBackAndForth(t *testing.T) {
	group1, _ := NewGroup("relay.com", "xyz")
	group1.Name = "banana"
	group1.Private = true
	meta1 := group1.ToMetadataEvent()

	require.Equal(t, "xyz", meta1.Tags.GetD(), "translation of group1 to metadata event failed: %s", meta1)
	require.NotNil(t, meta1.Tags.FindWithValue("name", "banana"), "translation of group1 to metadata event failed: %s", meta1)

	hasPrivate := false
	for _, tag := range meta1.Tags {
		if len(tag) == 1 && tag[0] == "private" {
			hasPrivate = true
		}
	}
	require.True(t, hasPrivate, "translation of group1 to metadata event failed: %s", meta1)

	group2, _ := NewGroup("groups.com", "abc")
	alicePub, _ := nostr.PubKeyFromHex(ALICE)
	group2.Members[alicePub] = []*Role{{Name: "nada"}}
	admins2 := group2.ToAdminsEvent()

	require.Equal(t, "abc", admins2.Tags.GetD(), "translation of group2 to admins event failed")
	require.Equal(t, 2, len(admins2.Tags), "translation of group2 to admins event failed")
	require.True(t, admins2.Tags.FindWithValue("p", ALICE)[2] == "nada", "translation of group2 to admins event failed")

	members2 := group2.ToMembersEvent()
	require.Equal(t, 2, len(members2.Tags), "translation of group2 to members2 event failed")
	require.NotNil(t, members2.Tags.FindWithValue("p", ALICE), "translation of group2 to members2 event failed")

	group1.MergeInMembersEvent(&members2)
	require.Equal(t, 1, len(group1.Members), "merge of members2 into group1 failed")
	require.Len(t, group1.Members[alicePub], 0, "merge of members2 into group1 failed")

	group1.MergeInAdminsEvent(&admins2)
	require.Equal(t, 1, len(group1.Members), "merge of admins2 into group1 failed")

	require.Equal(t, "nada", group1.Members[alicePub][0].Name, "merge of admins2 into group1 failed")

	group2.MergeInMetadataEvent(&meta1)
	require.Equal(t, "banana", group2.Name, "merge of meta1 into group2 failed")
	require.Equal(t, "abc", group2.Address.ID, "merge of meta1 into group2 failed")
}

func TestUpdatePinList(t *testing.T) {
	group1, _ := NewGroup("relay.com", "xyz")
	id1, _ := nostr.IDFromHex("0000000000000000000000000000000000000000000000000000000000000001")
	id2, _ := nostr.IDFromHex("0000000000000000000000000000000000000000000000000000000000000002")
	alicePub, _ := nostr.PubKeyFromHex(ALICE)
	group1.Pinned = []nostr.Pointer{
		nostr.EventPointer{ID: id1},
		nostr.EntityPointer{Kind: 30023, PublicKey: alicePub, Identifier: "abc"},
		nostr.EventPointer{ID: id2},
	}
	pinned1 := group1.ToPinnedEventsEvent()

	group2, _ := NewGroup("relay.com", "xyz")
	group2.MergeInPinnedEventsEvent(&pinned1)
	require.Len(t, group2.Pinned, 3, "merge of pinned1 into group2 failed")

	ep, ok := group2.Pinned[0].(nostr.EventPointer)
	require.True(t, ok)
	require.Equal(t, id1, ep.ID, "merge of pinned1 into group2 failed")

	ap, ok := group2.Pinned[1].(nostr.EntityPointer)
	require.True(t, ok)
	require.Equal(t, nostr.Kind(30023), ap.Kind, "merge of pinned1 into group2 failed")
	require.Equal(t, alicePub, ap.PublicKey, "merge of pinned1 into group2 failed")
	require.Equal(t, "abc", ap.Identifier, "merge of pinned1 into group2 failed")
}

func TestUpdatePinListAction(t *testing.T) {
	group, _ := NewGroup("relay.com", "xyz")
	id, _ := nostr.IDFromHex("0000000000000000000000000000000000000000000000000000000000000001")
	alicePub, _ := nostr.PubKeyFromHex(ALICE)

	evt := nostr.Event{
		Kind: nostr.KindSimpleGroupUpdatePinList,
		Tags: nostr.Tags{
			{"e", id.Hex()},
			{"a", "30023:" + ALICE + ":abc"},
		},
	}

	action, err := PrepareModerationAction(evt)
	require.NoError(t, err)
	action.Apply(&group)

	require.Len(t, group.Pinned, 2)
	ep, ok := group.Pinned[0].(nostr.EventPointer)
	require.True(t, ok)
	require.Equal(t, id, ep.ID)
	ap, ok := group.Pinned[1].(nostr.EntityPointer)
	require.True(t, ok)
	require.Equal(t, alicePub, ap.PublicKey)
	require.Equal(t, "abc", ap.Identifier)
}

func TestBannerInMetadata(t *testing.T) {
	group1, _ := NewGroup("relay.com", "xyz")
	group1.Banner = "https://pizza.com/banner.png"
	meta1 := group1.ToMetadataEvent()
	require.NotNil(t, meta1.Tags.FindWithValue("banner", "https://pizza.com/banner.png"))

	group2, _ := NewGroup("relay.com", "abc")
	group2.MergeInMetadataEvent(&meta1)
	require.Equal(t, "https://pizza.com/banner.png", group2.Banner)
}
