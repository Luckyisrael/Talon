package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubSpill stands in for compaction.SpillStore: it resolves locator-shaped
// candidates to one absolute file. This pins read_file's half of W1.2 — the
// resolved file is read verbatim, outside the FS root, with the same line
// window a workspace read gets — while the store's own containment check is
// pinned in package compaction (TestResolvePath).
type stubSpill struct{ path string }

func (s stubSpill) ResolvePath(candidate string) string {
	if s.path != "" && (candidate == "spilled" || strings.Contains(candidate, "saved to ")) {
		return s.path
	}
	return ""
}

func TestReadFileResolvesSpillLocators(t *testing.T) {
	dir := t.TempDir()
	filed := filepath.Join(dir, "hash.txt")
	body := strings.Repeat("spilled-line\n", 5000)
	if err := os.WriteFile(filed, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	rf := &ReadFile{FS: NewFS(t.TempDir()), Spill: stubSpill{path: filed}}

	// The locator line, verbatim as the model saw it: the whole body is
	// parsed (5000 lines + trailing newline = 5001) and windowed like any
	// read — the continuation nudge names the true line total.
	res, err := rf.Execute(context.Background(), json.RawMessage(`{"path":"60000 bytes saved to /x/hash.txt] tail"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("spill read errored: %s", res.Content)
	}
	if !strings.Contains(res.Content, "of 5001") {
		t.Errorf("whole spilled body not parsed (want line total 5001): %.120s", res.Content)
	}
	if !strings.Contains(res.Content, "spilled-line") {
		t.Errorf("body content missing: %.120s", res.Content)
	}

	// Paging works on a spilled body like any other file: the requested
	// offset's lines come back (no continuation nudge — this window reaches
	// EOF, which is correct; the first read above proves the nudge names
	// the true total).
	page, err := rf.Execute(context.Background(), json.RawMessage(`{"path":"spilled","offset":5000,"limit":5}`))
	if err != nil || page.IsError {
		t.Fatalf("page: %v %s", err, page.Content)
	}
	if !strings.Contains(page.Content, "5000\tspilled-line") {
		t.Errorf("offset paging broken on spilled body: %q", page.Content)
	}

	// Past the end reports the real filed path, not the locator line.
	end, err := rf.Execute(context.Background(), json.RawMessage(`{"path":"spilled","offset":99999}`))
	if err != nil {
		t.Fatal(err)
	}
	if !end.IsError || !strings.Contains(end.Content, "past the end of "+filed) {
		t.Errorf("past-end error should name the filed path: isError=%v %q", end.IsError, end.Content)
	}

	// Nil resolver: unchanged behavior — locator text is just a weird
	// relative path, and the filed body does not leak.
	plain := &ReadFile{FS: NewFS(t.TempDir())}
	res2, err := plain.Execute(context.Background(), json.RawMessage(`{"path":"60000 bytes saved to /x/hash.txt]"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res2.Content, "spilled-line") {
		t.Error("nil resolver leaked the spilled body")
	}
}
