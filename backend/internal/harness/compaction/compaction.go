// Package compaction is the H8 context-economy toolkit: the tool-result
// pruner and the spill store, extracted from DSH's compaction contracts and
// adapted to Talon's turn shapes.
//
// Two distinct disciplines live here:
//
//   - Prune shrinks oversized tool results for the wire: a giant read_file
//     body is trimmed to a head (the beginning usually carries the imports
//     and structure) and a tail (the errors land at the end), with the byte
//     counts named so the model knows what it did not see. Pruning is a
//     wire-copy transform: the log's stored copy keeps full fidelity.
//   - Spill moves a truly oversized output out of the conversation entirely:
//     the body lands on disk under a locator, and the model sees the locator
//     plus a summary line. The model re-reads by range through the normal
//     file tools.
//
// Thresholds follow DSH: prune at 8,192 bytes with a 4,096-byte head and a
// 1,024-byte tail; spill above 256 KiB. All are vars so evals can scale them.
package compaction

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// PruneHeadBytes is how much of an oversized tool result's beginning survives.
var PruneHeadBytes = 4096

// PruneTailBytes is how much of its end survives.
var PruneTailBytes = 1024

// PruneThresholdBytes is the size above which a tool result is pruned.
var PruneThresholdBytes = 8192

// SpillThresholdBytes is the size above which a tool result spills to disk.
var SpillThresholdBytes = 256 * 1024

// Pruned marks a pruned result: the content is head+…+tail with the counts.
func Prune(content string) string {
	b := []byte(content)
	if len(b) <= PruneThresholdBytes {
		return content
	}
	head := b[:PruneHeadBytes]
	tail := b[len(b)-PruneTailBytes:]
	omitted := len(b) - PruneHeadBytes - PruneTailBytes
	return fmt.Sprintf("%s\n\n[… %d bytes omitted — use read_file with a range if you need the middle …]\n\n%s",
		head, omitted, tail)
}

// NeedsPrune reports whether a result would be pruned — the caller can skip
// copies that are already small.
func NeedsPrune(content string) bool {
	return len(content) > PruneThresholdBytes
}

// SpillStore files oversized tool outputs on disk and hands back locators.
// The store owns one directory; locators are stable file names derived from
// the content hash, so the same body spilled twice is one file.
type SpillStore struct {
	dir string
	mu  sync.Mutex
}

// NewSpillStore builds a store rooted at dir, creating it if needed.
func NewSpillStore(dir string) (*SpillStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &SpillStore{dir: dir}, nil
}

// Spill writes body to disk when it exceeds the spill threshold and returns
// the replacement text: a locator line plus a summary head. Content under the
// threshold returns unchanged (ok=false). The body is verbatim on disk —
// what the model re-reads by range is exactly what the tool produced.
func (s *SpillStore) Spill(toolName, content string) (replacement string, spilled bool, err error) {
	if len(content) <= SpillThresholdBytes {
		return content, false, nil
	}
	sum := sha256.Sum256([]byte(content))
	name := hex.EncodeToString(sum[:16])
	path := filepath.Join(s.dir, name+".txt")

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(path); err == nil {
		// Same body already spilled: reuse the locator, do not rewrite.
	} else if writeErr := os.WriteFile(path, []byte(content), 0o600); writeErr != nil {
		return content, false, writeErr
	}
	head := content
	if len(head) > 512 {
		head = head[:512]
	}
	return fmt.Sprintf("[output too large for the transcript — %d bytes saved to %s]\n%s\n[… re-read with read_file on that path if you need more …]",
		len(content), path, head), true, nil
}

// LocatorPath returns the absolute path a locator names, or "" when the text
// carries no locator. The read tools resolve locators through this.
func (s *SpillStore) LocatorPath(text string) string {
	// Locators are absolute paths under the store's directory.
	if len(text) == 0 || s.dir == "" {
		return ""
	}
	marker := "saved to "
	idx := indexOf(text, marker)
	if idx < 0 {
		return ""
	}
	rest := text[idx+len(marker):]
	end := indexOf(rest, "]")
	if end < 0 {
		return ""
	}
	p := rest[:end]
	// Only paths inside the store resolve — a locator is not an arbitrary
	// file-read token.
	if filepath.Dir(p) != s.dir {
		return ""
	}
	return p
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
