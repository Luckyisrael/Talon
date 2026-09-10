package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/seekersstudio/backend/internal/harness/workflow"
)

// RunWorkflow executes a declarative fan-out spec: parallel child checks,
// then an aggregate. The model authors the spec as JSON — the engine runs it
// with per-item isolation (a failed item nulls itself, never the run).
type RunWorkflow struct {
	Engine *workflow.Engine
}

func (t *RunWorkflow) Name() string { return "run_workflow" }

func (t *RunWorkflow) Description() string {
	return "Run a declarative fan-out workflow when a task decomposes into many independent pieces: " +
		"verify every screen in parallel, review several packages at once, then aggregate. " +
		"You write a JSON spec; the engine runs each piece as a child agent with per-item isolation " +
		"(one failed piece nulls itself, never the run) and returns every phase's results in order. " +
		"Example spec: " +
		`{"name":"verify-screens","phases":[{"title":"checks","parallel":[` +
		`{"prompt":"Boot and vision-check the Wallet screen; answer pass/fail","schema":{"pass":"boolean","reason":"string"}},` +
		`{"prompt":"Boot and vision-check the Settings screen; answer pass/fail","schema":{"pass":"boolean","reason":"string"}}]},` +
		`{"title":"aggregate","prompt":"Given the phase-1 results, write the verify summary"}]}. ` +
		"Every phase's result is JSON; use schemas to get structured answers back."
}

func (t *RunWorkflow) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"spec": {"type": "object", "description": "The workflow spec: {name, phases: [{title, parallel: [{prompt, schema}] | prompt}]}"}
		},
		"required": ["spec"]
	}`)
}

func (t *RunWorkflow) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Spec json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}
	if t.Engine == nil {
		return errResult("workflows are not available in this session."), nil
	}
	var spec workflow.Spec
	if err := json.Unmarshal(a.Spec, &spec); err != nil {
		return errResult("the spec is not valid workflow JSON: %v", err), nil
	}
	if err := spec.Validate(); err != nil {
		return errResult("the spec is invalid: %v", err), nil
	}
	res := t.Engine.Run(ctx, spec)
	out, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return errResult("workflow result could not be serialized: %v", err), nil
	}
	if res.Errored {
		return ToolResult{Content: string(out), IsError: true}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Workflow %q completed: %d phases", res.Name, len(res.Phases))
	if res.Phases != nil {
		for _, p := range res.Phases {
			fmt.Fprintf(&b, "; %s (%d results, %d dropped)", p.Title, len(p.Results), p.Nulls)
		}
	}
	b.WriteString("\n\n")
	b.WriteString(string(out))
	return ToolResult{Content: b.String()}, nil
}
