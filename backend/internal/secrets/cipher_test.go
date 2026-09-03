package secrets

import (
	"bytes"
	"strings"
	"testing"
)

func TestCipherRoundTrip(t *testing.T) {
	c := FromPassphrase("a-sufficiently-long-development-passphrase")
	plaintext := []byte("gho_exampletokenvalue1234567890")

	sealed, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if strings.Contains(sealed, string(plaintext)) {
		t.Fatal("ciphertext contains the plaintext — it is not actually encrypted")
	}

	opened, err := c.Decrypt(sealed)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("round trip mismatch: got %q want %q", opened, plaintext)
	}
}

func TestCipherNonceIsRandom(t *testing.T) {
	c := FromPassphrase("passphrase-passphrase-passphrase")
	a, _ := c.Encrypt([]byte("same"))
	b, _ := c.Encrypt([]byte("same"))
	if a == b {
		t.Fatal("two encryptions of the same plaintext are identical — the nonce is not random")
	}
}

func TestCipherWrongKeyFails(t *testing.T) {
	sealed, err := FromPassphrase("the-original-encryption-passphrase").Encrypt([]byte("secret"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// A value sealed under one key must not open under another. This is what makes
	// a rotated/incorrect GIT_TOKEN_ENCRYPTION_KEY fail closed rather than return
	// garbage that later looks like a valid token.
	if _, err := FromPassphrase("a-completely-different-passphrase").Decrypt(sealed); err == nil {
		t.Fatal("decrypt succeeded under the wrong key")
	}
}

func TestCipherRejectsMalformed(t *testing.T) {
	c := FromPassphrase("passphrase-passphrase-passphrase")
	if _, err := c.Decrypt("not valid base64 !!!"); err == nil {
		t.Fatal("expected an error decoding non-base64 input")
	}
	if _, err := c.Decrypt("YWJj"); err == nil { // "abc" — shorter than a nonce
		t.Fatal("expected an error on a too-short ciphertext")
	}
}

func TestNewCipherRejectsBadKeyLength(t *testing.T) {
	if _, err := NewCipher([]byte("too short")); err == nil {
		t.Fatal("expected an error for a non-32-byte key")
	}
}
