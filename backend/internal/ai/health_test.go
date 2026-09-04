package ai

import (
	"errors"
	"testing"
)

func TestHealthUnconfigured(t *testing.T) {
	c := &Client{}
	if ok, _ := c.Health(); ok {
		t.Error("empty client should be unhealthy")
	}
}

func TestHealthFailureAgesOut(t *testing.T) {
	c := &Client{baseURL: "https://x", apiKey: "k", model: "m"}
	if ok, _ := c.Health(); !ok {
		t.Fatal("configured client with no failures should be healthy")
	}
	c.noteFailure(errors.New("boom"))
	if ok, detail := c.Health(); ok || detail == "" {
		t.Errorf("recent failure should degrade health, got ok=%v %q", ok, detail)
	}
}

func TestNoteFailureTruncates(t *testing.T) {
	c := &Client{baseURL: "https://x", apiKey: "k", model: "m"}
	long := make([]byte, 500)
	for i := range long {
		long[i] = 'x'
	}
	c.noteFailure(errors.New(string(long)))
	if len(c.lastErr) > 200 {
		t.Errorf("health detail not truncated: %d chars", len(c.lastErr))
	}
}
