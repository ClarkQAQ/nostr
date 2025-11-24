package policies

import (
	"context"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/schema"
)

func ValidateAgainstSchema(v schema.Validator) func(ctx context.Context, evt nostr.Event) (bool, string) {
	return func(ctx context.Context, evt nostr.Event) (bool, string) {
		err := v.ValidateEvent(evt)
		if err != nil {
			return true, err.Error()
		}
		return false, ""
	}
}
