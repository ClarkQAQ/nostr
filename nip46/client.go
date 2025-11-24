package nip46

import (
	"context"
	"fmt"
	"math/rand"
	"net/url"
	"strconv"
	"sync/atomic"
	"unsafe"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip44"
	"github.com/mailru/easyjson"
	"github.com/puzpuzpuz/xsync/v3"
)

type BunkerClient struct {
	serial          atomic.Uint64
	clientSecretKey [32]byte
	pool            *nostr.Pool
	target          nostr.PubKey
	relays          []string
	conversationKey [32]byte // nip44
	listeners       *xsync.MapOf[string, chan Response]
	idPrefix        string
	onAuth          func(string)

	// memoized
	getPublicKeyResponse nostr.PubKey

	// SkipSignatureCheck can be set if you don't want to double-check incoming signatures
	SkipSignatureCheck bool
}

// ConnectBunker establishes an RPC connection to a NIP-46 signer using the relays and secret provided in the bunkerURL.
// pool can be passed to reuse an existing pool, otherwise a new pool will be created.
func ConnectBunker(
	ctx context.Context,
	clientSecretKey nostr.SecretKey,
	bunkerURLOrNIP05 string,
	pool *nostr.Pool,
	onAuth func(string),
) (*BunkerClient, error) {
	parsed, err := ParseBunkerInput(ctx, bunkerURLOrNIP05)
	if err != nil {
		return nil, fmt.Errorf("invalid bunker: %w", err)
	}

	bunker := NewBunker(
		ctx,
		clientSecretKey,
		parsed.HostPubKey,
		parsed.Relays,
		pool,
		onAuth,
	)
	_, err = bunker.RPC(ctx, "connect", []string{nostr.HexEncodeToString(parsed.HostPubKey[:]), parsed.Secret})
	return bunker, err
}

type ParsedBunker struct {
	HostPubKey nostr.PubKey
	Relays     []string
	Secret     string
}

func ParseBunkerInput(ctx context.Context, bunkerURLOrNIP05 string) (ParsedBunker, error) {
	parsed, err := url.Parse(bunkerURLOrNIP05)
	if err != nil {
		return ParsedBunker{}, fmt.Errorf("invalid url: %w", err)
	}

	var host nostr.PubKey
	var secret string
	var relays []string

	if parsed.Scheme == "" {
		// could be a NIP-05
		pubkey, relays_, err := queryWellKnownNostrJson(ctx, bunkerURLOrNIP05)
		if err != nil {
			return ParsedBunker{}, fmt.Errorf("failed to query nip05: %w", err)
		}
		host = pubkey
		relays = relays_
	} else if parsed.Scheme == "bunker" {
		secret = parsed.Query().Get("secret")
		relays = parsed.Query()["relay"]
		host, err = nostr.PubKeyFromHex(parsed.Host)
		if err != nil {
			return ParsedBunker{}, fmt.Errorf("invalid host pubkey: %w", err)
		}
	} else {
		// otherwise fail here
		return ParsedBunker{}, fmt.Errorf("wrong scheme '%s', must be bunker://", parsed.Scheme)
	}

	return ParsedBunker{
		HostPubKey: host,
		Relays:     relays,
		Secret:     secret,
	}, nil
}

func NewBunker(
	ctx context.Context,
	clientSecretKey [32]byte,
	targetPublicKey nostr.PubKey,
	relays []string,
	pool *nostr.Pool,
	onAuth func(string),
) *BunkerClient {
	if pool == nil {
		pool = nostr.NewPool(nostr.PoolOptions{})
	}

	clientPublicKey := nostr.GetPublicKey(clientSecretKey)
	conversationKey, _ := nip44.GenerateConversationKey(targetPublicKey, clientSecretKey)

	bunker := &BunkerClient{
		pool:            pool,
		clientSecretKey: clientSecretKey,
		target:          targetPublicKey,
		relays:          relays,
		conversationKey: conversationKey,
		listeners:       xsync.NewMapOf[string, chan Response](),
		onAuth:          onAuth,
		idPrefix:        "nl-" + strconv.Itoa(rand.Intn(65536)),
	}

	go func() {
		events := pool.SubscribeMany(ctx, relays, nostr.Filter{
			Tags:      nostr.TagMap{"p": []string{clientPublicKey.Hex()}},
			Kinds:     []nostr.Kind{nostr.KindNostrConnect},
			Since:     nostr.Now(),
			LimitZero: true,
		}, nostr.SubscriptionOptions{
			Label: "bunker46client",
		})
		for ie := range events {
			if ie.Kind != nostr.KindNostrConnect {
				continue
			}

			var resp Response
			plain, err := nip44.Decrypt(ie.Content, conversationKey)
			if err != nil {
				continue
			}

			err = json.Unmarshal([]byte(plain), &resp)
			if err != nil {
				continue
			}

			if resp.Result == "auth_url" {
				// special case
				authURL := resp.Error
				bunker.onAuth(authURL)
				continue
			}

			if dispatcher, ok := bunker.listeners.LoadAndDelete(resp.ID); ok {
				dispatcher <- resp
				continue
			}
		}
	}()

	return bunker
}

