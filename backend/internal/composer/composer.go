// Package composer applies a resolved composition document to live wiring —
// the row-application half of H12's "main.go becomes a composer". It is the
// ONE place a row can take effect: cmd/server and cmd/talon-headless both
// call Load+Apply, so both profiles boot the same document and there is no
// second wiring for config-as-code drift to grow in.
//
// The layering: harness/composition stays data-pure (the document and its
// overlay rules, importing no wiring packages); this package is where rows
// meet the orchestrator. Unknown components are errors — a wiring file that
// quietly stops working is the drift composition exists to prevent.
package composer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/luckyisrael/talon/backend/internal/harness/composition"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
)

// compositionFile is the tree's wiring file name — searched for, never
// assumed to exist.
const compositionFile = "talon.composition.yaml"

// Load resolves this boot's composition.
//
// TALONSTUDIO_COMPOSITION names an explicit file and fails loud when it
// does not exist — naming a file that is not there is a typo, and a typo
// must not silently become a dark boot. Without it, the search walks up
// from the working directory (this tree's wiring lives in this tree: launch
// from the repo root or from backend/ and the same file resolves), taking
// the first talon.composition.yaml. Nothing found is a dark boot —
// byte-identical to before the file existed.
//
// Layers: base → profiles[profile] → presets[preset]. The profile defaults
// to defaultProfile (the binary's own name — "server", "headless") and is
// overridden by TALONSTUDIO_PROFILE; the preset comes from
// TALONSTUDIO_PRESET (unset = the preset layer is skipped).
func Load(defaultProfile string) (*composition.Composition, error) {
	profile := strings.TrimSpace(os.Getenv("TALONSTUDIO_PROFILE"))
	if profile == "" {
		profile = defaultProfile
	}
	preset := strings.TrimSpace(os.Getenv("TALONSTUDIO_PRESET"))

	if path := strings.TrimSpace(os.Getenv("TALONSTUDIO_COMPOSITION")); path != "" {
		comp, err := composition.Load(path, profile, preset)
		if err != nil {
			return nil, err
		}
		if comp == nil {
			return nil, fmt.Errorf("%s does not exist (TALONSTUDIO_COMPOSITION is set — an explicit composition path must resolve)", path)
		}
		return comp, nil
	}

	dir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("composition: cannot determine the working directory: %w", err)
	}
	for i := 0; i < 4; i++ {
		comp, err := composition.Load(filepath.Join(dir, compositionFile), profile, preset)
		if err != nil {
			return nil, err // a found-but-broken file stops the walk: fix it, do not boot around it
		}
		if comp != nil {
			return comp, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil, nil
}

// Apply runs every resolved row against the wiring and returns the ids in
// apply order (boot-only — before the first turn, the same contract as
// SetMaxRounds). Every error names the row; an unknown component is an
// error, never a silent skip. A nil composition is a dark boot and returns
// nothing applied.
func Apply(c *composition.Composition, o *orchestrator.Orchestrator) ([]string, error) {
	if c == nil {
		return nil, nil
	}
	if o == nil {
		return nil, fmt.Errorf("composition %s: no wiring to apply to", c.Path)
	}

	rows := c.Rows()
	applied := make([]string, 0, len(rows))
	for _, row := range rows {
		if err := applyRow(row, o); err != nil {
			return nil, fmt.Errorf("composition %s: row %q (%s): %w", c.Path, row.ID, row.Component, err)
		}
		applied = append(applied, row.ID)
	}
	return applied, nil
}

// applyRow dispatches one row by component name. Known components:
//
//	harness/loop — the round budget (config.maxRounds)
//	policy/<name> — a registered policy component (orchestrator's registry)
//
// Anything else is an error naming what is known: composition's example
// documents stage rows the existing per-project contracts already own
// (sandbox, approval, verify, …) — those flip their source of truth in a
// later slice with their own parity gate, and until then a row for them
// fails loudly instead of pretending.
func applyRow(row composition.Row, o *orchestrator.Orchestrator) error {
	switch {
	case row.Component == "harness/loop":
		return applyLoop(row.Config, o)
	case strings.HasPrefix(row.Component, "policy/"):
		return applyPolicy(row, o)
	default:
		return fmt.Errorf("unknown component (known: harness/loop, policy/<name>)")
	}
}

// applyLoop applies the round budget. maxRounds is optional: a loop row
// that omits it leaves the env seed in place (composition overrides the
// seed only where it actually says something).
func applyLoop(config map[string]any, o *orchestrator.Orchestrator) error {
	raw, ok := config["maxRounds"]
	if !ok {
		return nil
	}
	n, ok := asInt(raw)
	if !ok {
		return fmt.Errorf("maxRounds must be an integer, found %T", raw)
	}
	return o.SetMaxRounds(n)
}

// applyPolicy resolves a policy row through the orchestrator's registry and
// subscribes the built listener. An unregistered name lists what IS
// registered, so the error teaches the fix.
func applyPolicy(row composition.Row, o *orchestrator.Orchestrator) error {
	name := strings.TrimSpace(strings.TrimPrefix(row.Component, "policy/"))
	if name == "" {
		return fmt.Errorf("component names no policy (use policy/<name>)")
	}
	f := orchestrator.LookupPolicyComponent(name)
	if f == nil {
		return fmt.Errorf("unknown component (registered: %s)",
			strings.Join(orchestrator.PolicyComponentNames(), ", "))
	}
	listener, err := f(row.Config)
	if err != nil {
		return err
	}
	o.SubscribeLoopPolicy(listener)
	return nil
}

// asInt accepts the integer shapes YAML produces. A float is not an
// integer: maxRounds 60.0 is a config mistake, and the boot says so.
func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	default:
		return 0, false
	}
}
