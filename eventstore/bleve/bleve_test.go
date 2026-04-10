package bleve

import (
	"os"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/lmdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBleveFlow(t *testing.T) {
	os.RemoveAll("/tmp/blevetest-lmdb")
	os.RemoveAll("/tmp/blevetest-bleve")

	bb := &lmdb.LMDBBackend{Path: "/tmp/blevetest-lmdb"}
	bb.Init()
	defer bb.Close()

	bl := BleveBackend{
		Path:          "/tmp/blevetest-bleve",
		RawEventStore: bb,
	}
	err := bl.Init()
	require.NoError(t, err, "init")

	defer bl.Close()

	willDelete := make([]nostr.Event, 0, 3)

	for i, content := range []string{
		"good morning mr paper maker",
		"good night",
		"I'll see you again in the paper house",
		"tonight we dine in my house",
		"the paper in this house if very good, mr",
	} {
		evt := nostr.Event{Content: content, Tags: nostr.Tags{}}
		evt.Sign(nostr.MustSecretKeyFromHex("0000000000000000000000000000000000000000000000000000000000000001"))

		bb.SaveEvent(evt)
		bl.SaveEvent(evt)

		if i%2 == 0 {
			willDelete = append(willDelete, evt)
		}
	}

	{
		n := 0
		t.Logf("searching for 'good' (should find 3)")
		for range bl.QueryEvents(nostr.Filter{Search: "good"}, 400) {
			n++
		}
		t.Logf("found %d results", n)
		assert.Equal(t, 3, n)
	}

	for _, evt := range willDelete {
		bl.DeleteEvent(evt.ID)
	}

	{
		n := 0
		for evt := range bl.QueryEvents(nostr.Filter{Search: "good"}, 400) {
			n++
			assert.Equal(t, evt.Content, "good night")
			assert.Equal(t,
				nostr.MustPubKeyFromHex("79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"),
				evt.PubKey,
			)
		}
		assert.Equal(t, 1, n)
	}
}
