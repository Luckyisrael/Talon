package skills

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/luckyisrael/talon/backend/internal/knowledge"
)

// Workspace pins live beside the workspace skills they resemble:
// <root>/.talon/plugins.json holds {"plugins": {"name": "version"}}.
// A missing file is normal (no plugins pinned), a malformed one fails the
// workspace loudly rather than resolving half a pin set.
func ReadWorkspacePins(root string) (map[string]string, error) {
	raw, err := os.ReadFile(filepath.Join(root, ".talon", "plugins.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	var doc struct {
		Plugins map[string]string `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("plugins: bad .talon/plugins.json: %w", err)
	}
	out := map[string]string{}
	for name, version := range doc.Plugins {
		if name == "" || version == "" {
			return nil, fmt.Errorf("plugins: bad .talon/plugins.json: empty name or version")
		}
		out[name] = version
	}
	return out, nil
}

// Snapshot is one workspace's plugin skills layered under a base catalog:
// the base (embedded, env-pinned) always wins name clashes, so workspace
// content can never shadow built-in guidance.
type Snapshot struct {
	base  *Catalog
	extra []knowledge.Skill
}

// SnapshotForRoot resolves a workspace's pins through reg and returns the
// overlay view. Resolution errors degrade to an empty overlay — the base
// catalog is never at risk from a plugin fetch.
func (c *Catalog) SnapshotForRoot(root string, reg RegistryProvider) *Snapshot {
	extra, _ := resolveWorkspace(root, reg)
	return &Snapshot{base: c, extra: extra}
}

// resolveWorkspace reads pins and resolves them, reporting whether the
// resolution itself worked. A workspace with no pins resolves empty and ok;
// a fetch failure resolves empty and not-ok, which is how callers tell "no
// plugins" from "plugins unreachable, keep stale".
func resolveWorkspace(root string, reg RegistryProvider) (extra []knowledge.Skill, ok bool) {
	pins, err := ReadWorkspacePins(root)
	if err != nil {
		log.Printf("plugins: %v", err)
		return nil, false
	}
	if len(pins) == 0 {
		return nil, true
	}
	extra, err = reg.LoadForPins(pins)
	if err != nil {
		log.Printf("plugins: workspace resolve failed: %v", err)
		return nil, false
	}
	return extra, true
}

// Pins returns the workspace pin set behind this snapshot, for approval
// recording and update checks.
func (s *Snapshot) Pins(root string) map[string]string {
	pins, err := ReadWorkspacePins(root)
	if err != nil {
		return map[string]string{}
	}
	return pins
}

// SkillBody adapts the snapshot to the read_skill library interface:
// base first, then the workspace overlay. It drops the source annotation;
// callers that need provenance use Body.
func (s *Snapshot) SkillBody(name string) (string, bool) {
	body, _, ok := s.Body(name)
	return body, ok
}

// HasExtra reports whether the workspace overlay contributed skills.
func (s *Snapshot) HasExtra() bool { return len(s.extra) > 0 }

// SkillNames adapts the merged names to the read_skill library interface.
func (s *Snapshot) SkillNames() []string { return s.Names() }

// Manifest renders base plus workspace lines, each annotated with its
// plugin so the model can tell built-in guidance from fetched guidance.
func (s *Snapshot) Manifest() string {
	base := ""
	if s.base != nil {
		base = s.base.Manifest()
	}
	if len(s.extra) == 0 {
		return base
	}
	var b strings.Builder
	if base != "" {
		b.WriteString(base)
		b.WriteString("\n\n")
	}
	b.WriteString("## Workspace plugins\n\nFetched guidance for this project. Load one skill at a time with `read_skill`, " +
		"only when its description matches the current job — never preload several. " +
		"Each skill wins exactly one job (connect vs balance vs send, never two).\n\n")
	seen := map[string]bool{}
	if s.base != nil {
		for _, n := range s.base.Names() {
			seen[n] = true
		}
	}
	names := make([]string, 0, len(s.extra))
	for _, sk := range s.extra {
		if seen[sk.Name] {
			continue
		}
		seen[sk.Name] = true
		names = append(names, sk.Name)
	}
	sort.Strings(names)
	byName := map[string]knowledge.Skill{}
	for _, sk := range s.extra {
		byName[sk.Name] = sk
	}
	for _, n := range names {
		fmt.Fprintf(&b, "- **%s** — %s *(plugin)*\n", n, byName[n].Description)
	}
	return strings.TrimSpace(b.String())
}

// Body resolves through the base first, then the workspace overlay.
func (s *Snapshot) Body(name string) (string, string, bool) {
	if s.base != nil {
		if body, src, ok := s.base.Body(name); ok {
			return body, src, true
		}
	}
	for _, sk := range s.extra {
		if sk.Name == name {
			return sk.Body, "plugin:" + name, true
		}
	}
	return "", "", false
}

// Names merges both layers, base first and deduplicated.
func (s *Snapshot) Names() []string {
	seen := map[string]bool{}
	var out []string
	if s.base != nil {
		for _, n := range s.base.Names() {
			seen[n] = true
			out = append(out, n)
		}
	}
	var extra []string
	for _, sk := range s.extra {
		if !seen[sk.Name] {
			seen[sk.Name] = true
			extra = append(extra, sk.Name)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

// SnapshotCache memoizes per-workspace snapshots so manifest renders (which
// run to fingerprint every enrichment) never hit the network. Entries live
// for ttl; expiry refetches on next use, failure serves the stale entry.
type SnapshotCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	reg     RegistryProvider
	entries map[string]*snapshotEntry
}

type snapshotEntry struct {
	at   time.Time
	snap *Snapshot
}

// NewSnapshotCache builds the cache. A zero ttl disables expiry (every call
// resolves fresh) — useful in tests, wasteful in production.
func NewSnapshotCache(reg RegistryProvider, ttl time.Duration) *SnapshotCache {
	return &SnapshotCache{reg: reg, ttl: ttl, entries: map[string]*snapshotEntry{}}
}

// ForRoot returns the workspace snapshot, refreshing it when stale. A
// failed refresh serves the previous snapshot rather than an empty one.
func (c *SnapshotCache) ForRoot(base *Catalog, root string) *Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[root]
	if ok && (c.ttl <= 0 || time.Since(e.at) < c.ttl) {
		return e.snap
	}
	extra, resolveOK := resolveWorkspace(root, c.reg)
	if !resolveOK && ok {
		return e.snap
	}
	snap := &Snapshot{base: base, extra: extra}
	c.entries[root] = &snapshotEntry{at: time.Now(), snap: snap}
	return snap
}
