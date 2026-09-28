// Package promptsec is the H7 system-prompt section registry: the prompt is
// an ordered list of named sections, each independently fingerprinted, so
// the cache discipline that already exists for projectMap generalizes to
// every section.
//
// The law: a section is (name, render). The registry holds them in a stable
// total order. Assembly is concatenation with byte-stable separators. A
// section whose render changes shifts its fingerprint; the composition can
// then decide what to re-send and what the cache still covers. Sections with
// empty renders are omitted — an empty section contributes no separator, so
// absence is byte-identical to never having registered it.
package promptsec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Section is one named, rendered slice of the system prompt.
type Section struct {
	// Name identifies the section across renders ("persona", "rules/core").
	Name string
	// Render is the section's current text; "" omits the section.
	Render func() string
	// Weight orders the section: lower renders earlier. Weights are the
	// stable total order — names break ties.
	Weight int
}

// Registry is the ordered section set. Safe for concurrent use.
type Registry struct {
	mu       sync.RWMutex
	sections map[string]Section
}

// New builds an empty registry.
func New() *Registry {
	return &Registry{sections: map[string]Section{}}
}

// Standard weights: the canonical prompt order. Composition may insert custom
// sections between them; the weights keep the ordering total.
//
// FM-C1c (RESEARCH P1.5): the session-stable sections — persona through
// skills — must sort BEFORE the volatile block (project map → checklist).
// A file write refreshes the map and a status flip re-renders the
// checklist: if the stable sections sat behind those, every such mutation
// would invalidate the instructions and skills bytes too, which is the
// whole cache lost for the sake of a tail.
const (
	WeightPersona      = 100
	WeightRules        = 200
	WeightDeviceFacts  = 300
	WeightDiscipline   = 400
	WeightInstructions = 450
	WeightSkills       = 460
	WeightProjectMap   = 500
	WeightMemory       = 600
	WeightADRs         = 700
	WeightFailures     = 800
	WeightPlanCheck    = 900
)

// Register adds or replaces a section by name. Replacing keeps the latest
// weight (the caller's), so a composition can reorder by re-registering.
func (r *Registry) Register(s Section) {
	if s.Name == "" || s.Render == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sections[s.Name] = s
}

// Suppress removes a section by name — per-preset shaping (an "expert"
// preset drops spelled-out steps). Suppressing an absent name is a no-op.
func (r *Registry) Suppress(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sections, name)
}

// ordered returns the sections in total order: weight, then name.
func (r *Registry) ordered() []Section {
	out := make([]Section, 0, len(r.sections))
	for _, s := range r.sections {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Weight != out[j].Weight {
			return out[i].Weight < out[j].Weight
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Assemble renders the full prompt: sections joined by a blank line. Empty
// renders are skipped, so assembly is byte-stable regardless of which
// sections are present.
func (r *Registry) Assemble() string {
	r.mu.RLock()
	sections := r.ordered()
	r.mu.RUnlock()
	var parts []string
	for _, s := range sections {
		text := strings.TrimSpace(s.Render())
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n\n")
}

// Fingerprint is one section's content hash — the cache-decision unit.
type Fingerprint struct {
	Name    string
	Hash    string
	Present bool
}

// Fingerprints renders every registered section and returns its fingerprint
// set, in registry order. The caller compares against the previous set to
// learn which sections moved.
func (r *Registry) Fingerprints() []Fingerprint {
	r.mu.RLock()
	sections := r.ordered()
	renders := make([]string, len(sections))
	r.mu.RUnlock()
	for i, s := range sections {
		text := strings.TrimSpace(s.Render())
		if text != "" {
			renders[i] = text
		}
	}
	out := make([]Fingerprint, len(sections))
	for i, s := range sections {
		out[i] = Fingerprint{Name: s.Name, Present: renders[i] != ""}
		if renders[i] == "" {
			out[i].Hash = ""
			continue
		}
		sum := sha256.Sum256([]byte(renders[i]))
		out[i].Hash = hex.EncodeToString(sum[:8])
	}
	return out
}

// SectionSize is one section's rendered byte contribution — the
// per-layer attribution the prompt-sec log prints (L13, FM-TELa).
type SectionSize struct {
	Name  string
	Bytes int
}

// Sizes renders every registered section exactly as Assemble will send it
// and reports its byte length. Empty renders are omitted the same way
// Assemble omits them, so sizes plus (n-1) blank-line separators
// reconcile with the assembled prompt — a section table that cannot be
// summed is the "? row" of prompt telemetry.
func (r *Registry) Sizes() []SectionSize {
	r.mu.RLock()
	sections := r.ordered()
	r.mu.RUnlock()
	out := make([]SectionSize, 0, len(sections))
	for _, s := range sections {
		text := strings.TrimSpace(s.Render())
		if text == "" {
			continue
		}
		out = append(out, SectionSize{Name: s.Name, Bytes: len(text)})
	}
	return out
}

// Diff reports which section names changed between two fingerprint sets —
// added, removed, or re-hashed. The prompt-cache discipline consumes this:
// only a change at the head invalidates the cached prefix, so knowing which
// section moved tells the loop how much re-send is needed.
func Diff(prev, next []Fingerprint) []string {
	pm := map[string]Fingerprint{}
	for _, f := range prev {
		pm[f.Name] = f
	}
	nm := map[string]Fingerprint{}
	for _, f := range next {
		nm[f.Name] = f
	}
	var changed []string
	for n, f := range nm {
		p, ok := pm[n]
		if !ok || p.Hash != f.Hash || p.Present != f.Present {
			changed = append(changed, n)
		}
	}
	for n := range pm {
		if _, ok := nm[n]; !ok {
			changed = append(changed, n)
		}
	}
	sort.Strings(changed)
	return changed
}

// HeadChanged reports whether any of the changed sections sit at or before
// the named head section in registry order — i.e. the cached prefix broke
// and the whole prompt must re-send. A change strictly after the head leaves
// the prefix intact.
func (r *Registry) HeadChanged(head string, changed []string) bool {
	if len(changed) == 0 {
		return false
	}
	r.mu.RLock()
	sections := r.ordered()
	r.mu.RUnlock()
	headIdx := -1
	for i, s := range sections {
		if s.Name == head {
			headIdx = i
			break
		}
	}
	if headIdx < 0 {
		// Unknown head: be safe, re-send.
		return true
	}
	names := map[string]bool{}
	for _, n := range changed {
		names[n] = true
	}
	for i := 0; i <= headIdx; i++ {
		if names[sections[i].Name] {
			return true
		}
	}
	return false
}

// Describe renders the registry's shape for diagnostics: names in order with
// their weights — the answer to "why is my prompt built like this".
func (r *Registry) Describe() string {
	r.mu.RLock()
	sections := r.ordered()
	r.mu.RUnlock()
	var b strings.Builder
	for _, s := range sections {
		fmt.Fprintf(&b, "%d %s\n", s.Weight, s.Name)
	}
	return strings.TrimRight(b.String(), "\n")
}
