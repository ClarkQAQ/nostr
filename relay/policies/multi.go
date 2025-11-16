package policies

import (
	"context"

	"fiatjaf.com/nostr"
)

func SeqEvent(
	funcs ...func(ctx context.Context, evt nostr.Event) (bool, string),
) func(context.Context, nostr.Event) (reject bool, reason string) {
	return func(ctx context.Context, evt nostr.Event) (reject bool, reason string) {
		for _, fn := range funcs {
			reject, reason := fn(ctx, evt)
			if reject {
				return reject, reason
			}
		}
		return false, ""
	}
}

func SeqStore(funcs ...func(ctx context.Context, evt nostr.Event) error) func(context.Context, nostr.Event) error {
	return func(ctx context.Context, evt nostr.Event) error {
		for _, fn := range funcs {
			err := fn(ctx, evt)
			if err != nil {
				return err
			}
		}
		return nil
	}
}

func SeqRequest(
	funcs ...func(ctx context.Context, filter nostr.Filter) (bool, string),
) func(context.Context, nostr.Filter) (reject bool, reason string) {
	return func(ctx context.Context, evt nostr.Filter) (reject bool, reason string) {
		for _, fn := range funcs {
			reject, reason := fn(ctx, evt)
			if reject {
				return reject, reason
			}
		}
		return false, ""
	}
}
