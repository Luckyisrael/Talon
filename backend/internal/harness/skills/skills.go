// Package skills is the H6 multi-source skills registry: embedded knowledge
// (today's go:embed content) plus per-project filesystem skills, merged into
// one catalog with a deterministic winner per name.
//
// Trust boundary: filesystem skills are authored in the workspace — anyone
// with write access to the project (the user, and the agent itself through
// its file tools) can add one. They are therefore listed in the catalog (the
// manifest carries name + description only), but their bodies reach the
// model only through an explicit read_skill — the same boundary the embedded
// skills already have — and they never contribute alwaysApply rules.
package skills

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/luckyisrael/talon/backend/internal/knowledge"
)

// Provider is one source of skills. Load returns the source's skills; an
// error is the source refusing (unreadable directory), not fatal to the
// merged catalog.
type Provider interface {
	// Name identifies the source in diagnostics ("embedded", "workspace").
	Name() string
	// Load reads the source now. Errors degrade: the provider contributes
	// nothing this pass, logged by the catalog.
	Load() ([]knowledge.Skill, error)
}

// EmbeddedProvider serves the binary's embedded skills through the existing
// knowledge.Registry — the authoritative source; it wins every name clash.
type EmbeddedProvider struct {
	// Registry is the loaded embedded knowledge.
	Registry *knowledge.Registry
}

func (p EmbeddedProvider) Name() string { return "embedded" }

func (p EmbeddedProvider) Load() ([]knowledge.Skill, error) {
	if p.Registry == nil {
		return nil, fmt.Errorf("no embedded registry")
	}
	names := p.Registry.SkillNames()
	out := make([]knowledge.Skill, 0, len(names))
	for _, n := range names {
		body, ok := p.Registry.SkillBody(n)
		if !ok {
			continue
		}
		out = append(out, knowledge.Skill{Name: n, Body: body})
	}
	return out, nil
}

// FSProvider reads workspace-authored skills:
// <root>/.talon/skills/<name>/SKILL.md with the same front-matter format as
// embedded skills. A workspace without the directory contributes nothing —
// absence is normal, not an error.
type FSProvider struct {
	// Root is the project workspace.
	Root string
}

func (p FSProvider) Name() string { return "workspace" }

func (p FSProvider) Load() ([]knowledge.Skill, error) {
	dir := filepath.Join(p.Root, ".talon", "skills")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []knowledge.Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillPath := filepath.Join(dir, e.Name(), "SKILL.md")
		raw, err := os.ReadFile(skillPath)
		if err != nil {
			// One malformed skill must not sink the catalog: skip, and let
			// the catalog's diagnostic surface the gap.
			continue
		}
		s, err := parseWorkspaceSkill(string(raw))
		if err != nil {
			continue
		}
		if s.Name != e.Name() {
			// The directory is the identity, like embedded skills.
			continue
		}
		out = append(out, *s)
	}
	return out, nil
}

// parseWorkspaceSkill reuses the embedded format's front-matter parser.
func parseWorkspaceSkill(raw string) (*knowledge.Skill, error) {
	scalars, lists, body, ok := knowledge.ParseFrontMatter(raw)
	if !ok {
		return nil, fmt.Errorf("missing front matter")
	}
	name := scalars["name"]
	if name == "" || scalars["description"] == "" || strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("skill %q lacks name/description/body", name)
	}
	return &knowledge.Skill{
		Name:        name,
		Description: scalars["description"],
		Version:     scalars["version"],
		AutoAttach:  scalars["autoAttach"] == "true",
		Triggers:    lists["triggers"],
		Body:        strings.TrimSpace(body),
	}, nil
}

// Catalog is the merged, multi-source skill set with a deterministic winner
// per name: embedded beats workspace (binary knowledge stays authoritative),
// and within a source later loads win (they are fresher reads).
type Catalog struct {
	mu        sync.RWMutex
	providers []Provider
	// merged is name → winning skill.
	merged map[string]knowledge.Skill
	// sources records which provider contributed each name — the honest
	// surface for diagnostics ("this convention comes from your workspace").
	sources map[string]string
	loaded  time.Time
}

// NewCatalog builds a catalog over providers, in priority order (index 0
// wins name clashes).
func NewCatalog(providers ...Provider) *Catalog {
	return &Catalog{providers: providers, merged: map[string]knowledge.Skill{}, sources: map[string]string{}}
}

// Reload re-reads every provider and rebuilds the winner map. Called at
// composition start and on workspace-touch reconciliation (the same
// discipline as the instructions chain: no watcher, refresh on touch).
func (c *Catalog) Reload() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	merged := map[string]knowledge.Skill{}
	sources := map[string]string{}
	for _, p := range c.providers {
		list, err := p.Load()
		if err != nil {
			// Degrade: this source contributes nothing; keep going.
			continue
		}
		for _, s := range list {
			if s.Name == "" {
				continue
			}
			if _, taken := merged[s.Name]; taken {
				// Priority order: earlier providers win.
				continue
			}
			merged[s.Name] = s
			sources[s.Name] = p.Name()
		}
	}
	c.merged = merged
	c.sources = sources
	c.loaded = time.Now()
	return nil
}

// Names returns the merged skill names, sorted.
func (c *Catalog) Names() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	names := make([]string, 0, len(c.merged))
	for n := range c.merged {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Body returns the skill body and its source, or ok=false.
func (c *Catalog) Body(name string) (body string, source string, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, found := c.merged[strings.TrimSpace(name)]
	if !found {
		return "", "", false
	}
	return s.Body, c.sources[s.Name], true
}

// Manifest renders the catalog listing: name — description, one per line,
// sources annotated when a skill is workspace-authored.
func (c *Catalog) Manifest() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.merged) == 0 {
		return ""
	}
	names := make([]string, 0, len(c.merged))
	for n := range c.merged {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("## Skills available\n\n")
	b.WriteString("Detailed, version-pinned how-tos for this project's stack. Load one skill at a time " +
		"with `read_skill` only when its description matches the current job; never preload several " +
		"\"just in case\". Some attach automatically when relevant.\n\n")
	for _, n := range names {
		s := c.merged[n]
		line := fmt.Sprintf("- **%s** — %s", s.Name, s.Description)
		if src := c.sources[n]; src != "" && src != "embedded" {
			line += fmt.Sprintf(" *(%s)*", src)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// EnsurePathPrefix is the fs helper workspace providers need: it reports
// whether p stays inside root, rejecting parent escapes.
func EnsurePathPrefix(root, p string) bool {
	rp, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rp != ".." && !strings.HasPrefix(rp, ".."+string(filepath.Separator))
}

// ListDir is a fs helper that keeps the package a leaf: it wraps fs.ReadDir
// for providers that want to enumerate without importing io/fs themselves.
func ListDir(dir string) ([]fs.DirEntry, error) { return os.ReadDir(dir) }

// JoinPath joins path elements for providers.
func JoinPath(parts ...string) string { return path.Join(parts...) }
