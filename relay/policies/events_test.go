package policies

import (
	"context"
	"testing"

	"fiatjaf.com/nostr"
)

func TestRejectUnprefixedNostrReferences(t *testing.T) {
	tests := []struct {
		name         string
		content      string
		shouldReject bool
	}{
		{
			name:         "unprefixed nevent1 valid",
			content:      "nevent1qqsz3q79apv874xv8ta5za03nkmugnwc3nq046dd2wy30fh8hurn67qpp4mhxue69uhkummn9ekx7mqdzh53y",
			shouldReject: true,
		},
		{
			name:         "unprefixed npub1 valid",
			content:      "npub1eer3xzy76k8tqr2w40804d07qxyzq4ypfv0vv70kj3xnuukcdhts35cfkg",
			shouldReject: true,
		},
		{
			name:         "unprefixed nprofile1 valid",
			content:      "nprofile1qqsxu3ytjdwz9xwtlzuhrgf7yx3e0pcw8hvgtqnramc4t5gdh5vm6mgud3p0w",
			shouldReject: true,
		},
		{
			name:         "unprefixed note1 valid",
			content:      "note1sugf04s8yvveh7a4nhguhu2h3yumqd3kcr3yu6f4phk5u3m635wqz3tngh",
			shouldReject: true,
		},
		{
			name:         "prefixed nostr:nevent1",
			content:      "Check this event: nostr:nevent1qqsz3q79apv874xv8ta5za03nkmugnwc3nq046dd2wy30fh8hurn67qpp4mhxue69uhkummn9ekx7mqdzh53y",
			shouldReject: false,
		},
		{
			name:         "prefixed nostr:npub1",
			content:      "User: nostr:npub1eer3xzy76k8tqr2w40804d07qxyzq4ypfv0vv70kj3xnuukcdhts35cfkg",
			shouldReject: false,
		},
		{
			name:         "no references",
			content:      "This is just regular text",
			shouldReject: false,
		},
		{
			name:         "multiple unprefixed valid",
			content:      "See nevent1qqsz3q79apv874xv8ta5za03nkmugnwc3nq046dd2wy30fh8hurn67qpp4mhxue69uhkummn9ekx7mqdzh53y and npub1eer3xzy76k8tqr2w40804d07qxyzq4ypfv0vv70kj3xnuukcdhts35cfkg",
			shouldReject: true,
		},
		{
			name:         "mixed prefixed and unprefixed valid",
			content:      "Good: nostr:nevent1qqsz3q79apv874xv8ta5za03nkmugnwc3nq046dd2wy30fh8hurn67qpp4mhxue69uhkummn9ekx7mqdzh53y Bad: npub1eer3xzy76k8tqr2w40804d07qxyzq4ypfv0vv70kj3xnuukcdhts35cfkg",
			shouldReject: true,
		},
		{
			name:         "invalid unprefixed nevent1",
			content:      "nevent1abc123",
			shouldReject: false, // invalid, so allowed
		},
		{
			name:         "invalid unprefixed npub1",
			content:      "npub1def456",
			shouldReject: false, // invalid, so allowed
		},
		{
			name:         "invalid unprefixed note1",
			content:      "note1jkl012",
			shouldReject: false, // invalid, so allowed
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := nostr.Event{
				Content: tt.content,
			}
			reject, _ := RejectUnprefixedNostrReferences(context.Background(), event)
			if reject != tt.shouldReject {
				t.Errorf("RejectUnprefixedNostrReferences() = %v, shouldReject %v", reject, tt.shouldReject)
			}
		})
	}
}
