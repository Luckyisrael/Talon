// deny-tools: the shipped example of a composition-addressable guardrail
// (W14.2). The deterministic guardrails in guardrail.Check answer path
// questions (write where, read what); this component answers a name
// question — a workspace that refuses run_shell outright states it in
// talon.composition.yaml instead of editing code:
//
//	base:
//	  - id: no-shell
//	    component: policy/deny-tools
//	    config:
//	      tools: [run_shell, run_command]
//
// It runs on the listener bus like every policy component: only under
// TALONSTUDIO_LOOP=driver, and only after the built-in guardrails allow —
// composition adds restrictions, it never weakens them.
package orchestrator

import (
	"fmt"
	"strings"
)

func init() {
	RegisterPolicyComponent("deny-tools", denyToolsComponent)
}

// denyToolsComponent parses the row's config into a name-set listener. A
// config that names nothing (or no config at all) is an error: a component
// that denies nothing is a typo, and a typo must fail the boot loudly.
func denyToolsComponent(config map[string]any) (ToolPolicyListener, error) {
	raw, ok := config["tools"]
	if !ok {
		return nil, fmt.Errorf("config.tools is required (a component that denies nothing is a typo)")
	}
	var names []string
	switch v := raw.(type) {
	case []string:
		names = v
	case []any:
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("tools[%d] must be a tool name, found %T", i, item)
			}
			names = append(names, s)
		}
	default:
		return nil, fmt.Errorf("tools must be a list of tool names, found %T", raw)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("tools must name at least one tool")
	}

	deny := make(map[string]bool, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			return nil, fmt.Errorf("tools contains an empty name")
		}
		deny[n] = true
	}
	return func(ev PolicyEvent) PolicyDecision {
		if deny[ev.Call.Name] {
			return PolicyDecision{
				Allow:  false,
				Rule:   "deny-tools",
				Reason: fmt.Sprintf("%s is denied by the workspace composition", ev.Call.Name),
			}
		}
		return PolicyDecision{Allow: true}
	}, nil
}
