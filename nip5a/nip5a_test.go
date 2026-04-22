package nip5a

import (
	"encoding/hex"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSiteManifest(t *testing.T) {
	pubkey := nostr.MustPubKeyFromHex("266815e0c9210dfa324c6cba3573b14bee49da4209a9456f9484e5106cd408a5")

	t.Run("root site", func(t *testing.T) {
		event := &nostr.Event{
			Kind:   nostr.KindNsiteRoot,
			PubKey: pubkey,
			Tags: nostr.Tags{
				{"path", "/index.html", "186ea5fd14e88fd1ac49351759e7ab906fa94892002b60bf7f5a428f28ca1c99"},
				{"path", "/about.html", "a1b2c3d4e5f6789012345678901234567890abcdef1234567890abcdef123456"},
				{"path", "/favicon.ico", "fedcba0987654321fedcba0987654321fedcba0987654321fedcba0987654321"},
				{"server", "https://blossom.example.com"},
				{"title", "My Nostr Site"},
				{"description", "A static website hosted on Nostr"},
				{"source", "https://github.com/example/my-nostr-site"},
			},
		}

		sm, err := ParseSiteManifest(event)
		require.NoError(t, err)
		assert.True(t, sm.Root)
		assert.Equal(t, pubkey, sm.Pubkey)
		assert.Equal(t, "My Nostr Site", sm.Title)
		assert.Equal(t, "A static website hosted on Nostr", sm.Description)
		assert.Equal(t, "https://github.com/example/my-nostr-site", sm.Source)
		assert.Len(t, sm.Paths, 3)
		assert.Len(t, sm.Servers, 1)
		assert.Equal(t, "https://blossom.example.com", sm.Servers[0])
	})

	t.Run("named site", func(t *testing.T) {
		event := &nostr.Event{
			Kind:   nostr.KindNsiteNamed,
			PubKey: pubkey,
			Tags: nostr.Tags{
				{"d", "blog"},
				{"path", "/index.html", "186ea5fd14e88fd1ac49351759e7ab906fa94892002b60bf7f5a428f28ca1c99"},
				{"path", "/post.html", "a1b2c3d4e5f6789012345678901234567890abcdef1234567890abcdef123456"},
				{"server", "https://blossom.example.com"},
				{"title", "My Blog"},
				{"description", "A blog hosted on Nostr"},
				{"source", "https://github.com/example/my-nostr-blog"},
			},
		}

		sm, err := ParseSiteManifest(event)
		require.NoError(t, err)
		assert.False(t, sm.Root)
		assert.Equal(t, "blog", sm.Identifier)
		assert.Equal(t, pubkey, sm.Pubkey)
		assert.Equal(t, "My Blog", sm.Title)
	})

	t.Run("missing d tag on named site", func(t *testing.T) {
		event := &nostr.Event{
			Kind:   nostr.KindNsiteNamed,
			PubKey: pubkey,
			Tags: nostr.Tags{
				{"path", "/index.html", "186ea5fd14e88fd1ac49351759e7ab906fa94892002b60bf7f5a428f28ca1c99"},
			},
		}

		_, err := ParseSiteManifest(event)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing d tag")
	})

	t.Run("invalid kind", func(t *testing.T) {
		event := &nostr.Event{
			Kind:   1,
			PubKey: pubkey,
			Tags:   nostr.Tags{},
		}

		_, err := ParseSiteManifest(event)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid site manifest kind")
	})
}

func TestGetHashForPath(t *testing.T) {
	pubkey := nostr.MustPubKeyFromHex("266815e0c9210dfa324c6cba3573b14bee49da4209a9456f9484e5106cd408a5")
	event := &nostr.Event{
		Kind:   nostr.KindNsiteRoot,
		PubKey: pubkey,
		Tags: nostr.Tags{
			{"path", "/index.html", "186ea5fd14e88fd1ac49351759e7ab906fa94892002b60bf7f5a428f28ca1c99"},
			{"path", "/about.html", "a1b2c3d4e5f6789012345678901234567890abcdef1234567890abcdef123456"},
		},
	}

	sm, err := ParseSiteManifest(event)
	require.NoError(t, err)

	hash, ok := sm.GetHashForPath("/index.html")
	assert.True(t, ok)
	assert.Equal(t, "186ea5fd14e88fd1ac49351759e7ab906fa94892002b60bf7f5a428f28ca1c99", hex.EncodeToString(hash[:]))

	_, ok = sm.GetHashForPath("/nonexistent.html")
	assert.False(t, ok)
}

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/index.html", "/index.html"},
		{"/about.html", "/about.html"},
		{"/blog/", "/blog/index.html"},
		{"/", "/index.html"},
	}

	for _, test := range tests {
		result := NormalizePath(test.input)
		assert.Equal(t, test.expected, result)
	}
}