func (bunker *BunkerClient) Ping(ctx context.Context) error {
	_, err := bunker.RPC(ctx, "ping", []string{})
	if err != nil {
		return err
	}
	return nil
}

func (bunker *BunkerClient) GetPublicKey(ctx context.Context) (nostr.PubKey, error) {
	if bunker.getPublicKeyResponse != nostr.ZeroPK {
		return bunker.getPublicKeyResponse, nil
	}
	resp, err := bunker.RPC(ctx, "get_public_key", []string{})
	if err != nil {
		return nostr.ZeroPK, err
	}

	pk, err := nostr.PubKeyFromHex(resp)
	if err != nil {
		return nostr.ZeroPK, err
	}

	bunker.getPublicKeyResponse = pk
	return pk, nil
}

func (bunker *BunkerClient) SignEvent(ctx context.Context, evt *nostr.Event) error {
	resp, err := bunker.RPC(ctx, "sign_event", []string{evt.String()})
	if err != nil {
		return err
	}

	err = easyjson.Unmarshal(unsafe.Slice(unsafe.StringData(resp), len(resp)), evt)
	if err != nil {
		return err
	}

	if !bunker.SkipSignatureCheck {
		if ok := evt.CheckID(); !ok {
			return fmt.Errorf("sign_event response from bunker has invalid id")
		}
		if !evt.VerifySignature() {
			return fmt.Errorf("sign_event response from bunker has invalid signature")
		}
	}

	return nil
}

func (bunker *BunkerClient) NIP44Encrypt(
	ctx context.Context,
	targetPublicKey nostr.PubKey,
	plaintext string,
) (string, error) {
	return bunker.RPC(ctx, "nip44_encrypt", []string{targetPublicKey.Hex(), plaintext})
}

func (bunker *BunkerClient) NIP44Decrypt(
	ctx context.Context,
	targetPublicKey nostr.PubKey,
	ciphertext string,
) (string, error) {
	return bunker.RPC(ctx, "nip44_decrypt", []string{targetPublicKey.Hex(), ciphertext})
}

func (bunker *BunkerClient) NIP04Encrypt(
	ctx context.Context,
	targetPublicKey nostr.PubKey,
	plaintext string,
) (string, error) {
	return bunker.RPC(ctx, "nip04_encrypt", []string{targetPublicKey.Hex(), plaintext})
}

func (bunker *BunkerClient) NIP04Decrypt(
	ctx context.Context,
	targetPublicKey nostr.PubKey,
	ciphertext string,
) (string, error) {
	return bunker.RPC(ctx, "nip04_decrypt", []string{targetPublicKey.Hex(), ciphertext})
}

func (bunker *BunkerClient) RPC(ctx context.Context, method string, params []string) (string, error) {
	id := bunker.idPrefix + "-" + strconv.FormatUint(bunker.serial.Add(1), 10)
	req, err := json.Marshal(Request{
		ID:     id,
		Method: method,
		Params: params,
	})
	if err != nil {
		return "", err
	}

	content, err := nip44.Encrypt(string(req), bunker.conversationKey)
	if err != nil {
		return "", fmt.Errorf("error encrypting request: %w", err)
	}

	evt := nostr.Event{
		Content:   content,
		CreatedAt: nostr.Now(),
		Kind:      nostr.KindNostrConnect,
		Tags:      nostr.Tags{{"p", bunker.target.Hex()}},
	}
	if err := evt.Sign(bunker.clientSecretKey); err != nil {
		return "", fmt.Errorf("failed to sign request event: %w", err)
	}

	dispatcher := make(chan Response)
	bunker.listeners.Store(id, dispatcher)
	defer bunker.listeners.Delete(id)
	relayConnectionWorked := make(chan struct{})
	bunkerConnectionWorked := make(chan struct{})

	for _, url := range bunker.relays {
		go func(url string) {
			relay, err := bunker.pool.EnsureRelay(url)
			if err == nil {
				select {
				case relayConnectionWorked <- struct{}{}:
				default:
				}
				if err := relay.Publish(ctx, evt); err == nil {
					select {
					case bunkerConnectionWorked <- struct{}{}:
					default:
					}
				}
			}
		}(url)
	}

	select {
	case <-relayConnectionWorked:
		select {
		case <-bunkerConnectionWorked:
			// continue
		case <-ctx.Done():
			return "", fmt.Errorf("couldn't reach the bunker, it is probably offline")
		}
	case <-ctx.Done():
		return "", fmt.Errorf("couldn't connect to any relay")
	}

	select {
	case <-ctx.Done():
		return "", fmt.Errorf("context canceled")
	case resp := <-dispatcher:
		if resp.Error != "" {
			return "", fmt.Errorf("response error: %s", resp.Error)
		}
		return resp.Result, nil
	}
}
