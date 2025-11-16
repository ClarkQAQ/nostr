package policies

import (
	"context"
	"net/http"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/relay"
)

func EventIPRateLimiter(tokensPerInterval int, interval time.Duration, maxTokens int) func(ctx context.Context, _ nostr.Event) (reject bool, msg string) {
	rl := startRateLimitSystem[string](tokensPerInterval, interval, maxTokens)

	return func(ctx context.Context, _ nostr.Event) (reject bool, msg string) {
		ip := relay.GetIP(ctx)
		if ip == "127.0.0.1" || ip == "::1" {
			return false, ""
		}
		return rl(ip), "rate-limited: slow down, please"
	}
}

func EventPubKeyRateLimiter(tokensPerInterval int, interval time.Duration, maxTokens int) func(ctx context.Context, _ nostr.Event) (reject bool, msg string) {
	rl := startRateLimitSystem[string](tokensPerInterval, interval, maxTokens)

	return func(ctx context.Context, evt nostr.Event) (reject bool, msg string) {
		ip := relay.GetIP(ctx)
		if ip == "127.0.0.1" || ip == "::1" {
			return false, ""
		}
		return rl(evt.PubKey.Hex()), "rate-limited: slow down, please"
	}
}

func ConnectionRateLimiter(tokensPerInterval int, interval time.Duration, maxTokens int) func(r *http.Request) bool {
	rl := startRateLimitSystem[string](tokensPerInterval, interval, maxTokens)

	return func(r *http.Request) bool {
		ip := relay.GetIPFromRequest(r)
		if ip == "127.0.0.1" || ip == "::1" {
			return false
		}
		return rl(ip)
	}
}

func FilterIPRateLimiter(tokensPerInterval int, interval time.Duration, maxTokens int) func(ctx context.Context, _ nostr.Filter) (reject bool, msg string) {
	rl := startRateLimitSystem[string](tokensPerInterval, interval, maxTokens)

	return func(ctx context.Context, _ nostr.Filter) (reject bool, msg string) {
		ip := relay.GetIP(ctx)
		if ip == "127.0.0.1" {
			return false, ""
		}
		return rl(ip), "rate-limited: there is a bug in the client, no one should be making so many requests"
	}
}
