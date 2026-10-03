package release

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
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

// FM-KEYSa: a keystore file no row owns is unreadable by construction —
// its password lived only in the row that is gone. Generation must move it
// aside, not crash into it wearing keytool's "password was incorrect"
// disguise (the live failure: export job 1, server log 05:58:31 —
// project_keystores empty, keystores/project-1.jks dated the day before,
// and the row is written only AFTER generation, so every future export
// repeated the crash).
//
// RED pre-fix: Ensure opens the orphan with a fresh random password and
// fails `keytool failed: … keystore password was incorrect`, no row saved.
func TestEnsureMovesAsideOrphanFileWithNoRow(t *testing.T) {
	if _, err := os.Stat("/usr/bin/keytool"); err != nil {
		t.Skip("keytool not available")
	}
	s := testStore(t)
	pid := testProject(t, s, "orphan@test.com", "proj-o", "com.test.orphan")
	dir := t.TempDir()
	k := &Keystore{Store: s, Cipher: testCipher(t), Dir: dir}

	// A valid keystore "from another database": generated directly, so no
	// row in THIS store has ever recorded its password — the exact orphan
	// state a DB restore lands in.
	path := filepath.Join(dir, fmt.Sprintf("project-%d.jks", pid))
	alias := fmt.Sprintf("project-%d", pid)
	seedPass := "orphan-password-no-row-knows"
	pwPath, cleanup, err := passFile(seedPass)
	if err != nil {
		t.Fatal(err)
	}
	gen := exec.Command("keytool", "-genkeypair",
		"-keystore", path, "-storepass:file", pwPath,
		"-alias", alias, "-keypass:file", pwPath,
		"-keyalg", "RSA", "-dname", "CN=orphan")
	if out, err := gen.CombinedOutput(); err != nil {
		cleanup()
		t.Fatalf("seed keystore: %v: %s", err, out)
	}
	cleanup()

	rec, err := k.Ensure(context.Background(), pid)
	if err != nil {
		t.Fatalf("FM-KEYSa: Ensure must clear an orphan file aside and generate — nothing owns it and its password died with the missing row. Pre-fix this fails exactly like the live export job: %v", err)
	}
	// The orphan is preserved beside the canonical path (bytes kept for an
	// operator — it may be a published app's signing key), and `path` now
	// holds the NEW keystore: Ensure's internal keyFingerprint(path,
	// freshPassword) already proved the fresh password opens it.
	if matches, _ := filepath.Glob(path + ".orphaned-*"); len(matches) != 1 {
		t.Errorf("want the orphan preserved as %s.orphaned-*, got %v", filepath.Base(path), matches)
	}
	pw, err := k.Password(rec)
	if err != nil || pw == "" || pw == seedPass {
		t.Errorf("new record unusable (err=%v, fresh=%v) — the saved password must open the NEW keystore, not the orphan", err, pw != seedPass)
	}
	// The row now exists: the next Ensure reuses instead of regenerating.
	rec2, err := k.Ensure(context.Background(), pid)
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if rec2.Path != rec1Path(rec) || rec2.Fingerprint != rec.Fingerprint {
		t.Errorf("keystore not reused after regeneration: %+v vs %+v", rec2, rec)
	}
}

func rec1Path(rec *store.KeystoreRecord) string { return rec.Path }
