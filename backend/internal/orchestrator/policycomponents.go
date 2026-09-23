// Composition wiring inside the orchestrator (H12/W14.2): the registry a
// composition row resolves through. A new guardrail lands as ONE new file
// calling RegisterPolicyComponent in its init() plus ONE row in
// talon.composition.yaml — zero edits to the loop, the guard stage, or the
// composer. This file owns registry + the loop's boot-time knobs;
// internal/composer owns walking the rows.
package orchestrator

import (
	"fmt"
	"sort"
	"sync"
)

// PolicyComponentFactory builds a policy listener from a composition row's
// config. The config arrives exactly as YAML parsed it; validation is the
// factory's job — a factory that cannot make sense of its config returns an
// error, and Apply fails the boot with the row named.
type PolicyComponentFactory func(config map[string]any) (ToolPolicyListener, error)

var (
	policyComponentsMu sync.RWMutex
	policyComponents   = map[string]PolicyComponentFactory{}
)

// RegisterPolicyComponent publishes a composition-addressable guardrail
// under component "policy/<name>". Call it from init() in the component's
// own file. A duplicate name panics: two components answering to one name
// is a silent shadowing that must surface at boot, not at 3am.
func RegisterPolicyComponent(name string, f PolicyComponentFactory) {
	if name == "" || f == nil {
		panic("orchestrator: RegisterPolicyComponent needs a name and a factory")
	}
	policyComponentsMu.Lock()
	defer policyComponentsMu.Unlock()
	if _, dup := policyComponents[name]; dup {
		panic(fmt.Sprintf("orchestrator: policy component %q registered twice", name))
	}
	policyComponents[name] = f
}

// LookupPolicyComponent resolves "policy/<name>" rows; nil means the
// component does not exist (the composer turns that into a boot error —
// never a silent skip).
func LookupPolicyComponent(name string) PolicyComponentFactory {
	policyComponentsMu.RLock()
	defer policyComponentsMu.RUnlock()
	return policyComponents[name]
}

// PolicyComponentNames lists the registered components, sorted — boot logs,
// error messages, and the dump-config surface read it for "what exists".
func PolicyComponentNames() []string {
	policyComponentsMu.RLock()
	defer policyComponentsMu.RUnlock()
	names := make([]string, 0, len(policyComponents))
	for n := range policyComponents {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// SetMaxRounds overrides the round budget at boot — the composition's
// harness/loop row applied after New read AI_MAX_ROUNDS (data beats seed,
// the settings-document precedent). The bounds are the env reader's own
// [minMaxRounds, maxMaxRounds]; out of range is an error so a bad row
// fails the boot instead of silently clamping. Boot-only: call before the
// first turn, never mid-flight.
func (o *Orchestrator) SetMaxRounds(n int) error {
	if n < minMaxRounds || n > maxMaxRounds {
		return fmt.Errorf("maxRounds %d out of range [%d, %d]", n, minMaxRounds, maxMaxRounds)
	}
	o.maxRounds = n
	return nil
}

// MaxRounds reports the active round budget — the composed value after
// boot (composition row if one applied, else the env seed, else the
// default). Tests and the dump surface read it.
func (o *Orchestrator) MaxRounds() int {
	return o.maxRounds
}
