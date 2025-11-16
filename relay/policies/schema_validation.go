package policies

import (
	"context"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/schema"
)

func ValidateAgainstSchema(ctx context.Context, evt nostr.Event) (bool, string) {
	v := schema.NewDefaultValidator()
	v.FailOnUnknown = true
	err := v.ValidateEvent(evt)
	if err != nil {
		return true, err.Error()
	}
	return false, ""
}
