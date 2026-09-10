// Package settings is the H12 hot-reloadable model settings document: which
// adapters exist is composition (code); which providers run is settings
// (data), and data changes without a restart.
//
// The document (JSON, watched by mtime):
//
//	{
//	  "routing": {
//	    "fallbackModel": "…",
//	    "shortPromptModel": "…",
//	    "tiers": { "cheap": "…", "medium": "…", "heavy": "…" }
//	  },
//	  "pricing": { "model-id": {"in": 0.0, "out": 0.0} }
//	}
//
// The discipline: reads consult the document each time (cheap: a stat and
// maybe a read), writes update it, and a change takes effect on the next
// turn — never mid-turn. ROUTING_POLICY_JSON semantics are preserved: an
// existing env-provided policy remains the seed; the document overrides it
// once present.
package settings

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Routing is the model-selection policy.
type Routing struct {
	// FallbackModel answers a failed primary request, once.
	FallbackModel string `json:"fallbackModel,omitempty"`
	// ShortPromptModel routes short follow-up prompts to a cheaper tier.
	ShortPromptModel string `json:"shortPromptModel,omitempty"`
	// Tiers maps tier names to model ids.
	Tiers map[string]string `json:"tiers,omitempty"`
}

// Price is one model's per-million-token pricing.
type Price struct {
	In  float64 `json:"in"`
	Out float64 `json:"out"`
}

// Document is the settings file's shape.
type Document struct {
	Routing Routing          `json:"routing"`
	Pricing map[string]Price `json:"pricing,omitempty"`
}

// Doc is the live settings holder: reads are lock-guarded snapshots, the
// file is re-read when its mtime moves (the reconciliation-on-touch
// discipline — no watcher, same as the instructions chain).
type Doc struct {
	mu   sync.RWMutex
	path string
	// seed is the boot-time document (from env or defaults).
	seed Document
	// live is the file's document when one exists and parses.
	live    Document
	hasLive bool
	// stamp is the file's last-seen mtime; zero means never read.
	stamp time.Time
	// badLive reports the file exists but did not parse — the honest
	// degradation: keep the last good document, surface the failure.
	badLive bool
}

// NewDoc builds the holder. seed is the boot policy (ROUTING_POLICY_JSON's
// parsed form or the defaults); path, when non-empty, is the watched file.
func NewDoc(seed Document, path string) *Doc {
	return &Doc{seed: seed, path: path}
}

// Snapshot returns the current effective document. The file is consulted
// first (a stat; a read only when its mtime moved); when no file exists or
// it never parsed, the seed serves.
func (d *Doc) Snapshot() Document {
	if d == nil {
		return Document{}
	}
	// Fast path: a file that has not moved serves the cached live copy.
	d.mu.RLock()
	path, stamp, hasLive, badLive := d.path, d.stamp, d.hasLive, d.badLive
	d.mu.RUnlock()

	if path == "" {
		return d.seedSnapshot()
	}
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		// No file: the seed is the whole truth.
		return d.seedSnapshot()
	}
	if !fi.ModTime().After(stamp) && hasLive && !badLive {
		d.mu.RLock()
		live := cloneDoc(d.live)
		d.mu.RUnlock()
		return live
	}
	// The file moved (or was never read): re-read and parse.
	raw, err := os.ReadFile(path)
	if err != nil {
		return d.seedSnapshot()
	}
	var doc Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		// Honest degradation: keep the last good copy, flag the failure —
		// a malformed settings file must not silently revert routing.
		d.mu.Lock()
		d.badLive = true
		d.stamp = fi.ModTime()
		d.mu.Unlock()
		if hasLive {
			d.mu.RLock()
			live := cloneDoc(d.live)
			d.mu.RUnlock()
			return live
		}
		return d.seedSnapshot()
	}
	d.mu.Lock()
	d.live = doc
	d.hasLive = true
	d.badLive = false
	d.stamp = fi.ModTime()
	d.mu.Unlock()
	return cloneDoc(doc)
}

func (d *Doc) seedSnapshot() Document {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return cloneDoc(d.seed)
}

// BadFile reports whether the watched file exists but does not parse — the
// operator surface for "your settings are broken; routing is held at the
// last good copy".
func (d *Doc) BadFile() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.badLive
}

// Path reports the watched file's path ("" when seeded from env only).
func (d *Doc) Path() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.path
}

func cloneDoc(d Document) Document {
	out := d
	out.Routing.Tiers = cloneMap(d.Routing.Tiers)
	out.Pricing = clonePricing(d.Pricing)
	return out
}

func cloneMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func clonePricing(m map[string]Price) map[string]Price {
	if m == nil {
		return nil
	}
	out := make(map[string]Price, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Write persists a new document to the watched path (creating it) so the
// operator's change becomes data. The next Snapshot consults it.
func (d *Doc) Write(doc Document) error {
	d.mu.RLock()
	path := d.path
	d.mu.RUnlock()
	if path == "" {
		return os.ErrInvalid
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return err
	}
	// Refresh the live copy now so a read following a write sees it without
	// waiting for the mtime check (WriteFile may preserve mtimes within
	// filesystem timestamp granularity).
	d.mu.Lock()
	d.live = doc
	d.hasLive = true
	d.badLive = false
	if fi, err := os.Stat(path); err == nil {
		d.stamp = fi.ModTime()
	}
	d.mu.Unlock()
	return nil
}
