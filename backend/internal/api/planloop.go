package api

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/tools"
)

// W3.1 — plan with eyes. Behind TALONSTUDIO_PLAN_TOOLS=v1 a plan turn runs a
// bounded loop over a read-only tool subset, so the planner surveys the real
// workspace before it commits to a plan. Mutation stays impossible by tool
// absence, not by promise (law 6): newPlanRegistry holds no writer, no
// runner, no shell — the loop executes only what the registry has, and the
// registry has only readers. The eval gates pin the set byte-for-byte
// (TestPhaseW3PlanRegistryReadOnly) and prove the eyes read real files
// (TestPhaseW3PlanTurnSeesFixtureRepo).
//
// W3.3 — bounded and never worse than before: maxPlanRounds tool rounds, one
// tool-less finalize call, and any loop failure that isn't the caller
// hanging up degrades to the tool-less single-shot ai.Plan plan mode has
// always run. The flag can add eyes; it can never subtract an answer or
// change the ai.Plan JSON reply contract the doors persist.

// maxPlanRounds bounds the eye-seeing part of one plan turn: how many tool
// rounds may run before the loop forces a final, tool-less answer. The cap
// is the cost guarantee behind the plan's "plan turns get slower/costlier"
// risk note (the round count is asserted by TestPhaseW3PlanRoundCap).
const maxPlanRounds = 8

// planToolsEnabled reads TALONSTUDIO_PLAN_TOOLS: planning-with-eyes ships
// dark, the same opt-in posture as the other harness flags — unset means
// exactly today's tool-less plan path, byte for byte.
func planToolsEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TALONSTUDIO_PLAN_TOOLS"))) {
	case "1", "true", "on", "v1":
		return true
	}
	return false
}

// newPlanRegistry wires the W3 read-only subset to one project's workspace —
// and nothing else. The subset is the plan's list exactly; widening it is a
// reviewable change, not a drive-by (the eval gate pins the set).
func (h *PromptHandler) newPlanRegistry(workspacePath string) *tools.Registry {
	fs := tools.NewFS(workspacePath)
	reg := tools.NewRegistry()
	reg.Register(&tools.ListFiles{FS: fs})
	reg.Register(&tools.ReadFile{FS: fs, Spill: h.spill})
	reg.Register(&tools.GrepCodebase{FS: fs})
	reg.Register(&tools.FindFiles{FS: fs})
	reg.Register(&tools.ProjectMap{FS: fs})
	reg.Register(&tools.Explore{FS: fs})
	return reg
}

