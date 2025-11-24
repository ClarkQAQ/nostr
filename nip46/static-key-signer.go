package nip46

import (
	"context"
	"fmt"
	"sync"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip04"
	"fiatjaf.com/nostr/nip44"
	"github.com/mailru/easyjson"
)

var _ Signer = (*StaticKeySigner)(nil)

type StaticKeySigner struct {
	secretKey [32]byte
	sessions  map[nostr.PubKey]Session

	sync.Mutex

	AuthorizeRequest func(harmless bool, from nostr.PubKey, secret string) bool
}

func NewStaticKeySigner(secretKey [32]byte) StaticKeySigner {
	return StaticKeySigner{
		secretKey: secretKey,
		sessions:  make(map[nostr.PubKey]Session),
	}
}

func (p *StaticKeySigner) getOrCreateSession(clientPubkey nostr.PubKey) (Session, error) {
	p.Lock()
	defer p.Unlock()

	session, exists := p.sessions[clientPubkey]
	if exists {
		return session, nil
	}

	ck, err := nip44.GenerateConversationKey(clientPubkey, p.secretKey)
	if err != nil {
		return Session{}, fmt.Errorf("failed to compute shared secret: %w", err)
	}

	pubkey := nostr.GetPublicKey(p.secretKey)
	session = Session{
		PublicKey:       pubkey,
		ConversationKey: ck,
	}

	// add to pool
	p.sessions[pubkey] = session

	return session, nil
}

func (p *StaticKeySigner) HandleRequest(_ context.Context, event nostr.Event) (
	req Request,
	resp Response,
	eventResponse nostr.Event,
	err error,
) {
	if event.Kind != nostr.KindNostrConnect {
		return req, resp, eventResponse,
			fmt.Errorf("event kind is %d, but we expected %d", event.Kind, nostr.KindNostrConnect)
	}

	session, err := p.getOrCreateSession(event.PubKey)
	if err != nil {
		return req, resp, eventResponse, err
	}

	req, err = session.ParseRequest(event)
	if err != nil {
		return req, resp, eventResponse, fmt.Errorf("error parsing request: %w", err)
	}

	var secret string
	var harmless bool
	var result string
	var resultErr error

	switch req.Method {
	case "connect":
		if len(req.Params) >= 2 {
			secret = req.Params[1]
		}
		result = "ack"
		harmless = true
	case "get_public_key":
		result = nostr.HexEncodeToString(session.PublicKey[:])
		harmless = true
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
		err = evt.Sign(p.secretKey)
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
			resultErr = fmt.Errorf("first argument to 'nip04_encrypt' is not a valid pubkey hex")
			break
		}
		plaintext := req.Params[1]

		sharedSecret, err := nip44.GenerateConversationKey(thirdPartyPubkey, p.secretKey)
		if err != nil {
			resultErr = fmt.Errorf("failed to compute shared secret: %w", err)
			break
		}
		ciphertext, err := nip44.Encrypt(plaintext, sharedSecret)
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
		ciphertext := req.Params[1]

		sharedSecret, err := nip44.GenerateConversationKey(thirdPartyPubkey, p.secretKey)
		if err != nil {
			resultErr = fmt.Errorf("failed to compute shared secret: %w", err)
			break
		}
		plaintext, err := nip44.Decrypt(ciphertext, sharedSecret)
		if err != nil {
			resultErr = fmt.Errorf("failed to encrypt: %w", err)
			break
		}
		result = plaintext
	case "nip04_encrypt":
		if len(req.Params) != 2 {
			resultErr = fmt.Errorf("wrong number of arguments to 'nip04_encrypt'")
			break
		}
		thirdPartyPubkey, err := nostr.PubKeyFromHex(req.Params[0])
		if err != nil {
			resultErr = fmt.Errorf("first argument to 'nip04_encrypt' is not a valid pubkey hex")
			break
		}
		plaintext := req.Params[1]

		sharedSecret, err := nip04.ComputeSharedSecret(thirdPartyPubkey, p.secretKey)
		if err != nil {
			resultErr = fmt.Errorf("failed to compute shared secret: %w", err)
			break
		}
		ciphertext, err := nip04.Encrypt(plaintext, sharedSecret)
		if err != nil {
			resultErr = fmt.Errorf("failed to encrypt: %w", err)
			break
		}
		result = ciphertext
	case "nip04_decrypt":
		if len(req.Params) != 2 {
			resultErr = fmt.Errorf("wrong number of arguments to 'nip04_decrypt'")
			break
		}
		thirdPartyPubkey, err := nostr.PubKeyFromHex(req.Params[0])
		if err != nil {
			resultErr = fmt.Errorf("first argument to 'nip04_decrypt' is not a valid pubkey hex")
			break
		}
		ciphertext := req.Params[1]

		sharedSecret, err := nip04.ComputeSharedSecret(thirdPartyPubkey, p.secretKey)
		if err != nil {
			resultErr = fmt.Errorf("failed to compute shared secret: %w", err)
			break
		}
		plaintext, err := nip04.Decrypt(ciphertext, sharedSecret)
		if err != nil {
			resultErr = fmt.Errorf("failed to encrypt: %w", err)
			break
		}
		result = plaintext
	case "ping":
		result = "pong"
		harmless = true
	default:
		return req, resp, eventResponse,
			fmt.Errorf("unknown method '%s'", req.Method)
	}

	if resultErr == nil && p.AuthorizeRequest != nil {
		if !p.AuthorizeRequest(harmless, event.PubKey, secret) {
			resultErr = fmt.Errorf("unauthorized")
		}
	}

	resp, eventResponse, err = session.MakeResponse(req.ID, event.PubKey, result, resultErr)
	if err != nil {
		return req, resp, eventResponse, err
	}

	err = eventResponse.Sign(p.secretKey)
	if err != nil {
		return req, resp, eventResponse, err
	}

	return req, resp, eventResponse, err
}
