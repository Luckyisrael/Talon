package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	ErrPathTraversal = errors.New("path traversal not allowed")
	ErrAbsolutePath  = errors.New("absolute paths not allowed")
	ErrOutsideRoot   = errors.New("path escapes workspace root")
	ErrSymlinkEscape = errors.New("symlink escapes workspace root")
)

// ValidateSandboxPath resolves a caller-supplied relative path against root and
// guarantees the result stays inside it. Lexical checks alone are not enough:
// a symlink component can point anywhere, so the deepest existing ancestor is
// resolved through EvalSymlinks and re-checked for containment.
func ValidateSandboxPath(root, userPath string) (string, error) {
	clean := filepath.Clean(userPath)

	if filepath.IsAbs(clean) {
		return "", ErrAbsolutePath
	}
	sep := string(filepath.Separator)
	if clean == ".." || strings.HasPrefix(clean, ".."+sep) || strings.Contains(clean, sep+".."+sep) {
		return "", ErrPathTraversal
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("failed to resolve root: %w", err)
	}
	// The root itself may sit under a symlinked directory (common on macOS,
	// where /tmp is a link to /private/tmp). Resolve it once so the comparison
	// below is apples-to-apples.
	realRoot := absRoot
	if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
		realRoot = resolved
	}

	absFull := filepath.Join(absRoot, clean)

	// Resolve the longest existing prefix; the remaining tail cannot contain a
	// symlink because those components do not exist yet.
	existing := absFull
	var tail []string
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		tail = append([]string{filepath.Base(existing)}, tail...)
		existing = parent
	}

	resolvedExisting, err := filepath.EvalSymlinks(existing)
	if err != nil {
		// Nothing on this path exists yet; fall back to the lexical result,
		// which the traversal checks above already constrained.
		resolvedExisting = existing
	}

	final := resolvedExisting
	if len(tail) > 0 {
		final = filepath.Join(append([]string{resolvedExisting}, tail...)...)
	}

	if !within(final, realRoot) && !within(final, absRoot) {
		if resolvedExisting != existing {
			return "", ErrSymlinkEscape
		}
		return "", ErrOutsideRoot
	}

	return absFull, nil
}

