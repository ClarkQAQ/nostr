package keyer

import (
	"context"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip04"
	"fiatjaf.com/nostr/nip44"
	"github.com/puzpuzpuz/xsync/v3"
)

var _ nostr.Keyer = (*KeySigner)(nil)

// KeySigner is a signer that holds the private key in memory
type KeySigner struct {
	sk [32]byte
	pk nostr.PubKey

	conversationKeys *xsync.MapOf[nostr.PubKey, [32]byte]
}

// NewPlainKeySigner creates a new KeySigner from a private key.
// Returns an error if the private key is invalid.
func NewPlainKeySigner(sec [32]byte) KeySigner {
	return KeySigner{sec, nostr.GetPublicKey(sec), xsync.NewMapOf[nostr.PubKey, [32]byte]()}
}

// SignEvent signs the provided event with the signer's private key.
// It sets the event's ID, PubKey, and Sig fields.
func (ks KeySigner) SignEvent(ctx context.Context, evt *nostr.Event) error { return evt.Sign(ks.sk) }

// GetPublicKey returns the public key associated with this signer.
func (ks KeySigner) GetPublicKey(ctx context.Context) (nostr.PubKey, error) { return ks.pk, nil }

// Encrypt encrypts a plaintext message for a recipient using NIP-44.
// It caches conversation keys for efficiency in repeated operations.
func (ks KeySigner) Encrypt(ctx context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	ck, ok := ks.conversationKeys.Load(recipient)
	if !ok {
		var err error
		ck, err = nip44.GenerateConversationKey(recipient, ks.sk)
		if err != nil {
			return "", err
		}
		ks.conversationKeys.Store(recipient, ck)
	}
	return nip44.Encrypt(plaintext, ck)
}

// Decrypt decrypts a base64-encoded ciphertext from a sender using NIP-44.
// It caches conversation keys for efficiency in repeated operations.
func (ks KeySigner) Nip04Encrypt(ctx context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	sharedSecret, err := nip04.ComputeSharedSecret(recipient, ks.sk)
	if err != nil {
		return "", err
	}
	return nip04.Encrypt(plaintext, sharedSecret)
}

func (ks KeySigner) Nip04Decrypt(ctx context.Context, ciphertext string, sender nostr.PubKey) (string, error) {
	sharedSecret, err := nip04.ComputeSharedSecret(sender, ks.sk)
	if err != nil {
		return "", err
	}
	return nip04.Decrypt(ciphertext, sharedSecret)
}

func (ks KeySigner) Decrypt(ctx context.Context, base64ciphertext string, sender nostr.PubKey) (string, error) {
	ck, ok := ks.conversationKeys.Load(sender)
	if !ok {
		var err error
		ck, err = nip44.GenerateConversationKey(sender, ks.sk)
		if err != nil {
			return "", err
		}
		ks.conversationKeys.Store(sender, ck)
	}
	return nip44.Decrypt(base64ciphertext, ck)
}
