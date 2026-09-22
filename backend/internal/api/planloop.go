package api

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/harness/instructions"
	"github.com/luckyisrael/talon/backend/internal/harness/promptsec"
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

// ── W3.2 — per-project plan prompt (map + survey + AGENTS chain) ────────

// planSurvey is one project's last successful W3.5 explore survey: the
// workspace fingerprint it was taken at and the digest it produced.
type planSurvey struct {
	fp     string
	digest string
}

// weightPlanSurvey places the survey digest between the project map (500)
// and memory (600): raw structure first, the cheap model's read of it next,
// conventions last. promptsec keeps the standard weights total; this one is
// local to the plan composition and still orders deterministically.
const weightPlanSurvey = 550

// maxSurveyRounds bounds one W3.5 survey: explore rounds over the
// single-tool registry, then one tool-less finalize — the plan loop's shape
// at a third of its budget (the survey is orientation, not planning).
const maxSurveyRounds = 3

// planSurveySystem/User prompt the cheap-model survey (W3.5). The digest
// lands verbatim as the promptsec "project/survey" section, so it is plain
// text by instruction — JSON here would leak into the system prompt.
const planSurveySystem = `You are surveying a workspace before a plan is drafted for it. You have one tool, explore — a bounded overview of a project (tree, name/pattern search, one file read). Call it: the tree first, then a targeted read only if something needs checking. Then write the digest: plain text, at most 120 words — what the project is, its shape (key directories and files), and anything a planner must know. No JSON, no preamble, no recommendations.`

const planSurveyUser = `Survey this workspace and send the digest.`

// planSystemPrompt assembles the plan-mode system prompt for one project
// (W3.2). With TALONSTUDIO_PLAN_TOOLS unset it returns h.planPrompt without
// touching the workspace, the caches, or the model — flag-off plan turns
// stay byte-identical. With the flag on the prompt becomes an ordered
// promptsec registry: the static plan head, the project map, the W3.5
// survey digest when the fingerprint says the last one is stale, and the
// AGENTS.md chain — sections rendered once, omitted when empty,
// fingerprinted so a moved section is visible.
func (h *PromptHandler) planSystemPrompt(ctx context.Context, projectID int64) string {
	if !planToolsEnabled() {
		return h.planPrompt
	}
	ws, err := h.workspacePath(projectID)
	if err != nil || ws == "" {
		// The same degrade planWithTools applies: no workspace, no eyes —
		// and no injected sections either; the static head is a complete
		// prompt, exactly what flag-off sends.
		return h.planPrompt
	}
	survey := h.planSurveyDigest(ctx, projectID, ws, tools.ProjectFingerprint(ws))
	reg := h.planPromptSections(ws, projectID, survey)
	h.notePlanSections(projectID, reg)
	return reg.Assemble()
}

// planPromptSections builds the plan composition's section registry. Each
// dynamic section renders exactly once — the closures capture the rendered
// text — so Assemble and Fingerprints agree byte-for-byte and neither walk
// the workspace twice. projectID feeds the W4.6 map view: the map section is
// a view over the code index when the flag is on, the walk otherwise.
func (h *PromptHandler) planPromptSections(workspacePath string, projectID int64, survey string) *promptsec.Registry {
	mapText := h.projectMapView(workspacePath, projectID)
	chainText := h.instructionsChainText(workspacePath)

	reg := promptsec.New()
	reg.Register(promptsec.Section{
		Name:   "plan/head",
		Weight: promptsec.WeightPersona,
		Render: func() string { return h.planPrompt },
	})
	reg.Register(promptsec.Section{
		Name:   "project/map",
		Weight: promptsec.WeightProjectMap,
		Render: func() string { return mapText },
	})
	reg.Register(promptsec.Section{
		Name:   "project/survey",
		Weight: weightPlanSurvey,
		Render: func() string { return survey },
	})
	reg.Register(promptsec.Section{
		Name:   "instructions/chain",
		Weight: promptsec.WeightInstructions,
		Render: func() string { return chainText },
	})
	return reg
}

// instructionsChainText is the H6 AGENTS.md chain for plan assembly: the
// factory main attached (byte-identical to the orchestrator's consumer of
// the same closure), or the workspace-only chain when nothing attached one.
func (h *PromptHandler) instructionsChainText(workspacePath string) string {
	if h.instructionsChain != nil {
		return h.instructionsChain(workspacePath)
	}
	return instructions.Loader{WorkspaceRoot: workspacePath}.Load().Text()
}

