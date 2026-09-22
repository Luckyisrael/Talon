package compaction

import (
	"os"
	"strings"
	"testing"
)

// Prune is a no-op at or under the threshold and head+tail above it, with the
// omitted count honest. The polarities: small stays identical, large shrinks
// to exactly head+tail+marker.
func TestPrune(t *testing.T) {
	small := strings.Repeat("a", 100)
	if got := Prune(small); got != small {
		t.Errorf("small content changed under prune")
	}

	at := strings.Repeat("b", PruneThresholdBytes)
	if got := Prune(at); got != at {
		t.Errorf("content at the threshold was pruned — threshold is exclusive of equality")
	}

	large := strings.Repeat("H", PruneHeadBytes) + strings.Repeat("M", 10000) + strings.Repeat("T", PruneTailBytes)
	got := Prune(large)
	if len(got) >= len(large) {
		t.Errorf("large content did not shrink: %d -> %d", len(large), len(got))
	}
	if !strings.Contains(got, strings.Repeat("H", PruneHeadBytes)) {
		t.Error("head lost")
	}
	if !strings.Contains(got, strings.Repeat("T", PruneTailBytes)) {
		t.Error("tail lost")
	}
	if !strings.Contains(got, "bytes omitted") {
		t.Error("omitted count missing — the model must know what it did not see")
	}
	omitted := len(large) - PruneHeadBytes - PruneTailBytes
	if !strings.Contains(got, itoa(omitted)) {
		t.Errorf("omitted count wrong: want %d", omitted)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// Spill: under the threshold nothing changes; over it, the body lands on disk
// verbatim and the replacement carries the locator; the same body spilled
// twice is one file (hash-named).
func TestSpill(t *testing.T) {
	s, err := NewSpillStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSpillStore: %v", err)
	}

	small := "tiny output"
	rep, spilled, err := s.Spill("read_file", small)
	if err != nil || spilled || rep != small {
		t.Errorf("small content spilled: %q %v %v", rep, spilled, err)
	}

	body := strings.Repeat("x", SpillThresholdBytes+1)
	rep, spilled, err = s.Spill("run_shell", body)
	if err != nil {
		t.Fatalf("Spill: %v", err)
	}
	if !spilled {
		t.Fatal("oversized content did not spill")
	}
	if !strings.Contains(rep, "output too large") {
		t.Error("replacement lacks the honest marker")
	}
	loc := s.LocatorPath(rep)
	if loc == "" {
		t.Fatalf("locator not resolvable from replacement: %q", rep)
	}
	// The spilled file is verbatim.
	onDisk, err := readFileForTest(loc)
	if err != nil {
		t.Fatalf("re-reading spilled file: %v", err)
	}
	if onDisk != body {
		t.Errorf("spilled body differs: %d bytes on disk vs %d", len(onDisk), len(body))
	}

	// Same body again: same locator, one file.
	_, spilled2, err := s.Spill("run_shell", body)
	if err != nil || !spilled2 {
		t.Fatalf("re-spill failed: %v", err)
	}
	loc2 := s.LocatorPath(rep)
	if loc2 != loc {
		t.Errorf("same body spilled to a second file: %q vs %q", loc, loc2)
	}
}

// A locator outside the store does not resolve — the locator is not an
// arbitrary read token.
func TestLocatorScoping(t *testing.T) {
	s, err := NewSpillStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := s.LocatorPath("[saved to /etc/passwd]"); got != "" {
		t.Errorf("locator outside the store resolved: %q", got)
	}
	if got := s.LocatorPath("no locator here"); got != "" {
		t.Errorf("non-locator resolved: %q", got)
	}
}

// ResolvePath is the read tools' seam onto the store (W1.2): the locator line
// the model saw and the bare spill path both resolve to the filed body;
// anything outside the store — including a lexical hop via .. — does not.
// This is what makes the compaction README's promise ("read_file on it
// returns the body verbatim") true without turning a locator into an
// arbitrary file-read token.
func TestResolvePath(t *testing.T) {
	s, err := NewSpillStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("x", SpillThresholdBytes+10)
	rep, spilled, err := s.Spill("run_shell", body)
	if err != nil || !spilled {
		t.Fatalf("spill: %v %v", spilled, err)
	}

	byLocator := s.ResolvePath(rep)
	if byLocator == "" {
		t.Fatal("locator line did not resolve")
	}
	if byPath := s.ResolvePath(byLocator); byPath != byLocator {
		t.Errorf("bare path resolved to %q, want %q", byPath, byLocator)
	}
	// The locator as it lands in a transcript: wrapped in surrounding text
	// and whitespace.
	if got := s.ResolvePath("turn result:\n  " + rep + "\n"); got != byLocator {
		t.Errorf("wrapped locator resolved to %q, want %q", got, byLocator)
	}

	for _, bad := range []string{
		"",
		"no locator here",
		"[saved to /etc/passwd]",
		s.dir + "/../escape.txt", // lexical hop out of the store
		s.dir + "/notes.md",      // wrong suffix — not a spill file
	} {
		if got := s.ResolvePath(bad); got != "" {
			t.Errorf("ResolvePath(%q) = %q, want empty", bad, got)
		}
	}
}

func readFileForTest(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}
