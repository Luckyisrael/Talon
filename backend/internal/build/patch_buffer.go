package build

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Entry is one staged pre-image: the file's content before a try_edit
// touched it, plus the hash that identifies that content.
type Entry struct {
	Path     string
	PreImage []byte
	Hash     string
	Round    int
}

// MaxEntries caps the buffer per session. Past the cap the oldest entry is
// evicted FIFO with its path reported, so the model sees what undo history
// it just lost instead of silently losing it.
const MaxEntries = 50

// PatchBuffer is the content-hash-keyed pre-image store behind try_edit.
// It is pure memory: entries die with the session, which is exactly the
// blast radius a sandboxed-edit experiment wants.
type PatchBuffer struct {
	mu      sync.Mutex
	entries map[string]Entry
	order   []string
}

// NewPatchBuffer returns an empty buffer.
func NewPatchBuffer() *PatchBuffer {
	return &PatchBuffer{entries: make(map[string]Entry)}
}

// Stage records the pre-image for path, evicting the oldest entry past the
// cap. It returns the evicted path, or "" when nothing was evicted.
// Re-staging a path replaces its entry and refreshes its FIFO position.
func (b *PatchBuffer) Stage(path string, preImage []byte, round int) (evicted string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	sum := sha256.Sum256(preImage)
	if _, ok := b.entries[path]; !ok {
		b.order = append(b.order, path)
	} else {
		b.removeFromOrder(path)
		b.order = append(b.order, path)
	}
	b.entries[path] = Entry{Path: path, PreImage: append([]byte(nil), preImage...), Hash: hex.EncodeToString(sum[:]), Round: round}
	for len(b.order) > MaxEntries {
		oldest := b.order[0]
		b.order = b.order[1:]
		delete(b.entries, oldest)
		evicted = oldest
	}
	return evicted
}

// Accept drops the entry for path: the edit is kept, undo is gone.
func (b *PatchBuffer) Accept(path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.entries, path)
	b.removeFromOrder(path)
}

// PreImage returns the staged pre-image for path, or false when nothing is
// staged (never tried, or already accepted).
func (b *PatchBuffer) PreImage(path string) ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.entries[path]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), e.PreImage...), true
}

// Len reports the staged entry count.
func (b *PatchBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.entries)
}

func (b *PatchBuffer) removeFromOrder(path string) {
	for i, p := range b.order {
		if p == path {
			b.order = append(b.order[:i], b.order[i+1:]...)
			return
		}
	}
}

// Gradle perf flags every workspace build should carry. Applied by editing
// gradle.properties in place (not by shipping a template file) because
// android/ is generated per workspace by expo prebuild — a template file
// would never reach it. configuration-cache stays off: still incubating,
// breaks RN/Expo plugins.
var gradlePerfFlags = map[string]string{
	"org.gradle.daemon":            "true",
	"org.gradle.caching":           "true",
	"org.gradle.configureondemand": "true",
	"org.gradle.jvmargs":           "-Xmx4g -XX:MaxMetaspaceSize=512m -XX:+UseParallelGC -Dfile.encoding=UTF-8",
}

// EnsureGradlePerfFlags idempotently pins the perf flags in the
// gradle.properties at androidDir/gradle.properties, preserving every other
// line (including expo's). Missing file is not an error — the caller may run
// before prebuild; gradle just runs unflagged that once.
func EnsureGradlePerfFlags(androidDir string) error {
	path := filepath.Join(androidDir, "gradle.properties")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	lines := strings.Split(string(raw), "\n")
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if eq := strings.Index(t, "="); eq > 0 {
			if val, ok := gradlePerfFlags[strings.TrimSpace(t[:eq])]; ok {
				lines[i] = strings.TrimSpace(t[:eq]) + "=" + val
				seen[strings.TrimSpace(t[:eq])] = true
			}
		}
	}
	for key, val := range gradlePerfFlags {
		if !seen[key] {
			lines = append(lines, key+"="+val)
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}
