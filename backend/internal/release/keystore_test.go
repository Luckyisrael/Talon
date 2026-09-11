package release

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/secrets"
	"github.com/luckyisrael/talon/backend/internal/store"
)

func testCipher(t *testing.T) *secrets.Cipher {
	t.Helper()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal(err)
	}
	c, err := secrets.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func testProject(t *testing.T, s *store.Store, userEmail, name, pkg string) int64 {
	t.Helper()
	u, err := s.CreateUser(userEmail, "hash")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(u.ID, name, "1.0.0", name+"-slug", pkg, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func testStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEnsureGeneratesOnce(t *testing.T) {
	if _, err := os.Stat("/usr/bin/keytool"); err != nil {
		t.Skip("keytool not available")
	}
	s := testStore(t)
	pid := testProject(t, s, "a@test.com", "proj", "com.test.proj")
	k := &Keystore{Store: s, Cipher: testCipher(t), Dir: t.TempDir()}

	rec1, err := k.Ensure(context.Background(), pid)
	if err != nil {
		t.Fatal(err)
	}
	if rec1.Fingerprint == "" {
		t.Error("expected a fingerprint")
	}
	fi, err := os.Stat(rec1.Path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("keystore mode = %o, want 600", fi.Mode().Perm())
	}
	if rec1.EncPassword == "" {
		t.Error("expected an encrypted password")
	}

	// Second call reuses the same file, not a rotation.
	rec2, err := k.Ensure(context.Background(), pid)
	if err != nil {
		t.Fatal(err)
	}
	if rec2.Path != rec1.Path || rec2.Fingerprint != rec1.Fingerprint {
		t.Errorf("keystore not reused: %+v vs %+v", rec1, rec2)
	}

	pw, err := k.Password(rec2)
	if err != nil || pw == "" {
		t.Errorf("password round-trip failed: %v", err)
	}
}

func TestProjectsGetDistinctKeystores(t *testing.T) {
	if _, err := os.Stat("/usr/bin/keytool"); err != nil {
		t.Skip("keytool not available")
	}
	s := testStore(t)
	pa := testProject(t, s, "a@test.com", "proja", "com.test.a")
	pb := testProject(t, s, "b@test.com", "projb", "com.test.b")
	k := &Keystore{Store: s, Cipher: testCipher(t), Dir: t.TempDir()}

	a, err := k.Ensure(context.Background(), pa)
	if err != nil {
		t.Fatal(err)
	}
	b, err := k.Ensure(context.Background(), pb)
	if err != nil {
		t.Fatal(err)
	}
	if a.Fingerprint == b.Fingerprint {
		t.Error("two projects share a keystore fingerprint")
	}
}
