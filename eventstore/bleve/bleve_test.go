package bleve

import (
	"os"
	"testing"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/boltdb"
	"github.com/stretchr/testify/assert"
)

func TestBleveFlow(t *testing.T) {
	os.RemoveAll("/tmp/blevetest-boltdb")
	os.RemoveAll("/tmp/blevetest-bleve")

	bb := &boltdb.BoltBackend{Path: "/tmp/blevetest-boltdb"}
	if e := bb.Init(); e != nil {
		t.Fatal(e)
	}
	defer bb.Close()

	bl := BleveBackend{
		Path:          "/tmp/blevetest-bleve",
		RawEventStore: bb,
	}
	if e := bl.Init(); e != nil {
		t.Fatal(e)
	}
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
		if e := evt.Sign(nostr.MustSecretKeyFromHex("0000000000000000000000000000000000000000000000000000000000000001")); e != nil {
			t.Fatal(e)
		}

		if e := bb.SaveEvent(evt); e != nil {
			t.Fatal(e)
		}
		if e := bl.SaveEvent(evt); e != nil {
			t.Fatal(e)
		}

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
		if e := bl.DeleteEvent(evt.ID); e != nil {
			t.Fatal(e)
		}
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
