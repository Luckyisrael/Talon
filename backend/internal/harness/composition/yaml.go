// The YAML composer (H12/W14.2): talon.composition.yaml is read at boot,
// resolved through the overlay layers, and applied by internal/composer —
// main.go stops wiring structs and starts reading data. This file is the
// loader half: parse, validate, and resolve. It imports no wiring packages
// on purpose — composition stays data-pure (harness/composition is the
// document, internal/composer is where rows meet live code).
package composition

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Row is one composition entry: id, component, config. The overlay rule is
// last-write-wins per id with WHOLE-row replacement — a later layer's row
// for the same id replaces the earlier one completely, config map and all;
// configs never merge (a half-merged config is a wiring state no one wrote).
type Row struct {
	ID        string         `yaml:"id" json:"id"`
	Component string         `yaml:"component" json:"component"`
	Config    map[string]any `yaml:"config,omitempty" json:"config,omitempty"`
}

// document is the file shape: one document holding the three overlay
// layers. Resolution order: base → profiles[profile] → presets[preset].
type document struct {
	Base     []Row            `yaml:"base"`
	Profiles map[string][]Row `yaml:"profiles"`
	Presets  map[string][]Row `yaml:"presets"`
}

// Composition is a resolved document: the final rows in first-appearance
// order (an overriding row keeps its id's original position — the dump and
// the apply both walk this order, so they are deterministic and identical).
// The rows are the composition's own values; treat them as read-only.
type Composition struct {
	// Path is the file as loaded (error messages carry it whole).
	Path string
	// Profile and Preset name the layers this document was resolved
	// through — the dump prints them so a diff shows WHICH overlay ran.
	Profile string
	Preset  string

	rows  map[string]Row
	order []string
}

// Load resolves the composition document at path through base →
// profiles[profile] → presets[preset], last-write-wins per id, whole-row
// replacement.
//
// A missing file is (nil, nil): a tree without a composition boots dark,
// byte-identically to before this file existed — composition is opt-in
// wiring, not a new mandatory contract. A malformed file, a row without an
// id, or a row without a component is an error: wiring you cannot trust
// must not boot.
func Load(path, profile, preset string) (*Composition, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("composition: read %s: %w", path, err)
	}
	var doc document
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("composition: parse %s: %w", path, err)
	}

	c := &Composition{Path: path, Profile: profile, Preset: preset,
		rows: map[string]Row{}}
	// Overlay order: base, then the selected profile, then the selected
	// preset. A layer that is not present (unknown key, empty preset) is
	// simply absent — layers are optional, not errors.
	for _, layer := range [][]Row{doc.Base, doc.Profiles[profile], doc.Presets[preset]} {
		for _, r := range layer {
			if err := c.place(r); err != nil {
				return nil, err
			}
		}
	}
	return c, nil
}

// place applies one row under the overlay rule: first appearance fixes the
// id's position, any later row for that id replaces it whole (LWW).
func (c *Composition) place(r Row) error {
	if strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("composition: %s: a row with component %q has no id", c.Path, r.Component)
	}
	if strings.TrimSpace(r.Component) == "" {
		return fmt.Errorf("composition: %s: row %q has no component", c.Path, r.ID)
	}
	if _, seen := c.rows[r.ID]; !seen {
		c.order = append(c.order, r.ID)
	}
	c.rows[r.ID] = r
	return nil
}

// Rows returns the final rows in first-appearance order. This is both the
// dump order and the apply order — one walk, no divergence.
func (c *Composition) Rows() []Row {
	if c == nil || len(c.order) == 0 {
		return nil
	}
	out := make([]Row, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.rows[id])
	}
	return out
}

// MarshalJSON is the dump-config shape (--dump-config, W14.4): the base
// name (a golden file must not bake machine paths into the repo), the
// layers that were selected, and the resolved rows in apply order. Rows is
// never null — an empty composition diffs as [] every time. encoding/json
// sorts map keys, so Config renders byte-stably too.
func (c *Composition) MarshalJSON() ([]byte, error) {
	name := ""
	profile, preset := "", ""
	var rows []Row
	if c != nil {
		if c.Path != "" {
			name = filepath.Base(c.Path)
		}
		profile, preset = c.Profile, c.Preset
		rows = c.Rows()
	}
	if rows == nil {
		rows = []Row{}
	}
	return json.Marshal(struct {
		Path    string `json:"path"`
		Profile string `json:"profile"`
		Preset  string `json:"preset"`
		Rows    []Row  `json:"rows"`
	}{name, profile, preset, rows})
}
