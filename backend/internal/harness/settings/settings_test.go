package settings

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var (
	timeNow    = time.Now
	timeSecond = time.Second
)

// The seed serves when no file exists; a file overrides once present; a
// changed file (mtime moves) is re-read on the next snapshot — routing flips
// without a restart.
func TestHotReload(t *testing.T) {
	seed := Document{Routing: Routing{FallbackModel: "seed-fallback", ShortPromptModel: "seed-short"}}
	path := filepath.Join(t.TempDir(), "settings.json")

	d := NewDoc(seed, path)
	if got := d.Snapshot().Routing.FallbackModel; got != "seed-fallback" {
		t.Fatalf("healthy: seed not serving before a file exists: %q", got)
	}

	// Write a live document.
	if err := d.Write(Document{Routing: Routing{FallbackModel: "live-fallback",
		Tiers: map[string]string{"cheap": "c1"}}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := d.Snapshot().Routing.FallbackModel; got != "live-fallback" {
		t.Fatalf("fault: live file not serving: %q", got)
	}
	if got := d.Snapshot().Routing.Tiers["cheap"]; got != "c1" {
		t.Errorf("tiers lost: %q", got)
	}

	// The file changes on disk (an operator edit): next snapshot flips.
	if err := os.WriteFile(path, []byte(`{"routing":{"fallbackModel":"edited-fallback"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Force the mtime to move: some filesystems share timestamp granularity.
	newTime := timeNow().Add(2 * timeSecond)
	_ = os.Chtimes(path, newTime, newTime)
	if got := d.Snapshot().Routing.FallbackModel; got != "edited-fallback" {
		t.Fatalf("fault: edit not picked up without restart: %q", got)
	}
}

// A malformed file degrades honestly: the last good copy keeps serving and
// BadFile reports it — routing never silently reverts to the seed.
func TestMalformedFile(t *testing.T) {
	seed := Document{Routing: Routing{FallbackModel: "seed-fallback"}}
	path := filepath.Join(t.TempDir(), "settings.json")
	d := NewDoc(seed, path)

	if err := d.Write(Document{Routing: Routing{FallbackModel: "good-fallback"}}); err != nil {
		t.Fatal(err)
	}
	// Corrupt it, with a moved mtime.
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	newTime := timeNow().Add(2 * timeSecond)
	_ = os.Chtimes(path, newTime, newTime)

	if got := d.Snapshot().Routing.FallbackModel; got != "good-fallback" {
		t.Fatalf("fault: malformed file dropped routing to %q", got)
	}
	if !d.BadFile() {
		t.Error("fault: bad file not reported")
	}
}

// Snapshot isolation: two snapshots see the same document; mutating one's
// maps does not corrupt the next.
func TestSnapshotIsolation(t *testing.T) {
	d := NewDoc(Document{Routing: Routing{Tiers: map[string]string{"a": "1"}}}, "")
	s1 := d.Snapshot()
	s1.Routing.Tiers["a"] = "mutated"
	s2 := d.Snapshot()
	if s2.Routing.Tiers["a"] != "1" {
		t.Fatal("fault: snapshot maps share storage — concurrent readers corrupt routing")
	}
}
