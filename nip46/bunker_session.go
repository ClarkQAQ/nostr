package nip46

import (
	"errors"
	"fmt"
	"slices"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip44"
)

type Session struct {
	PublicKey       nostr.PubKey
	ConversationKey [32]byte

	duplicatesBuf [6]string
	serial        int
}

type RelayReadWrite struct {
	Read  bool `json:"read"`
	Write bool `json:"write"`
}

var AlreadyHandled = errors.New("already handled this request")

func (s *Session) ParseRequest(event nostr.Event) (Request, error) {
	var req Request

	plain, err := nip44.Decrypt(event.Content, s.ConversationKey)
	if err != nil {
		return req, fmt.Errorf("failed to decrypt event from %s: (nip44: %w)", event.PubKey, err)
	}

	err = json.Unmarshal([]byte(plain), &req)

	// discard duplicates
	if slices.Contains(s.duplicatesBuf[:], req.ID) {
		return req, AlreadyHandled
	}

	s.duplicatesBuf[s.serial%len(s.duplicatesBuf)] = req.ID
	s.serial++

	return req, err
}

func (s Session) MakeResponse(
	id string,
	requester nostr.PubKey,
	result string,
	err error,
) (resp Response, evt nostr.Event, error error) {
	if err != nil {
		resp = Response{
			ID:    id,
			Error: err.Error(),
		}
	} else if result != "" {
		resp = Response{
			ID:     id,
			Result: result,
		}
	}

	jresp, _ := json.Marshal(resp)
	ciphertext, err := nip44.Encrypt(string(jresp), s.ConversationKey)
	if err != nil {
		return resp, evt, fmt.Errorf("failed to encrypt result: %w", err)
	}
	evt.Content = ciphertext
	evt.CreatedAt = nostr.Now()
	evt.Kind = nostr.KindNostrConnect
	evt.Tags = nostr.Tags{nostr.Tag{"p", requester.Hex()}}

	return resp, evt, nil
}