func within(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// skippedDirs never contain project source and would swamp any walk.
var skippedDirs = map[string]bool{
	"node_modules": true,
	".expo":        true,
	".staging":     true,
	".git":         true,
	"upper":        true,
	"work":         true,
	"android":      true,
	"ios":          true,
	"dist":         true,
	"build":        true,
	".next":        true,
}

// ShouldSkipDir reports whether a directory should be excluded from walks.
func ShouldSkipDir(name string) bool { return skippedDirs[name] }

// FileSnapshot is one file's state before a turn modified it. Existed=false
// means the turn created the file, so undoing it means deleting the file.
type FileSnapshot struct {
	Path    string
	Content string
	Existed bool
}

// FS is the sandboxed file view every file tool shares. Tools write directly to
// the live workspace — the agent must be able to read back what it just wrote,
// and typecheck has to see real files to mean anything. Undo comes from the
// journal of pre-images rather than from a separate staging tree.
type FS struct {
	root    string
	journal map[string]FileSnapshot
	touched []string
	// maxFileBytes caps a single read so one minified bundle cannot consume
	// the whole context window.
	maxFileBytes int
	// mu guards journal and touched. The turn's file tools run on the tool
	// lanes while a mutating child's merge-back marks from the delegation
	// pool's goroutines — parallel tool calls mean those genuinely overlap
	// on one shared journal, and the race detector must see none of it.
	mu sync.Mutex
}

func NewFS(root string) *FS {
	return &FS{
		root:         root,
		journal:      make(map[string]FileSnapshot),
		maxFileBytes: 256 * 1024,
	}
}

func (f *FS) Root() string { return f.root }

// Resolve validates a relative path and returns its absolute form.
func (f *FS) Resolve(rel string) (string, error) {
	return ValidateSandboxPath(f.root, rel)
}

// record captures a file's current state the first time a turn touches it,
// reporting whether THIS call journaled the path (the first snapshot wins —
// later touches keep it). It is the single mutation point for the journal
// and touched set; callers must not hold f.mu.
func (f *FS) record(rel, abs string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := filepath.ToSlash(filepath.Clean(rel))
	if _, seen := f.journal[key]; seen {
		return false
	}
	snap := FileSnapshot{Path: key}
	if data, err := os.ReadFile(abs); err == nil {
		snap.Content = string(data)
		snap.Existed = true
	}
	f.journal[key] = snap
	f.touched = append(f.touched, key)
	return true
}

// MarkChanged registers paths that changed OUTSIDE the file tools — a
// mutating child's merge-back lands through git apply — into this turn's
// touched set. Call it BEFORE the external mutation applies so the
// snapshot is the pre-change state (undo then reverts it exactly like a
// file-tool write). It returns only the paths this call newly journaled:
// a path already touched this turn keeps its earlier, deeper pre-image,
// and its caller must not withdraw that one when a later change fails to
// land. Paths that do not resolve inside the root are skipped — never
// journaled.
func (f *FS) MarkChanged(rels ...string) []string {
	var newly []string
	for _, rel := range rels {
		key := filepath.ToSlash(filepath.Clean(rel))
		if key == "" || key == "." {
			continue
		}
		abs, err := f.Resolve(rel)
		if err != nil {
			continue
		}
		if f.record(rel, abs) {
			newly = append(newly, key)
		}
	}
	return newly
}

// UnmarkChanged withdraws paths from the turn: a staged change that never
// landed did not happen, and files_touched must not claim otherwise. Both
// the touched entry and its pre-image go, so a later real write to the
// same path journals afresh.
func (f *FS) UnmarkChanged(rels ...string) {
	if len(rels) == 0 {
		return
	}
	drop := make(map[string]bool, len(rels))
	for _, rel := range rels {
		drop[filepath.ToSlash(filepath.Clean(rel))] = true
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.touched[:0]
	for _, p := range f.touched {
		if drop[p] {
			delete(f.journal, p)
			continue
		}
		kept = append(kept, p)
	}
	f.touched = kept
}

// Read returns a file's contents, capped at maxFileBytes.
func (f *FS) Read(rel string) (string, bool, error) {
	abs, err := f.Resolve(rel)
	if err != nil {
		return "", false, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", false, err
	}
	if info.IsDir() {
		return "", false, fmt.Errorf("%s is a directory", rel)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", false, err
	}
	if len(data) > f.maxFileBytes {
		return string(data[:f.maxFileBytes]), true, nil
	}
	return string(data), false, nil
}

// Write replaces a file's contents, journalling the previous state.
func (f *FS) Write(rel, content string) error {
	abs, err := f.Resolve(rel)
	if err != nil {
		return err
	}
	f.record(rel, abs)
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return err
	}
	// Write to a sibling temp file and rename so a crash mid-write cannot
	// leave a half-written source file behind.
	tmp := abs + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, abs)
}

// Delete removes a file, journalling its previous contents.
func (f *FS) Delete(rel string) error {
	abs, err := f.Resolve(rel)
	if err != nil {
		return err
	}
	f.record(rel, abs)
	if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Exists reports whether a path resolves to an existing file.
func (f *FS) Exists(rel string) bool {
	abs, err := f.Resolve(rel)
	if err != nil {
		return false
	}
	info, err := os.Stat(abs)
	return err == nil && !info.IsDir()
}

// Journal returns the pre-images of every file this turn changed, in the order
// they were first touched.
func (f *FS) Journal() []FileSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]FileSnapshot, 0, len(f.touched))
	for _, p := range f.touched {
		out = append(out, f.journal[p])
	}
	return out
}

// Touched returns the relative paths changed during this turn.
func (f *FS) Touched() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.touched))
	copy(out, f.touched)
	return out
}

// ResetJournal starts a new turn's change tracking.
func (f *FS) ResetJournal() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.journal = make(map[string]FileSnapshot)
	f.touched = nil
}

// Restore rolls the workspace back to a set of snapshots.
func (f *FS) Restore(snaps []FileSnapshot) error {
	var firstErr error
	// Reverse order so a file created inside a directory is removed before any
	// attempt to restore its parent.
	for i := len(snaps) - 1; i >= 0; i-- {
		s := snaps[i]
		abs, err := f.Resolve(s.Path)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !s.Existed {
			if err := os.Remove(abs); err != nil && !os.IsNotExist(err) && firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil && firstErr == nil {
			firstErr = err
			continue
		}
		if err := os.WriteFile(abs, []byte(s.Content), 0644); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
