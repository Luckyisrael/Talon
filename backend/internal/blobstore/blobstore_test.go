package blobstore

import (
	"context"
	"path/filepath"
	"testing"
)

func TestLocalStoreRoundTrip(t *testing.T) {
	s := LocalStore{Dir: filepath.Join(t.TempDir(), "blobs")}
	body := []byte("pixels")
	if err := s.Put(context.Background(), "9/1-shot.png", body, "image/png"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	back, err := s.Get(context.Background(), "9/1-shot.png")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(back) != string(body) {
		t.Error("round-tripped bytes differ")
	}
	if _, err := s.Get(context.Background(), "9/missing.png"); err == nil {
		t.Error("missing key must error")
	}
}

func TestNewR2StoreFromEnvAbsent(t *testing.T) {
	t.Setenv("R2_ACCOUNT_ID", "")
	t.Setenv("R2_ACCESS_KEY_ID", "")
	t.Setenv("R2_SECRET_ACCESS_KEY", "")
	t.Setenv("R2_BUCKET", "")
	if _, ok := NewR2StoreFromEnv(); ok {
		t.Error("empty env must not yield a usable R2 store")
	}
}

func TestNewR2StoreFromEnvPartial(t *testing.T) {
	t.Setenv("R2_ACCOUNT_ID", "acct")
	t.Setenv("R2_ACCESS_KEY_ID", "")
	t.Setenv("R2_SECRET_ACCESS_KEY", "secret")
	t.Setenv("R2_BUCKET", "bucket")
	if _, ok := NewR2StoreFromEnv(); ok {
		t.Error("partial env must not yield a half-configured client")
	}
}
