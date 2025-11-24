package nip10

import (
	"testing"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/require"
)

func TestGetThreadRoot(t *testing.T) {
	event1 := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	event2 := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

	// test with root tag
	tags := nostr.Tags{
		{"e", event1, "relay1", "author1", "root"},
		{"e", event2, "relay2", "author2", "reply"},
	}
	root := GetThreadRoot(tags)
	require.NotNil(t, root)
	ep := root.(nostr.EventPointer)
	require.Equal(t, event1, ep.ID.Hex())

	// test fallback to first e tag
	tags2 := nostr.Tags{
		{"e", event2, "relay2", "author2"},
		{"p", "pubkey1"},
	}
	root2 := GetThreadRoot(tags2)
	require.NotNil(t, root2)
	ep2 := root2.(nostr.EventPointer)
	require.Equal(t, event2, ep2.ID.Hex())

	// test no e tags
	tags3 := nostr.Tags{
		{"p", "pubkey1"},
	}
	root3 := GetThreadRoot(tags3)
	require.Nil(t, root3)
}

func TestGetImmediateParent(t *testing.T) {
	event1 := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	event2 := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	pubkey := "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"

	// test with reply tag
	tags := nostr.Tags{
		{"e", event1, "relay1", "author1", "root"},
		{"e", event2, "relay2", "author2", "reply"},
	}
	parent := GetImmediateParent(tags)
	require.NotNil(t, parent)
	ep := parent.(nostr.EventPointer)
	require.Equal(t, event2, ep.ID.Hex())

	// test with parent tag
	tags2 := nostr.Tags{
		{"e", event1, "relay1", "author1"},
		{"e", event2, "relay2", "author2"},
	}
	parent2 := GetImmediateParent(tags2)
	require.NotNil(t, parent2)
	ep2 := parent2.(nostr.EventPointer)
	require.Equal(t, event2, ep2.ID.Hex())

	// test with mention (should skip)
	tags3 := nostr.Tags{
		{"e", event1, "relay1", "author1", "mention"},
		{"e", event2, "relay2", "author2"},
	}
	parent3 := GetImmediateParent(tags3)
	require.NotNil(t, parent3)
	ep3 := parent3.(nostr.EventPointer)
	require.Equal(t, event2, ep3.ID.Hex()) // last e

	// test with a tag
	tags4 := nostr.Tags{
		{"a", "1:" + pubkey + ":d", "relay", "author", "reply"},
	}
	parent4 := GetImmediateParent(tags4)
	require.NotNil(t, parent4)
	ap := parent4.(nostr.EntityPointer)
	require.Equal(t, "1:"+pubkey+":d", ap.AsTagReference())

	// test no valid tags
	tags5 := nostr.Tags{
		{"p", "pubkey1"},
	}
	parent5 := GetImmediateParent(tags5)
	require.Nil(t, parent5)
}