func TestPubKeyBase36(t *testing.T) {
	pubkey := nostr.MustPubKeyFromHex("266815e0c9210dfa324c6cba3573b14bee49da4209a9456f9484e5106cd408a5")

	b36 := PubKeyToBase36(pubkey)
	assert.Len(t, b36, 50)

	decoded, err := PubKeyFromBase36(b36)
	require.NoError(t, err)
	assert.Equal(t, pubkey, decoded)
}

func TestDecodeSiteURL(t *testing.T) {
	pubkey := nostr.MustPubKeyFromHex("266815e0c9210dfa324c6cba3573b14bee49da4209a9456f9484e5106cd408a5")

	t.Run("npub root site", func(t *testing.T) {
		decodedPubkey, identifier, isRoot, err := DecodeSiteURL("npub1ye5ptcxfyyxl5vjvdjar2ua3f0hynkjzpx552mu5snj3qmx5pzjscpknpr")
		require.NoError(t, err)
		assert.True(t, isRoot)
		assert.Equal(t, "", identifier)
		assert.Equal(t, decodedPubkey, pubkey)
	})

	t.Run("named site", func(t *testing.T) {
		b36 := PubKeyToBase36(pubkey)
		label := b36 + "blog"

		decodedPubkey, identifier, isRoot, err := DecodeSiteURL(label)
		require.NoError(t, err)
		assert.False(t, isRoot)
		assert.Equal(t, "blog", identifier)
		assert.Equal(t, decodedPubkey, pubkey)
	})

	t.Run("strips domain suffix", func(t *testing.T) {
		b36 := PubKeyToBase36(pubkey)
		label := b36 + "blog.nsite-host.com"

		_, identifier, _, err := DecodeSiteURL(label)
		require.NoError(t, err)
		assert.Equal(t, "blog", identifier)
	})

	t.Run("invalid dtag format", func(t *testing.T) {
		b36 := PubKeyToBase36(pubkey)
		label := b36 + "Blog"

		_, _, _, err := DecodeSiteURL(label)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid dtag format")
	})

	t.Run("label too short", func(t *testing.T) {
		_, _, _, err := DecodeSiteURL("npub1")
		assert.Error(t, err)
	})

	t.Run("ends with dash", func(t *testing.T) {
		b36 := PubKeyToBase36(pubkey)
		label := b36 + "blog-"

		_, _, _, err := DecodeSiteURL(label)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid site label format")
	})
}

func TestSiteManifestToEvent(t *testing.T) {
	pubkey := nostr.MustPubKeyFromHex("266815e0c9210dfa324c6cba3573b14bee49da4209a9456f9484e5106cd408a5")

	sm := &SiteManifest{
		Root:       true,
		Pubkey:     pubkey,
		Identifier: "",
		Paths: map[string][32]byte{
			"/index.html": mustHash("186ea5fd14e88fd1ac49351759e7ab906fa94892002b60bf7f5a428f28ca1c99"),
		},
		Servers:     []string{"https://blossom.example.com"},
		Title:       "Test Site",
		Description: "A test site",
		Source:      "https://github.com/example/test",
	}

	event := sm.ToEvent()
	assert.Equal(t, nostr.KindNsiteRoot, event.Kind)
	assert.Equal(t, pubkey, event.PubKey)

	sm.Root = false
	sm.Identifier = "blog"
	event = sm.ToEvent()
	assert.Equal(t, nostr.KindNsiteNamed, event.Kind)
	found := false
	for _, tag := range event.Tags {
		if tag[0] == "d" && tag[1] == "blog" {
			found = true
			break
		}
	}
	assert.True(t, found)
}

func mustHash(s string) [32]byte {
	var h [32]byte
	b, _ := hex.DecodeString(s)
	copy(h[:], b)
	return h
}

