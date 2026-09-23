// Package composition is the H12 declarative composition: talon.composition.yaml
// becomes the wiring source, main.go becomes a composer, and model routing
// becomes runtime data (the one watched settings document behind
// ai.AttachSettingsFile) instead of boot-time env.
//
// This file holds the preset layer — per-project composition shaping — and
// the overlay rule. The full YAML composer landed in yaml.go (W14.2): rows
// resolve through base → profile → preset under exactly this overlay
// contract, and internal/composer applies them to live wiring — this
// package stays data-pure (no wiring imports).
package composition

import (
	"errors"
	"strings"
)

// Preset is a named project composition shape.
type Preset struct {
	// Name identifies the preset ("starter", "expert", "fast").
	Name string
	// Discipline selects the working-discipline section's register:
	// "spelled-out" names every step, "compressed" trusts the model.
	Discipline string
	// SkillsSections lists the skills catalog's shape: "all" lists every
	// skill, "core" lists the always-relevant subset.
	SkillsSections string
	// StartGuide includes the SCL starter-guide section (starter only).
	StartGuide bool
	// RoutingTightPrune tightens the tool-result pruner (fast preset).
	RoutingTightPrune bool
}

// ErrNoOutputYet is the switch guard: a preset switch requires a project
// with no produced output — an in-flight build never changes composition
// mid-turn (cache stability is the law).
var ErrNoOutputYet = errors.New("PRESET_SWITCH: the project already has produced output — an in-flight build never changes composition mid-turn; finish or revert the work first")

// ErrUnknownPreset names an unresolvable preset.
var ErrUnknownPreset = errors.New("unknown preset")

// Catalog is the known presets.
var Catalog = []Preset{
	{
		Name:           "starter",
		Discipline:     "spelled-out",
		SkillsSections: "all",
		StartGuide:     true,
	},
	{
		Name:           "expert",
		Discipline:     "compressed",
		SkillsSections: "all",
		StartGuide:     false,
	},
	{
		Name:              "fast",
		Discipline:        "compressed",
		SkillsSections:    "core",
		StartGuide:        false,
		RoutingTightPrune: true,
	},
}

// Resolve finds a preset by name (case-insensitive, trimmed).
func Resolve(name string) (Preset, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, p := range Catalog {
		if strings.ToLower(p.Name) == n {
			return p, nil
		}
	}
	return Preset{}, ErrUnknownPreset
}

// SwitchPolicy decides whether a preset switch is allowed. DSH's rule,
// stated for Talon's shape: the switch is allowed only while the
// project has produced nothing — no files written, no builds run — because
// a mid-build composition change breaks the prompt-cache discipline and the
// verify ladder's assumptions.
type SwitchPolicy struct{}

// CanSwitch reports whether the project at the given production state may
// switch presets. hasOutput means anything durable exists (files, builds).
func (SwitchPolicy) CanSwitch(hasOutput bool) error {
	if hasOutput {
		return ErrNoOutputYet
	}
	return nil
}

// Overlay is the composition overlay rule: base → mode → preset, last write
// wins per key, whole-config replacement at each layer (no partial merges —
// a layer either provides a value or does not mention the key; DSH's exact
// rule, kept word for word in spirit).
type Overlay struct {
	Base   map[string]string
	Mode   map[string]string
	Preset map[string]string
}

// Apply renders the effective config: for each key in any layer, the value
// from the highest layer that mentions it (preset > mode > base).
func (o Overlay) Apply() map[string]string {
	out := map[string]string{}
	for k, v := range o.Base {
		out[k] = v
	}
	for k, v := range o.Mode {
		out[k] = v
	}
	for k, v := range o.Preset {
		out[k] = v
	}
	return out
}