// notePlanSections logs which sections moved since this project's last plan
// turn — the fingerprint half of the cache discipline: a change late in the
// order (chain, survey) leaves the head and map hashes untouched, so the
// prefix a prompt cache still covers is provably intact.
func (h *PromptHandler) notePlanSections(projectID int64, reg *promptsec.Registry) {
	fps := reg.Fingerprints()
	h.planSurveyMu.Lock()
	prev := h.planSectionFP[projectID]
	h.planSectionFP[projectID] = fps
	h.planSurveyMu.Unlock()
	if prev == nil {
		return // first plan turn for this project: everything would be "added"
	}
	if changed := promptsec.Diff(prev, fps); len(changed) > 0 {
		log.Printf("plan: prompt sections changed for project %d: %s", projectID, strings.Join(changed, ", "))
	}
}

// planSurveyDigest is W3.5's gate: the digest is reused while the workspace
// fingerprint matches the one the last successful survey was taken at; a
// changed (or never-taken) fingerprint runs the cheap-model explore survey
// before drafting. A failed survey degrades to no digest and is NOT cached
// — the next plan turn retries — and never fails the turn itself.
func (h *PromptHandler) planSurveyDigest(ctx context.Context, projectID int64, workspacePath, fp string) string {
	h.planSurveyMu.Lock()
	cached, ok := h.planSurveyCache[projectID]
	h.planSurveyMu.Unlock()
	if ok && cached.fp == fp {
		return cached.digest
	}
	digest, err := h.exploreSurvey(ctx, workspacePath)
	if err != nil {
		log.Printf("plan: survey unavailable (project %d): %v", projectID, err)
		return ""
	}
	h.planSurveyMu.Lock()
	h.planSurveyCache[projectID] = planSurvey{fp: fp, digest: digest}
	h.planSurveyMu.Unlock()
	return digest
}

// exploreSurvey runs the W3.5 survey on the cheap tier: the routing
// policy's TierMedium model (AI_MODEL_CHEAP in the compiled defaults — the
// same cheap path ClassifyTurn's small-edit branch takes), one bounded loop
// over the single {explore} registry, then a tool-less finalize. Any
// provider failure returns an error the caller degrades on — the survey can
// add eyes, never fail a turn.
func (h *PromptHandler) exploreSurvey(ctx context.Context, workspacePath string) (string, error) {
	client := h.ai
	if model := ai.LoadRoutingPolicy().Models[ai.TierMedium]; model != "" && model != h.ai.Model() {
		client = h.ai.WithModel(model)
	}
	explore := tools.NewRegistry()
	explore.Register(&tools.Explore{FS: tools.NewFS(workspacePath)})
	toolDefs := explore.List()
	defs := make([]ai.ToolDefinition, 0, len(toolDefs))
	for _, d := range toolDefs {
		defs = append(defs, ai.ToolDefinition{Name: d.Name, Description: d.Description, InputSchema: d.InputSchema})
	}

	msgs := []ai.Message{ai.UserText(planSurveyUser)}
	for round := 0; round < maxSurveyRounds; round++ {
		res, err := client.ChatWithTools(ctx, planSurveySystem, msgs, defs, nil)
		if err != nil {
			return "", err
		}
		if len(res.ToolUses) == 0 {
			return strings.TrimSpace(res.Text), nil
		}
		msgs = append(msgs, ai.Message{Role: "assistant", Blocks: res.Blocks})
		results := make([]ai.Block, 0, len(res.ToolUses))
		for _, tu := range res.ToolUses {
			tr, err := explore.Execute(ctx, tu.Name, tu.Input)
			if err != nil {
				return "", err // a registry read fails only when the caller hangs up
			}
			results = append(results, ai.Block{
				Type: "tool_result", ToolUseID: tu.ID, Name: tu.Name,
				Content: tr.Content, IsError: tr.IsError,
			})
		}
		msgs = append(msgs, ai.ToolResults(results))
	}
	res, err := client.ChatWithTools(ctx, planSurveySystem, msgs, nil, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Text), nil
}
