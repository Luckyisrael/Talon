package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// validateArgs checks a tool call's arguments against the tool's declared JSON
// schema and, when they do not fit, returns a message the model can correct from
// in a single round.
//
// It exists because of one specific, expensive failure mode. A model driving the
// loop over an OpenAI-compatible or Anthropic endpoint sometimes sends arguments
// keyed for a different tool — old_string/new_string handed to write_file, a
// whole content blob handed to edit_file. There is no provider-side strict mode
// we can rely on across OpenRouter and the rest, and Go's json.Unmarshal silently
// drops fields it does not recognise, so the malformed call reaches the tool,
// "succeeds" doing nothing, and the model repeats the same mistake round after
// round. Catching it here — at the one dispatch point every call passes through —
// turns that silent death-spiral into a single actionable error.
//
// It validates only the top level: unknown keys and missing required keys, the
// class of mistake actually observed. It is deliberately permissive everywhere
// else. A schema it cannot parse, or one that declares no properties, passes
// everything through — this is a guard against a known failure, not a second type
// system, and it must never reject a call a tool would have handled. That safety
// rests on an invariant the tests pin: every tool reads only fields it declares.
func validateArgs(toolName string, schema, args json.RawMessage) (string, bool) {
	var sh struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	// No usable schema means nothing to check against — do not second-guess it.
	if err := json.Unmarshal(schema, &sh); err != nil || len(sh.Properties) == 0 {
		return "", true
	}

	var provided map[string]json.RawMessage
	if err := json.Unmarshal(args, &provided); err != nil {
		// Not a JSON object. The tool's own Unmarshal will report the parse error
		// with more context than we could here.
		return "", true
	}

	var unknown, missing []string
	for key := range provided {
		if _, ok := sh.Properties[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	for _, req := range sh.Required {
		if _, ok := provided[req]; !ok {
			missing = append(missing, req)
		}
	}
	if len(unknown) == 0 && len(missing) == 0 {
		return "", true
	}

	sort.Strings(unknown)
	sort.Strings(missing)

	allowed := make([]string, 0, len(sh.Properties))
	for key := range sh.Properties {
		allowed = append(allowed, key)
	}
	sort.Strings(allowed)

	var b strings.Builder
	// Lead with "did not run": the death-spiral was the model believing a
	// malformed edit had taken effect. It must know nothing happened.
	fmt.Fprintf(&b, "%s: invalid arguments, so the call did not run.", toolName)
	if len(unknown) > 0 {
		fmt.Fprintf(&b, " Unexpected: %s.", strings.Join(unknown, ", "))
	}
	if len(missing) > 0 {
		fmt.Fprintf(&b, " Missing required: %s.", strings.Join(missing, ", "))
	}
	if len(sh.Required) > 0 {
		required := append([]string(nil), sh.Required...)
		sort.Strings(required)
		fmt.Fprintf(&b, " This tool accepts: %s (required: %s).",
			strings.Join(allowed, ", "), strings.Join(required, ", "))
	} else {
		fmt.Fprintf(&b, " This tool accepts: %s.", strings.Join(allowed, ", "))
	}
	b.WriteString(" These may belong to a different tool — check this tool's schema and call it again with the right arguments.")
	return b.String(), false
}
