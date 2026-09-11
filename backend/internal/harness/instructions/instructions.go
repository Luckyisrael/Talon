// Package instructions is the H6 AGENTS.md chain: the files that teach the
// agent a project's own conventions, from broadest to most specific, under a
// byte budget.
//
// Sources, in precedence order (most specific wins on conflict, broader
// files are omitted first under pressure):
//
//	<workspace>/AGENTS.md        — the project's conventions
//	<workspace>/app/AGENTS.md …  — nested per-area conventions
//	$TALON_HOME/AGENTS.md      — the user's global conventions
//
// The budget law (from DSH): 65,536 bytes total. When the chain overflows,
// broader files are omitted whole before the most specific file is
// truncated — the most specific instructions are the ones the current work
// actually depends on. Truncation is honest: the cut carries a marker naming
// what was cut.
//
// Refresh discipline: no filesystem watcher. The chain is re-read on the
// next filesystem touch (the same reconciliation-on-touch law DSH uses), so
// an edit is visible at the next round boundary without a background
// watcher's race window.
package instructions

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// MaxChainBytes is the total budget for the assembled chain.
const MaxChainBytes = 65536

// Source is one discovered AGENTS.md file.
type Source struct {
	// Path is the file's absolute path.
	Path string
	// Rel is the workspace-relative path ("" for the root file), used for
	// headers and specificity ordering.
	Rel string
	// Body is the file's raw content.
	Body string
}

// Chain is the compiled instruction set for one workspace.
type Chain struct {
	mu      sync.RWMutex
	sources []Source
	assembled string
	loaded  time.Time
}

// Loader reads one workspace's AGENTS.md chain. The global file is optional;
// the workspace tree is walked once per Load.
type Loader struct {
	// WorkspaceRoot is the project directory.
	WorkspaceRoot string
	// GlobalPath is the user-global AGENTS.md ($TALON_HOME/AGENTS.md);
	// empty disables it.
	GlobalPath string
}

// Load reads the chain now. Missing files are skipped silently — an empty
// chain is a normal state, not an error. Errors that mean "unreadable when
// present" degrade the same way: the file is skipped, the chain stays honest
// about what it could read.
func (l Loader) Load() *Chain {
	c := &Chain{}
	var found []Source

	// Global first (broadest): it is omitted first under budget pressure.
	if l.GlobalPath != "" {
		if body, err := os.ReadFile(l.GlobalPath); err == nil {
			found = append(found, Source{Path: l.GlobalPath, Rel: "global", Body: string(body)})
		}
	}

	// The workspace tree: AGENTS.md at the root and nested one level per
	// area (app/, components/, features/, …). Deep nesting beyond two levels
	// is rare and the budget would omit it anyway.
	wsRoot := l.WorkspaceRoot
	if body, err := os.ReadFile(filepath.Join(wsRoot, "AGENTS.md")); err == nil {
		found = append(found, Source{Path: filepath.Join(wsRoot, "AGENTS.md"), Rel: "", Body: string(body)})
	}
	areas := listAreaDirs(wsRoot)
	for _, area := range areas {
		p := filepath.Join(wsRoot, area, "AGENTS.md")
		body, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		found = append(found, Source{Path: p, Rel: area, Body: string(body)})
	}

	c.mu.Lock()
	c.sources = found
	c.assembled = assemble(found)
	c.loaded = time.Now()
	c.mu.Unlock()
	return c
}

// listAreaDirs finds immediate subdirectories that carry an AGENTS.md.
func listAreaDirs(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, e.Name(), "AGENTS.md")); err == nil {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// assemble fits sources under the budget: broader files omitted whole first,
// most-specific truncated last with an honest marker.
func assemble(sources []Source) string {
	// Order broad → specific: global, root, areas alphabetical.
	// (Load already appends in that order; keep it stable here regardless.)
	ordered := append([]Source(nil), sources...)

	total := 0
	for _, s := range ordered {
		total += len(s.Body)
	}
	if total <= MaxChainBytes {
		return render(ordered, -1, 0)
	}

	// Over budget: omit from the broad end until the rest fits. The most
	// specific file (the last one) is never omitted — only truncated.
	budget := MaxChainBytes
	keepFrom := 0
	for i := 0; i < len(ordered)-1; i++ {
		if budget-len(ordered[i].Body) < 0 {
			keepFrom = i
			goto fit
		}
		budget -= len(ordered[i].Body)
	}
	keepFrom = len(ordered) - 1
fit:
	kept := ordered[keepFrom:]
	// Truncate the last (most specific) if still over.
	over := 0
	for _, s := range kept {
		over += len(s.Body)
	}
	cutAt := -1
	if over > budget {
		cutAt = budget
	}
	return render(kept, cutAt, len(ordered)-len(kept))
}

// render formats the surviving sources. cutAt >= 0 truncates the final
// source's body at that many bytes with the honest marker.
func render(sources []Source, cutAt, omitted int) string {
	var b strings.Builder
	if omitted > 0 {
		b.WriteString("[project conventions: ")
		b.WriteString(itoa(omitted))
		b.WriteString(" broader instruction file(s) omitted to fit the context budget]\n\n")
	}
	for i, s := range sources {
		if s.Rel == "global" {
			b.WriteString("# Global conventions (user)\n\n")
		} else if s.Rel == "" {
			b.WriteString("# Project conventions (AGENTS.md)\n\n")
		} else {
			b.WriteString("# ")
			b.WriteString(s.Rel)
			b.WriteString(" conventions\n\n")
		}
		body := s.Body
		if i == len(sources)-1 && cutAt >= 0 && len(body) > cutAt {
			b.WriteString(body[:cutAt])
			b.WriteString("\n\n[truncated at the context budget — ")
			b.WriteString(itoa(len(body) - cutAt))
			b.WriteString(" bytes of the most specific conventions omitted]\n\n")
		} else {
			b.WriteString(body)
			b.WriteString("\n\n")
		}
	}
	return strings.TrimSpace(b.String())
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

// Text returns the assembled chain — "" when there is nothing.
func (c *Chain) Text() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.assembled
}

// Sources reports what the chain was built from.
func (c *Chain) Sources() []Source {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Source(nil), c.sources...)
}

// LoadedAt reports the last Load time — the reconciliation-on-touch clock.
func (c *Chain) LoadedAt() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.loaded
}
