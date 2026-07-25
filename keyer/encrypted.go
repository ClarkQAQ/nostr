package keyer

import (
	"context"
	"fmt"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip04"
	"fiatjaf.com/nostr/nip44"
	"fiatjaf.com/nostr/nip49"
)

var _ nostr.Keyer = (*EncryptedKeySigner)(nil)

// EncryptedKeySigner is a signer that must ask the user for a password before every operation.
// It stores the private key in encrypted form (NIP-49) and uses a callback to request the password
// when needed for operations.
type EncryptedKeySigner struct {
	ncryptsec string
	pk        nostr.PubKey
	callback  func(context.Context) string
}

// GetPublicKey returns the public key associated with this signer.
// If the public key is not cached, it will decrypt the private key using the password
// callback to derive the public key.
func (es *EncryptedKeySigner) GetPublicKey(ctx context.Context) (nostr.PubKey, error) {
	if es.pk != nostr.ZeroPK {
		return es.pk, nil
	}
	password := es.callback(ctx)
	key, err := nip49.Decrypt(es.ncryptsec, password)
	if err != nil {
		return nostr.ZeroPK, err
	}
	pk := nostr.GetPublicKey(key)
	es.pk = pk
	return pk, nil
}

// SignEvent signs the provided event by first decrypting the private key
// using the password callback, then signing the event with the decrypted key.
func (es *EncryptedKeySigner) SignEvent(ctx context.Context, evt *nostr.Event) error {
	password := es.callback(ctx)
	sk, err := nip49.Decrypt(es.ncryptsec, password)
	if err != nil {
		return fmt.Errorf("invalid password: %w", err)
	}
	es.pk = evt.PubKey
	return evt.Sign(sk)
}

// Encrypt encrypts a plaintext message for a recipient using NIP-44.
// It first decrypts the private key using the password callback.
func (es EncryptedKeySigner) Encrypt(ctx context.Context, plaintext string, recipient nostr.PubKey) (c64 string, err error) {
	password := es.callback(ctx)
	sk, err := nip49.Decrypt(es.ncryptsec, password)
	if err != nil {
		return "", fmt.Errorf("invalid password: %w", err)
	}
	ck, err := nip44.GenerateConversationKey(recipient, sk)
	if err != nil {
		return "", err
	}
	return nip44.Encrypt(plaintext, ck)
}

// Decrypt decrypts a base64-encoded ciphertext from a sender using NIP-44.
// It first decrypts the private key using the password callback.
func (es EncryptedKeySigner) Nip04Encrypt(ctx context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	password := es.callback(ctx)
	sk, err := nip49.Decrypt(es.ncryptsec, password)
	if err != nil {
		return "", fmt.Errorf("invalid password: %w", err)
	}
	sharedSecret, err := nip04.ComputeSharedSecret(recipient, sk)
	if err != nil {
		return "", err
	}
	return nip04.Encrypt(plaintext, sharedSecret)
}

func (es EncryptedKeySigner) Nip04Decrypt(ctx context.Context, ciphertext string, sender nostr.PubKey) (string, error) {
	password := es.callback(ctx)
	sk, err := nip49.Decrypt(es.ncryptsec, password)
	if err != nil {
		return "", fmt.Errorf("invalid password: %w", err)
	}
	sharedSecret, err := nip04.ComputeSharedSecret(sender, sk)
	if err != nil {
		return "", err
	}
	return nip04.Decrypt(ciphertext, sharedSecret)
}

func (es EncryptedKeySigner) Decrypt(ctx context.Context, base64ciphertext string, sender nostr.PubKey) (plaintext string, err error) {
	password := es.callback(ctx)
	sk, err := nip49.Decrypt(es.ncryptsec, password)
	if err != nil {
		return "", fmt.Errorf("invalid password: %w", err)
	}
	ck, err := nip44.GenerateConversationKey(sender, sk)
	if err != nil {
		return "", err
	}
	return nip44.Encrypt(plaintext, ck)
}
