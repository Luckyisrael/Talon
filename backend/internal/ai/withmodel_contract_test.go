package ai

import (
	"net/http"
	"reflect"
	"testing"
	"time"
)

// TestWithModelCommentContract pins the claims WithModel's doc comment
// makes (law 12: comment-code lies of the WithModel class). Every clause
// of that comment is an assertion here — if the comment grows a claim, it
// grows an assertion in the same commit, and a clone edit that stops
// honoring the prose fails make evals by name.
//
// The "never struct-copied" clause itself is enforced by copylocks vet
// (the make vet stage): a struct copy of Client is rejected at compile
// time. What this test pins is the behavior the clause exists to protect —
// the copy shares what it promises to share, and its health state and
// mutex are its own.
func TestWithModelCommentContract(t *testing.T) {
	hook := func(attempt, maxRetries int, reason string, after time.Duration) {}
	pacer := &requestPacer{interval: 8 * time.Second}
	orig := &Client{
		protocol:     "anthropic",
		baseURL:      "https://gateway.example/v1",
		apiKey:       "key",
		model:        "model-a",
		providerName: "prov",
		httpClient:   &http.Client{Timeout: time.Second},
		workspaceDir: "/ws",
		lastErr:      "connection refused",
		lastErrAt:    time.Unix(1700000000, 0),
		pacer:        pacer,
		retryHook:    hook,
	}

	cp := orig.WithModel("model-b")

	// "returns a client pointed at a different model" — and only the model
	// differs: the original keeps its own identity.
	if cp.model != "model-b" {
		t.Errorf("WithModel copy points at %q, want model-b", cp.model)
	}
	if orig.model != "model-a" {
		t.Errorf("WithModel mutated the original's model to %q", orig.model)
	}

	// "on the same endpoint with the same credentials".
	if cp.protocol != orig.protocol || cp.baseURL != orig.baseURL ||
		cp.apiKey != orig.apiKey || cp.providerName != orig.providerName {
		t.Errorf("WithModel changed endpoint/credentials: got (%s %s %s %s), want the original's",
			cp.protocol, cp.baseURL, cp.apiKey, cp.providerName)
	}

	// "shares the (read-only) http client, the workspace, the pacer and any
	// retry hook".
	if cp.httpClient != orig.httpClient {
		t.Error("comment promises a shared http client; the copy built its own")
	}
	if cp.workspaceDir != orig.workspaceDir {
		t.Errorf("comment promises a shared workspace: copy %q, original %q", cp.workspaceDir, orig.workspaceDir)
	}
	if cp.pacer != orig.pacer {
		t.Error("comment promises a shared pacer; the copy built its own")
	}
	if reflect.ValueOf(cp.retryHook).Pointer() != reflect.ValueOf(orig.retryHook).Pointer() {
		t.Error("comment promises a shared retry hook; the copy built its own")
	}

	// "starts from the original's health snapshot".
	if cp.lastErr != orig.lastErr || !cp.lastErrAt.Equal(orig.lastErrAt) {
		t.Errorf("copy health (%q at %v) does not mirror the original (%q at %v)",
			cp.lastErr, cp.lastErrAt, orig.lastErr, orig.lastErrAt)
	}

	// "never duplicated, so the health mutex" is the copy's own: holding
	// the original's mutex must not block the copy's.
	orig.mu.Lock()
	if !cp.mu.TryLock() {
		orig.mu.Unlock()
		t.Fatal("WithModel shares the health mutex with its original — the comment promises a field-by-field clone")
	}
	cp.mu.Unlock()
	orig.mu.Unlock()

	// Divergence after the copy: one client's later health is not the
	// other's.
	cp.lastErr = "copy-only"
	if orig.lastErr != "connection refused" {
		t.Errorf("mutating the copy's health leaked into the original: %q", orig.lastErr)
	}
}
