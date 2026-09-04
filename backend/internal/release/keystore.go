// Package release implements the export pipeline: per-project signing
// keystores, isolated release builds, dApp Store manifests, and store-asset
// validation. It never touches the dev loop, the user's publisher wallet, or
// any private key material beyond the keystores it generates itself.
package release

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/seekersstudio/backend/internal/secrets"
	"github.com/seekersstudio/backend/internal/store"
)

// KeystoreDir holds one <project-id>.jks per project, mode 0600. Overridable
// for tests via Keystore struct field.
func KeystoreDir() string {
	if dir := strings.TrimSpace(os.Getenv("RELEASE_KEYSTORE_DIR")); dir != "" {
		return dir
	}
	return "./keystores"
}

type Keystore struct {
	Store  *store.Store
	Cipher *secrets.Cipher
	Dir    string
}

func (k *Keystore) dir() string {
	if k.Dir != "" {
		return k.Dir
	}
	return KeystoreDir()
}

// Ensure returns the project's keystore, generating and persisting it on
// first export. Generation is the only writer; every later export reuses the
// same file, alias, and password — app stores reject updates signed with a
// different key, so rotation is deliberately unsupported here.
func (k *Keystore) Ensure(ctx context.Context, projectID int64) (*store.KeystoreRecord, error) {
	if existing, err := k.Store.GetKeystore(projectID); err == nil {
		if _, statErr := os.Stat(existing.Path); statErr == nil {
			return existing, nil
		}
		log.Printf("release: keystore row exists for project %d but file is gone; regenerating", projectID)
	}

	dir := k.dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("could not create keystore dir: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("project-%d.jks", projectID))
	alias := fmt.Sprintf("project-%d", projectID)

	password, err := randomPassword()
	if err != nil {
		return nil, err
	}
	enc, err := k.Cipher.Encrypt([]byte(password))
	if err != nil {
		return nil, fmt.Errorf("could not encrypt keystore password: %w", err)
	}

	// The password travels via a 0600 temp file, never argv — command lines
	// are visible to every user on the host through ps.
	pwPath, cleanup, err := passFile(password)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	gen := exec.CommandContext(ctx, "keytool", "-genkeypair",
		"-keystore", path, "-storepass:file", pwPath,
		"-alias", alias, "-keypass:file", pwPath,
		"-keyalg", "RSA", "-keysize", "2048", "-validity", "10950",
		"-dname", fmt.Sprintf("CN=SeekerStudio Project %d, OU=SeekerStudio, O=SeekerStudio", projectID),
	)
	if out, err := gen.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("keytool failed: %v: %s", err, truncate(string(out), 300))
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, err
	}

	fp, err := keyFingerprint(ctx, path, password, alias)
	if err != nil {
		return nil, err
	}
	rec, err := k.Store.SaveKeystore(projectID, path, alias, fp, enc)
	if err != nil {
		return nil, err
	}
	log.Printf("release: generated keystore for project %d (fingerprint %s)", projectID, fp)
	return rec, nil
}

// Password decrypts the stored password for signing. Callers must not log it.
func (k *Keystore) Password(rec *store.KeystoreRecord) (string, error) {
	raw, err := k.Cipher.Decrypt(rec.EncPassword)
	if err != nil {
		return "", fmt.Errorf("could not decrypt keystore password: %w", err)
	}
	return string(raw), nil
}

var sha256Re = regexp.MustCompile(`(?i)SHA256:\s*([0-9A-F:]+)`)

func keyFingerprint(ctx context.Context, path, password, alias string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pwPath, cleanup, err := passFile(password)
	if err != nil {
		return "", err
	}
	defer cleanup()
	out, err := exec.CommandContext(ctx, "keytool", "-list", "-v",
		"-keystore", path, "-storepass:file", pwPath, "-alias", alias).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("keytool list failed: %v", err)
	}
	if m := sha256Re.FindStringSubmatch(string(out)); m != nil {
		return m[1], nil
	}
	return "", fmt.Errorf("no SHA256 fingerprint in keytool output")
}

func randomPassword() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

// passFile stages a secret for a subprocess that only accepts file-based
// passwords (keytool -storepass:file). The file is 0600 and the caller
// removes it via cleanup. Secrets must never ride argv.
func passFile(secret string) (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "release-pass-*")
	if err != nil {
		return "", nil, err
	}
	path = f.Name()
	cleanup = func() { os.Remove(path) }
	if _, err := f.WriteString(secret); err != nil {
		f.Close()
		cleanup()
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
