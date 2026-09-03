// Package secrets encrypts small values — OAuth tokens, chiefly — for storage at
// rest, so a database file that leaks does not also leak the credentials it holds.
//
// It is deliberately narrow: AES-256-GCM with a per-message random nonce, keyed by
// a single symmetric key the process reads from its environment. There is no key
// rotation and no per-record key; a key change invalidates everything sealed under
// the old one, which for short-lived access tokens (re-obtainable by reconnecting)
// is an acceptable trade for the simplicity.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// Cipher seals and opens secrets with AES-256-GCM. The nonce is generated per
// message and prepended to the ciphertext, so the same plaintext encrypts
// differently every time and each stored value carries what it needs to be
// opened. GCM is authenticated: a tampered or truncated ciphertext fails to open
// rather than returning garbage.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher builds a Cipher from a 32-byte key (AES-256). A key of any other
// length is a programming error, not a runtime condition, and is reported as one.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secrets: key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// FromPassphrase derives a 32-byte AES-256 key from an arbitrary passphrase via
// SHA-256, so any configured string can serve as the key without the operator
// having to produce exactly 32 raw bytes. The passphrase's entropy still bounds
// the key's, so the caller enforces a minimum length and fails closed in
// production when it is unset (see gitremote.Load). SHA-256 of a 32-byte input
// always yields 32 bytes, so NewCipher here cannot fail.
func FromPassphrase(passphrase string) *Cipher {
	sum := sha256.Sum256([]byte(passphrase))
	c, _ := NewCipher(sum[:])
	return c
}

// Encrypt seals plaintext and returns base64(nonce || ciphertext), safe to store
// in a text column.
func (c *Cipher) Encrypt(plaintext []byte) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	// Seal appends the ciphertext to its first argument (the nonce), so the
	// returned blob is nonce||ciphertext in one allocation.
	sealed := c.aead.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt. It fails if the input is not valid base64, is shorter
// than a nonce, or was sealed under a different key (GCM authentication).
func (c *Cipher) Decrypt(encoded string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	ns := c.aead.NonceSize()
	if len(raw) < ns {
		return nil, errors.New("secrets: ciphertext too short")
	}
	nonce, ciphertext := raw[:ns], raw[ns:]
	return c.aead.Open(nil, nonce, ciphertext, nil)
}
