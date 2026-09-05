package build

import (
	"crypto/sha256"
	"encoding/hex"
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