// planWithTools runs one plan turn with eyes and returns the model's final
// raw text. Both doors persist and parse that text exactly as they always
// have (the Send door through ai.ParsePlan's contract, the stream door
// through its own unmarshal); with the flag off this function is never
// entered.
//
// The loop: the flat plan history as block messages, up to maxPlanRounds
// rounds of ChatWithTools over the read-only registry (each tool_use
// executed against the workspace and answered back verbatim), then one
// tool-less finalize call so a capped turn still answers in text. The final
// text goes through ai.Plan's reply contract — one corrective round if it
// isn't the JSON envelope, prose after that.
//
// Fallback (W3.3): no workspace to read, or any provider failure other than
// cancellation, degrades to the tool-less single-shot over the original
// flat history. The eyes are strictly additive.
func (h *PromptHandler) planWithTools(ctx context.Context, projectID int64, system string, history []ai.PromptMessage) (string, error) {
	workspacePath, err := h.workspacePath(projectID)
	if err != nil {
		// Nothing to read: degrade, don't fail — the old path had no eyes either.
		return h.planFallback(ctx, system, history, "no workspace: "+err.Error())
	}
	reg := h.newPlanRegistry(workspacePath)
	toolDefs := reg.List()
	defs := make([]ai.ToolDefinition, 0, len(toolDefs))
	for _, d := range toolDefs {
		defs = append(defs, ai.ToolDefinition{Name: d.Name, Description: d.Description, InputSchema: d.InputSchema})
	}

	// planHistory guarantees flat, text-only turns; lift them into block
	// messages for the agentic round.
	msgs := make([]ai.Message, 0, len(history))
	for _, m := range history {
		msgs = append(msgs, ai.Message{Role: m.Role, Blocks: []ai.Block{{Type: "text", Text: m.Content}}})
	}

	for round := 0; round < maxPlanRounds; round++ {
		res, err := h.ai.ChatWithTools(ctx, system, msgs, defs, nil)
		if err != nil {
			if ctx.Err() != nil {
				return "", err
			}
			return h.planFallback(ctx, system, history, err.Error())
		}
		if len(res.ToolUses) == 0 {
			return finalizePlanReply(ctx, h.ai, system, msgs, res), nil
		}
		// The assistant turn plus its results, verbatim: the planner sees
		// exactly what it asked for and exactly what came back.
		msgs = append(msgs, ai.Message{Role: "assistant", Blocks: res.Blocks})
		results := make([]ai.Block, 0, len(res.ToolUses))
		for _, tu := range res.ToolUses {
			tr, err := reg.Execute(ctx, tu.Name, tu.Input)
			if err != nil {
				// Registry execution fails only on context: the caller hung up.
				return "", err
			}
			results = append(results, ai.Block{
				Type: "tool_result", ToolUseID: tu.ID, Name: tu.Name,
				Content: tr.Content, IsError: tr.IsError,
			})
		}
		msgs = append(msgs, ai.ToolResults(results))
	}

	// Cap reached with the planner still looking around: one tool-less call
	// over the transcript forces the answer — the loop is bounded by
	// construction, never open-ended (W3.3).
	res, err := h.ai.ChatWithTools(ctx, system, msgs, nil, nil)
	if err != nil {
		if ctx.Err() != nil {
			return "", err
		}
		return h.planFallback(ctx, system, history, err.Error())
	}
	return finalizePlanReply(ctx, h.ai, system, msgs, res), nil
}

// finalizePlanReply applies ai.Plan's reply contract to the loop's last
// text: a parseable envelope passes through untouched (the doors parse the
// raw text themselves, exactly as the stream door always has); anything
// else gets one corrective round over the same transcript — the single
// retry ai.Plan gives a single-shot reply. Prose that still isn't JSON
// returns as prose, and the door surfaces it verbatim instead of failing
// the turn — today's fallback, unchanged.
func finalizePlanReply(ctx context.Context, client *ai.Client, system string, msgs []ai.Message, res *ai.AgenticChatResult) string {
	raw := res.Text
	if _, ok := ai.ParsePlan(raw); ok {
		return raw
	}
	msgs = append(msgs,
		ai.Message{Role: "assistant", Blocks: []ai.Block{{Type: "text", Text: raw}}},
		ai.UserText("That was not valid JSON. Send the same reply again as a single JSON object "+
			"with \"message\" and \"plan\" keys, and nothing outside the object."),
	)
	if res2, err := client.ChatWithTools(ctx, system, msgs, nil, nil); err == nil && strings.TrimSpace(res2.Text) != "" {
		return res2.Text
	}
	// The first reply is still worth showing even though the plan is lost
	// (ai.Plan's stance): return it and let the door fall back to prose.
	return raw
}

// planFallback is W3.3's guarantee: any eyes failure that isn't the caller
// hanging up degrades to the tool-less single-shot Plan over the original
// flat history, and its reply is re-expressed as the raw text the doors
// already know how to persist — envelope JSON when a plan settled, the
// message otherwise.
func (h *PromptHandler) planFallback(ctx context.Context, system string, history []ai.PromptMessage, reason string) (string, error) {
	log.Printf("plan: eyes unavailable (%s); falling back to tool-less planning", reason)
	resp, err := h.ai.Plan(ctx, system, history)
	if err != nil {
		return "", err
	}
	if resp.Plan == nil {
		return resp.Message, nil
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return resp.Message, nil
	}
	return string(raw), nil
}
