package nip46

import (
	"context"
	"fmt"
	"sync"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip44"
	"github.com/mailru/easyjson"
)

var _ Signer = (*DynamicSigner)(nil)

type DynamicSigner struct {
	// { [handlePubkey]: {[clientKey]: Session} }
	sessions map[nostr.PubKey]map[nostr.PubKey]Session

	sync.Mutex

	// the handler is the keypair we use to communicate with the NIP-46 client, decrypt requests, encrypt responses etc
	// the context can be returned as is, but it can also be returned with some values in it so they can be passed
	//   to other functions later in the chain.
	GetHandlerSecretKey func(
		ctx context.Context,
		handlerPubkey nostr.PubKey,
	) (context.Context, nostr.SecretKey, error)

	// called when a client calls "connect", use it to associate the client pubkey with a secret or something like that
	OnConnect func(ctx context.Context, from nostr.PubKey, secret string) error

	// this should correspond to the actual user on behalf of which we will respond to requests
	// the context works the same as for GetHandlerSecretKey
	GetUserKeyer func(ctx context.Context, handlerPubkey nostr.PubKey) (context.Context, nostr.Keyer, error)

	// this is called on every sign_event call, if it is nil it will be assumed that everything is authorized
	AuthorizeSigning func(ctx context.Context, event nostr.Event, from nostr.PubKey) error

	// this is called on every encrypt or decrypt calls, if it is nil it will be assumed that everything is authorized
	AuthorizeEncryption func(ctx context.Context, from nostr.PubKey) bool

	// unless it is nil, this is called after every event is signed
	OnEventSigned func(event nostr.Event)
}

func (p *DynamicSigner) Init() {
	p.sessions = make(map[nostr.PubKey]map[nostr.PubKey]Session)
}

func (p *DynamicSigner) HandleRequest(ctx context.Context, event nostr.Event) (
	req Request,
	resp Response,
	eventResponse nostr.Event,
	err error,
) {
	if event.Kind != nostr.KindNostrConnect {
		return req, resp, eventResponse,
			fmt.Errorf("event kind is %d, but we expected %d", event.Kind, nostr.KindNostrConnect)
	}

	handler := event.Tags.Find("p")
	if handler == nil || !nostr.IsValid32ByteHex(handler[1]) {
		return req, resp, eventResponse, fmt.Errorf("invalid \"p\" tag")
	}

	handlerPubkey, err := nostr.PubKeyFromHex(handler[1])
	if err != nil {
		return req, resp, eventResponse, fmt.Errorf("%x is invalid pubkey: %w", handler[1], err)
	}

	p.Lock()
	defer p.Unlock()

	ctx, handlerSecret, err := p.GetHandlerSecretKey(ctx, handlerPubkey)
	if err != nil {
		return req, resp, eventResponse, fmt.Errorf("no private key for %s: %w", handlerPubkey, err)
	}
	ctx, userKeyer, err := p.GetUserKeyer(ctx, handlerPubkey)
	if err != nil {
		return req, resp, eventResponse, fmt.Errorf("failed to get user keyer for %s: %w", handlerPubkey, err)
	}

	handlerSessions, exists := p.sessions[handlerPubkey]
	if !exists {
		handlerSessions = make(map[nostr.PubKey]Session)
		p.sessions[handlerPubkey] = handlerSessions
	}

	session, exists := handlerSessions[event.PubKey]
	if !exists {
		// create session if it doesn't exist
		session = Session{}

		session.ConversationKey, err = nip44.GenerateConversationKey(event.PubKey, handlerSecret)
		if err != nil {
			return req, resp, eventResponse, fmt.Errorf("failed to compute shared secret: %w", err)
		}

		session.PublicKey, err = userKeyer.GetPublicKey(ctx)
		if err != nil {
			return req, resp, eventResponse, fmt.Errorf("failed to get public key: %w", err)
		}
	}

	// save session
	handlerSessions[event.PubKey] = session

	// use this session to handle the request
	req, err = session.ParseRequest(event)
	if err != nil {
		return req, resp, eventResponse, fmt.Errorf("error parsing request: %w", err)
	}

	var result string
	var resultErr error

	switch req.Method {
	case "connect":
		var secret string
		if len(req.Params) >= 2 {
			secret = req.Params[1]
		}

		if p.OnConnect != nil {
			if err := p.OnConnect(ctx, event.PubKey, secret); err != nil {
				resultErr = err
				break
			}
		}

		result = "ack"
	case "get_public_key":
		result = nostr.HexEncodeToString(session.PublicKey[:])
	case "sign_event":
		if len(req.Params) != 1 {
			resultErr = fmt.Errorf("wrong number of arguments to 'sign_event'")
			break
		}
		evt := nostr.Event{}
		err = easyjson.Unmarshal([]byte(req.Params[0]), &evt)
		if err != nil {
			resultErr = fmt.Errorf("failed to decode event/2: %w", err)
			break
		}
		if p.AuthorizeSigning != nil {
			if err := p.AuthorizeSigning(ctx, evt, event.PubKey); err != nil {
				resultErr = fmt.Errorf("refusing to sign: %s", err)
				break
			}
		}

		err = userKeyer.SignEvent(ctx, &evt)
		if err != nil {
			resultErr = fmt.Errorf("failed to sign event: %w", err)
			break
		}

		jrevt, _ := easyjson.Marshal(evt)
		result = string(jrevt)
	case "nip44_encrypt":
		if len(req.Params) != 2 {
			resultErr = fmt.Errorf("wrong number of arguments to 'nip44_encrypt'")
			break
		}
		thirdPartyPubkey, err := nostr.PubKeyFromHex(req.Params[0])
		if err != nil {
			resultErr = fmt.Errorf("first argument to 'nip44_encrypt' is not a valid pubkey hex")
			break
		}
		if p.AuthorizeEncryption != nil && !p.AuthorizeEncryption(ctx, event.PubKey) {
			resultErr = fmt.Errorf("refusing to encrypt")
			break
		}
		plaintext := req.Params[1]

		ciphertext, err := userKeyer.Encrypt(ctx, plaintext, thirdPartyPubkey)
		if err != nil {
			resultErr = fmt.Errorf("failed to encrypt: %w", err)
			break
		}
		result = ciphertext
	case "nip44_decrypt":
		if len(req.Params) != 2 {
			resultErr = fmt.Errorf("wrong number of arguments to 'nip04_decrypt'")
			break
		}
		thirdPartyPubkey, err := nostr.PubKeyFromHex(req.Params[0])
		if err != nil {
			resultErr = fmt.Errorf("first argument to 'nip04_decrypt' is not a valid pubkey hex")
			break
		}
		if p.AuthorizeEncryption != nil && !p.AuthorizeEncryption(ctx, event.PubKey) {
			resultErr = fmt.Errorf("refusing to decrypt")
			break
		}
		ciphertext := req.Params[1]

		plaintext, err := userKeyer.Decrypt(ctx, ciphertext, thirdPartyPubkey)
		if err != nil {
			resultErr = fmt.Errorf("failed to decrypt: %w", err)
			break
		}
		result = plaintext
	case "ping":
		result = "pong"
	default:
		return req, resp, eventResponse,
			fmt.Errorf("unknown method '%s'", req.Method)
	}

	resp, eventResponse, err = session.MakeResponse(req.ID, event.PubKey, result, resultErr)
	if err != nil {
		return req, resp, eventResponse, err
	}

	err = eventResponse.Sign(handlerSecret)
	if err != nil {
		return req, resp, eventResponse, err
	}

	return req, resp, eventResponse, err
}
