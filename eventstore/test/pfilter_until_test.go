package test

import (
	"context"
	"encoding/hex"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"github.com/stretchr/testify/require"
)

func pTagUntilMismatchTest(t *testing.T, db eventstore.Store) {
	err := db.Init(context.Background())
	require.NoError(t, err)

	targetP := "460c25e682fda7832b52d1f22d3d22b3176d972f60dcdc3212ed8c92ef85065c"
	author := nostr.MustPubKeyFromHex("7fa56f5d6962ab1e3cd424e758c3002b8665f7b0d8dcee9fe9e288d7751ac194")

	events := []nostr.Event{
		{
			Kind:      9802,
			ID:        nostr.MustIDFromHex("2c997233fa580b1a831f989d8fa320c409f8412d9c75b819c9a29df102d7f901"),
			PubKey:    author,
			CreatedAt: 1773835689,
			Tags: nostr.Tags{
				{"e", "bcaa6599e69cff48ed6ab4b0b315d4f33a869a4ba8fa808287700faebe17195f", "wss://nos.lol/", "source"},
				{"p", targetP, "", "author"},
			},
			Content: "With so few people donating in the zap the devs button, the incentives are quite low to produce cool new things",
			Sig:     sigFromHex(t, "b49206476c4d2a5f44590331541c83910fd826c0f4cdab99ceffd5bcf3aca94935e3db9d7820e7db3a0f1165c43a28dd3173a81fd08bf8348629ea4efde02537"),
		},
		{
			Kind:      9802,
			ID:        nostr.MustIDFromHex("31c1eddb3a5201ef1bbce91b9fb3b7d8fe3e3eb25a66bedadcbc93c84d072c7d"),
			PubKey:    author,
			CreatedAt: 1773154080,
			Tags: nostr.Tags{
				{"p", "5ea4648045bb1ff222655ddd36e6dceddc43590c26090c486bef38ef450da5bd", "", "mention"},
				{"p", "c8fb0d3aa788b9ace4f6cb92dd97d3f292db25b5c9f92462ef6c64926129fbaf", "", "mention"},
				{"p", "2f29aa33c2a3b45c2ef32212879248b2f4a49a002bd0de0fa16c94e138ac6f13", "", "mention"},
				{"p", targetP, "", "mention"},
				{"comment", "normie"},
				{"e", "5911eeba39a6886fe8abea82bb50612d27d1273d63904c9b64cde070c7088d48", "wss://relay.primal.net/", "source"},
				{"p", "3f770d65d3a764a9c5cb503ae123e62ec7598ad035d836e2a810f3877a745b24", "", "author"},
			},
			Content: "grimoire is cool, but it's too nerdy for me",
			Sig:     sigFromHex(t, "0ee01515d54293d52fa1247a395e64c8499df96eee80c30204cb7c8fc5b5977023e6ac00cc240f137ce7e594818545340bf74bce6d3de86539f1a5d26fe33f24"),
		},
		{
			Kind:      9802,
			ID:        nostr.MustIDFromHex("baeb90e2075c9d8a9b41286dbf1c52e5ef8ad6c030118839ce24d065e72df9b7"),
			PubKey:    author,
			CreatedAt: 1773154058,
			Tags: nostr.Tags{
				{"p", "c8fb0d3aa788b9ace4f6cb92dd97d3f292db25b5c9f92462ef6c64926129fbaf", "", "mention"},
				{"p", "2f29aa33c2a3b45c2ef32212879248b2f4a49a002bd0de0fa16c94e138ac6f13", "", "mention"},
				{"p", targetP, "", "mention"},
				{"p", "3f770d65d3a764a9c5cb503ae123e62ec7598ad035d836e2a810f3877a745b24", "", "mention"},
				{"comment", "no lies detected"},
				{"e", "81171c564cedbc5f07e5b7a9d06842d1f43a81cd79c8755921190382de55c514", "wss://nos.lol/", "source"},
				{"p", "5ea4648045bb1ff222655ddd36e6dceddc43590c26090c486bef38ef450da5bd", "", "author"},
			},
			Content: "i never used grimoire once in my life, but that is not the point, it is still the best client",
			Sig:     sigFromHex(t, "3e4b855c4e3a4d2b3a078d593e728f1bbdb07af91ba5831b7866e2d16df90ce58a5f9f1db5733603911b20b334bc7fc5ae1482c7870b9f7acde4a8ccc080a79d"),
		},
	}

	for _, evt := range events {
		err = db.SaveEvent(context.Background(), evt)
		require.NoError(t, err)
	}

	results := eventstore.CollectEvents(db.QueryEvents(context.Background(), nostr.Filter{
		Until: 1733934976,
		Limit: 3,
		Tags:  nostr.TagMap{"p": []string{targetP}},
	}, 1000))
	require.Len(t, results, 0)
}

func sigFromHex(t *testing.T, sigStr string) [64]byte {
	t.Helper()

	raw, err := hex.DecodeString(sigStr)
	require.NoError(t, err)
	require.Len(t, raw, 64)

	var sig [64]byte
	copy(sig[:], raw)
	return sig
}
