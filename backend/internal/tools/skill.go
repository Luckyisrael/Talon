package tools

import (
	"context"
	"encoding/json"
	"strings"
)

// SkillLibrary is the read side of the agent knowledge registry that read_skill
// needs. *agent.Registry satisfies it; depending on the interface keeps the tools
// package free of an import on the knowledge layer, matching how the other tools
// here take small interfaces (VisionChecker, StoreAdapter) rather than concretes.
type SkillLibrary interface {
	SkillBody(name string) (string, bool)
	SkillNames() []string
}

// ReadSkill loads one skill's full body on demand. The skill manifest in the
// system prompt lists what exists in a line each; this tool is how the model pulls
// the detail when it is about to work in that area. Keeping the bodies out of the
// prompt until asked for is the whole point — it holds quality up on weak models
// without paying for every skill on every request.
type ReadSkill struct{ Library SkillLibrary }

func (t *ReadSkill) Name() string { return "read_skill" }

func (t *ReadSkill) Description() string {
	names := "none loaded"
	if t.Library != nil {
		if n := t.Library.SkillNames(); len(n) > 0 {
			names = strings.Join(n, ", ")
		}
	}
	return "Load a project skill: a detailed, version-pinned how-to for part of this stack. " +
		"Read the relevant skill before working in its area — the guidance is current for the exact " +
		"versions installed here, which are newer than most training data. Available skills: " + names + "."
}

func (t *ReadSkill) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string", "description": "The skill name, exactly as listed in the manifest"}
		},
		"required": ["name"]
	}`)
}

// ReadOnly (in readonly.go) lets read_skill run alongside other reads in a turn —
// it only fetches embedded text and touches nothing.

func (t *ReadSkill) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}
	name := strings.TrimSpace(a.Name)
	if name == "" {
		return errResult("read_skill needs a name. Available skills: %s.", t.available()), nil
	}
	if t.Library == nil {
		return errResult("no skills are loaded in this build."), nil
	}
	body, ok := t.Library.SkillBody(name)
	if !ok {
		return errResult("no skill named %q. Available skills: %s.", name, t.available()), nil
	}
	return ToolResult{Content: body}, nil
}

func (t *ReadSkill) available() string {
	if t.Library == nil {
		return "none"
	}
	names := t.Library.SkillNames()
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
