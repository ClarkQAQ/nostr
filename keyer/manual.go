package keyer

import (
	"context"

	"fiatjaf.com/nostr"
)

var _ nostr.Keyer = (*ManualSigner)(nil)

// ManualSigner is a signer that delegates all operations to user-provided functions.
// It can be used when an app wants to ask the user or some custom server to manually provide a
// signed event or an encrypted or decrypted payload by copy-and-paste, for example, or when the
// app wants to implement custom signing logic.
type ManualSigner struct {
	// ManualGetPublicKey is called when the public key is needed
	ManualGetPublicKey func(context.Context) (nostr.PubKey, error)

	// ManualSignEvent is called when an event needs to be signed
	ManualSignEvent func(context.Context, *nostr.Event) error

	// ManualEncrypt is called when a message needs to be encrypted (NIP-44)
	ManualEncrypt func(ctx context.Context, plaintext string, recipientPublicKey nostr.PubKey) (base64ciphertext string, err error)

	// ManualDecrypt is called when a message needs to be decrypted (NIP-44)
	ManualDecrypt func(ctx context.Context, base64ciphertext string, senderPublicKey nostr.PubKey) (plaintext string, err error)

	// ManualNip04Encrypt is called when a message needs to be encrypted with NIP-04
	ManualNip04Encrypt func(ctx context.Context, plaintext string, recipientPublicKey nostr.PubKey) (ciphertext string, err error)

	// ManualNip04Decrypt is called when a message needs to be decrypted with NIP-04
	ManualNip04Decrypt func(ctx context.Context, ciphertext string, senderPublicKey nostr.PubKey) (plaintext string, err error)
}

// SignEvent delegates event signing to the ManualSignEvent function.
func (ms ManualSigner) SignEvent(ctx context.Context, evt *nostr.Event) error {
	return ms.ManualSignEvent(ctx, evt)
}

// GetPublicKey delegates public key retrieval to the ManualGetPublicKey function.
func (ms ManualSigner) GetPublicKey(ctx context.Context) (nostr.PubKey, error) {
	return ms.ManualGetPublicKey(ctx)
}

// Encrypt delegates encryption to the ManualEncrypt function.
func (ms ManualSigner) Encrypt(ctx context.Context, plaintext string, recipient nostr.PubKey) (c64 string, err error) {
	return ms.ManualEncrypt(ctx, plaintext, recipient)
}

// Decrypt delegates decryption to the ManualDecrypt function.
func (ms ManualSigner) Decrypt(ctx context.Context, base64ciphertext string, sender nostr.PubKey) (plaintext string, err error) {
	return ms.ManualDecrypt(ctx, base64ciphertext, sender)
}

// Nip04Encrypt delegates NIP-04 encryption to the ManualNip04Encrypt function.
func (ms ManualSigner) Nip04Encrypt(ctx context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	return ms.ManualNip04Encrypt(ctx, plaintext, recipient)
}

// Nip04Decrypt delegates NIP-04 decryption to the ManualNip04Decrypt function.
func (ms ManualSigner) Nip04Decrypt(ctx context.Context, ciphertext string, sender nostr.PubKey) (string, error) {
	return ms.ManualNip04Decrypt(ctx, ciphertext, sender)
}
