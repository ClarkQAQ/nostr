package nip46

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip44"
)

var NoConnectionReceived = errors.New("relay connections ended without a bunker connection established")

// GenerateNostrConnectURL generates a nostrconnect:// URL that can be passed to the remote-signer
// The remote-signer will then send a connect response to the client-pubkey via the specified relays
func GenerateNostrConnectURL(
	ctx context.Context,
	clientSecretKey [32]byte,
	relayURLs []string,
	permissions []string,
	clientName, clientURL, clientImage string,
) (string, error) {
	if len(relayURLs) == 0 {
		return "", fmt.Errorf("at least one relay URL is required")
	}

	// generate random secret
	secretBytes := make([]byte, 16)
	if _, err := rand.Read(secretBytes); err != nil {
		return "", fmt.Errorf("failed to generate secret: %w", err)
	}
	secret := fmt.Sprintf("%x", secretBytes)

	clientPublicKey := nostr.GetPublicKey(clientSecretKey)
	params := url.Values{}
	for _, relay := range relayURLs {
		params.Add("relay", relay)
	}
	params.Set("secret", secret)

	if len(permissions) > 0 {
		params.Set("perms", strings.Join(permissions, ","))
	}
	if clientName != "" {
		params.Set("name", clientName)
	}
	if clientURL != "" {
		params.Set("url", clientURL)
	}
	if clientImage != "" {
		params.Set("image", clientImage)
	}

	connectURL := fmt.Sprintf("nostrconnect://%s?%s", clientPublicKey.Hex(), params.Encode())
	return connectURL, nil
}

// NewBunkerFromNostrConnect waits for a client to connect through our nostrconnect:// URL and returns a BunkerClient on success
func NewBunkerFromNostrConnect(
	ctx context.Context,
	clientSecretKey [32]byte,
	relayURLs []string,
	secret string,
	pool *nostr.Pool,
) (*BunkerClient, error) {
	if pool == nil {
		pool = nostr.NewPool()
	}

	if len(relayURLs) == 0 {
		return nil, fmt.Errorf("at least one relay URL is required")
	}

	clientPublicKey := nostr.GetPublicKey(clientSecretKey)

	// subscribe to events addressed to our client public key
	for ie := range pool.SubscribeMany(ctx, relayURLs, nostr.Filter{
		Tags:      nostr.TagMap{"p": []string{clientPublicKey.Hex()}},
		Kinds:     []nostr.Kind{nostr.KindNostrConnect},
		Since:     nostr.Now(),
		LimitZero: true,
	}, nostr.SubscriptionOptions{Label: "nostrconnect-client"}) {
		if ie.Kind != nostr.KindNostrConnect {
			continue
		}
		if ie.PubKey.String() == clientPublicKey.Hex() {
			continue
		}

		// decrypt and process the connect response
		targetPublicKey := ie.PubKey
		conversationKey, err := nip44.GenerateConversationKey(targetPublicKey, clientSecretKey)
		if err != nil {
			continue
		}
		plain, err := nip44.Decrypt(ie.Content, conversationKey)
		if err != nil {
			continue
		}

		var req Response // contradictorily the thing starts with a response
		if err := json.Unmarshal([]byte(plain), &req); err != nil {
			continue
		}

		if req.Result != "" {
			if req.Result == secret {
				// secret validation passed - connection established
				cancellableCtx, cancel := context.WithCancel(ctx)
				_ = cancel
				bunker := NewBunker(cancellableCtx, clientSecretKey, targetPublicKey, relayURLs, pool, func(string) {})

				// attempt switch_relays
				if newRelays, _ := bunker.SwitchRelays(ctx); newRelays != nil {
					cancel()
					bunker = NewBunker(ctx, clientSecretKey, targetPublicKey, newRelays, pool, func(string) {})
				}

				return bunker, nil
			}
		}
	}

	return nil, NoConnectionReceived
}
