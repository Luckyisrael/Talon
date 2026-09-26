package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/luckyisrael/talon/backend/internal/ai"
)

// ── Turn state card ──────────────────────────────────────────────
//
// Conversation history is an event log, not working memory. Forty rounds
// into a build, "what am I doing, what have I already read, have I written
// anything?" is buried under kilobytes of tool output — and a model that
// cannot answer those questions re-reads files it already holds and spins
// for twenty rounds without writing a line. The turn state card answers
// them explicitly: a compact situation report the SYSTEM maintains (the
// model maintains nothing), regenerated every round from the transcript
// and appended to the wire copy of the conversation only.
//
// Two properties make it cheap and safe:
//
//   - Derived, never stored: the card is computed from the transcript on
//     each round and never persisted, so the stored history stays the raw
//     record, a resumed turn reconstructs the same card as an
//     uninterrupted one, and there is no state to drift or corrupt. It
//     rides the LAST message of the wire copy, where the prompt cache's
//     breakpoint (len-2) never lands, so the cached prefix is untouched.
//   - Bounded: a handful of lines, path counts instead of bodies, one
//     rule-derived NEXT directive. It replaces itself every round — it
//     never accumulates.
//
// The NEXT line is the structural countermeasure to the read spiral: prompt
// guidance is prose a model can skim past, but a directive inside its
// freshest context, restated every round once the condition holds, is not
// skimmable. TALONSTUDIO_TURN_STATE=0/false/off turns the card off.

const (
	// turnStateGoalLen caps the goal line: the opener's first sentence or
	// two, enough to anchor, never the whole prompt.
	turnStateGoalLen = 160
	// turnStateErrHead caps the last-error excerpt.
	turnStateErrLen = 140
	// turnStateListMax is how many paths each count list shows before
	// "+N more".
	turnStateListMax = 4
	// turnStatePlanReads is the plan-mode read budget: past it the card
	// tells the model to stop scoping and write the plan.
	turnStatePlanReads = 6
	// turnStateIdleRound is the round at which zero file writes becomes a
	// directive. Rounds 1-3 are normal orientation; round 4 with nothing
	// written is the start of the observed spiral.
	turnStateIdleRound = 4
)

// turnStateEnabled is the flag-dark escape hatch: this card changes what
// the model sees every round, so a deployment must be able to turn it off
// without a rebuild.
func turnStateEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TALONSTUDIO_TURN_STATE"))) {
	case "0", "false", "off":
		return false
	}
	return true
}

// turnCheck is one verification-class call this turn, verdict resolved.
type turnCheck struct {
	tool    string
	verdict string // PASS | FAIL | ERROR | ran
	head    string // summary/error excerpt on FAIL and ERROR
}

// turnStateScan is this turn's activity, derived from the transcript.
type turnStateScan struct {
	goal       string
	reads      map[string]int // target → read calls (read_file, list, grep, …)
	readCalls  int
	writes     map[string]int // path → write calls (write_file, edit_file, …)
	writeCalls int
	checks     []turnCheck // verification-class calls, first-appearance order
	checkSeen  map[string]int
	errCount   int
	lastErr    string
}

// turnStateStart returns the index of this turn's first message: one past
// the last turn_end marker, since every settled turn writes exactly one.
// With no marker anywhere the whole replay is this turn — a first-ever
// turn, or a crash before any turn settled — and counting from zero is the
// safe direction: over-counting older activity can only suppress the idle
// nudge, never fire it at a turn that already wrote.
func turnStateStart(conversation []ai.Message) int {
	for i := len(conversation) - 1; i >= 0; i-- {
		for _, b := range conversation[i].Blocks {
			if b.Type == "turn_end" {
				return i + 1
			}
		}
	}
	return 0
}

// scanTurnState walks this turn's messages and accumulates what the model
// has actually done. Only tool_use/tool_result blocks are read — cards and
// nudges other mechanisms inject are ignored by construction.
func scanTurnState(msgs []ai.Message, turnStart int) *turnStateScan {
	ts := &turnStateScan{
		reads:     make(map[string]int),
		writes:    make(map[string]int),
		checkSeen: make(map[string]int),
	}
	if turnStart < 0 {
		turnStart = 0
	}
	if turnStart >= len(msgs) {
		return ts
	}

	// Goal: the turn opener's own words — first user message at or after
	// the boundary that actually carries text.
	for i := turnStart; i < len(msgs) && ts.goal == ""; i++ {
		if msgs[i].Role == "user" {
			if t := strings.TrimSpace(msgs[i].Text()); t != "" {
				ts.goal = oneLine(truncateRunes(t, turnStateGoalLen))
			}
		}
	}

	// Results first: the verdict on a check and the excerpt on an error
	// live in the tool_result, keyed back to the call that produced it.
	results := make(map[string]ai.Block)
	for i := turnStart; i < len(msgs); i++ {
		for _, b := range msgs[i].Blocks {
			if b.Type == "tool_result" && b.ToolUseID != "" {
				results[b.ToolUseID] = b
			}
		}
	}

	for i := turnStart; i < len(msgs); i++ {
		for _, b := range msgs[i].Blocks {
			if b.Type != "tool_use" {
				continue
			}
			ts.observe(b, results[b.ID])
		}
	}
	return ts
}

// readTools are the orientation calls whose repetition is the spiral's
// signature: they cost a round each and add no code.
var readTools = map[string]bool{
	"read_file": true, "list_files": true, "grep_codebase": true,
	"search_files": true, "find_files": true, "explore": true,
	"codebase_search": true, "codebase_graph": true, "inspect_package": true,
	"read_skill": true, "lsp_diagnostics": true, "lsp_references": true,
	"lsp_definition": true, "lsp_symbol": true,
}

// writeTools are the path-targeted mutations — the same set the sandbox
// treats as writes (api/buildsession.go mutatingToolPath).
var writeTools = map[string]bool{
	"write_file": true, "edit_file": true, "multi_edit": true, "delete_file": true,
}

// checkTools are the verification-class calls whose verdicts the card
// reports. run_shell rides along: its command and outcome are the turn's
// "what did I run" record.
var checkTools = map[string]bool{
	"run_typecheck": true, "run_lint": true, "run_checks": true,
	"run_prettier": true, "run_shell": true,
}

// observe folds one tool call (and its result) into the scan.
func (ts *turnStateScan) observe(use, res ai.Block) {
	target := stateToolTarget(use.Name, use.Input)
	switch {
	case readTools[use.Name]:
		ts.readCalls++
		ts.reads[target]++
	case writeTools[use.Name]:
		ts.writeCalls++
		ts.writes[target]++
	case checkTools[use.Name]:
		ts.recordCheck(use.Name, res)
	}

	if res.IsError {
		ts.errCount++
		ts.lastErr = use.Name + ": " + oneLine(truncateRunes(res.Content, turnStateErrLen))
	}
}

// recordCheck keeps the latest verdict per check tool, first-appearance
// order preserved so the line reads like a checklist, not a map.
func (ts *turnStateScan) recordCheck(tool string, res ai.Block) {
	c := turnCheck{tool: tool, verdict: "ran"}
	switch {
	case res.IsError:
		c.verdict = "ERROR"
		c.head = oneLine(truncateRunes(res.Content, 90))
	default:
		// The verification tools speak verifyResult: an explicit "passed"
		// field is the verdict. A tool that omits it (run_shell's free-form
		// output) simply "ran" — claiming pass/fail from banner text is how
		// honest checkers get misread.
		var shape map[string]json.RawMessage
		if json.Unmarshal([]byte(res.Content), &shape) == nil {
			if raw, ok := shape["passed"]; ok {
				var passed bool
				_ = json.Unmarshal(raw, &passed)
				if passed {
					c.verdict = "PASS"
				} else {
					c.verdict = "FAIL"
					c.head = verifyHead(shape)
				}
			}
		}
	}
	if i, seen := ts.checkSeen[tool]; seen {
		ts.checks[i] = c
		return
	}
	ts.checkSeen[tool] = len(ts.checks)
	ts.checks = append(ts.checks, c)
}

// verifyHead pulls the most informative one-liner out of a failed verdict.
func verifyHead(shape map[string]json.RawMessage) string {
	for _, key := range []string{"summary", "errors"} {
		if raw, ok := shape[key]; ok {
			var s string
			if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
				return oneLine(truncateRunes(s, 90))
			}
		}
	}
	return ""
}

// stateToolTarget names what a call touched: its path, its command, or the
// tool itself when the input carries neither.
func stateToolTarget(name string, input json.RawMessage) string {
	var a struct {
		Path    string `json:"path"`
		Command string `json:"command"`
	}
	_ = json.Unmarshal(input, &a)
	if a.Path != "" {
		return a.Path
	}
	if a.Command != "" {
		return oneLine(truncateRunes(a.Command, 48))
	}
	return name
}

// renderPathCounts renders "a.ts ×7, b.ts, +2 more" plus the total call
// count, most-repeated first — the shape that makes waste visible without
// listing bodies.
func renderPathCounts(counts map[string]int, max int) (string, int) {
	total := 0
	type kv struct {
		k string
		n int
	}
	entries := make([]kv, 0, len(counts))
	for k, n := range counts {
		total += n
		entries = append(entries, kv{k, n})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].n != entries[j].n {
			return entries[i].n > entries[j].n
		}
		return entries[i].k < entries[j].k
	})
	parts := make([]string, 0, max+1)
	for i, e := range entries {
		if i >= max {
			parts = append(parts, fmt.Sprintf("+%d more", len(entries)-max))
			break
		}
		if e.n > 1 {
			parts = append(parts, fmt.Sprintf("%s ×%d", e.k, e.n))
		} else {
			parts = append(parts, e.k)
		}
	}
	return strings.Join(parts, ", "), total
}

// turnStateNext is the directive — one rule-derived line that names the
// next action. This is the card's reason to exist: the moment a turn stops
// producing code, the freshest thing in the model's context says so.
func turnStateNext(mode string, scan *turnStateScan, round, maxRounds, unverified int) string {
	// Budget outranks everything: a turn that is about to be cut off needs
	// finishing energy, not scoping energy.
	if maxRounds > 0 && round > maxRounds*3/4 {
		if mode == "plan" {
			return "Round budget nearly spent — write the plan now with what you have; stop reading."
		}
		if scan.writeCalls == 0 {
			return "Round budget nearly spent and nothing has been written — make the edit (or answer) now and run run_checks; no more exploration."
		}
		return "Round budget nearly spent — finish the change in front of you and run run_checks; no new threads of work."
	}
	if mode == "plan" {
		if scan.readCalls >= turnStatePlanReads {
			return fmt.Sprintf("%d read calls this turn — scope is sufficient. Stop reading and write the plan now.", scan.readCalls)
		}
		return "Scope tightly: read only what the change touches, then write the plan."
	}
	switch {
	case scan.writeCalls == 0 && round >= turnStateIdleRound:
		return fmt.Sprintf("No file writes after %d rounds (%d read calls) — stop gathering. "+
			"If this task changes code, make the first edit now; if it is a question, answer it from the transcript.",
			round, scan.readCalls)
	case scan.writeCalls == 0:
		return "Orient from the project map and known-files list, then act — do not re-read files already in your transcript."
	case unverified >= verifyEnforceThreshold:
		return fmt.Sprintf("%d writes since the last passing check — run run_checks now, fix what it reports, then continue.", unverified)
	default:
		return "Continue with the next concrete step; run run_checks before calling the work done."
	}
}

// renderTurnState builds the card text from a scan. Bounded by construction:
// five to seven lines, paths and counts only, never bodies.
func renderTurnState(scan *turnStateScan, round, maxRounds int, mode string, unverified int) string {
	var b strings.Builder
	b.WriteString("## Turn state (auto — system-maintained working memory, not a user message)\n")
	if scan.goal != "" {
		b.WriteString("Goal: " + scan.goal + "\n")
	}

	head := fmt.Sprintf("Round %d/%d", round, maxRounds)
	if reads, n := renderPathCounts(scan.reads, turnStateListMax); n > 0 {
		fmt.Fprintf(&b, "%s · reads: %d (%s)\n", head, n, reads)
	} else {
		b.WriteString(head + " · reads: 0\n")
	}

	writesLine := "writes: 0"
	if writes, n := renderPathCounts(scan.writes, turnStateListMax); n > 0 {
		writesLine = fmt.Sprintf("writes: %d (%s)", n, writes)
	}
	if unverified > 0 {
		writesLine += fmt.Sprintf(" · unverified: %d", unverified)
	}
	b.WriteString(writesLine + "\n")

	if len(scan.checks) > 0 {
		parts := make([]string, 0, len(scan.checks))
		for i, c := range scan.checks {
			if i >= 4 {
				parts = append(parts, fmt.Sprintf("+%d more", len(scan.checks)-4))
				break
			}
			switch c.head {
			case "":
				parts = append(parts, c.tool+" "+c.verdict)
			default:
				parts = append(parts, fmt.Sprintf("%s %s (%s)", c.tool, c.verdict, c.head))
			}
		}
		b.WriteString("Checks: " + strings.Join(parts, " · ") + "\n")
	}
	if scan.lastErr != "" {
		b.WriteString("Last error: " + scan.lastErr + "\n")
	}
	b.WriteString("NEXT: " + turnStateNext(mode, scan, round, maxRounds, unverified))
	return b.String()
}

// appendTurnState renders the card and attaches it to the wire copy of the
// conversation — the one that goes to the provider. `send` is copied before
// the append: TrimSupersededToolOutput can return the caller's slice
// unchanged, and writing the card into a message the stored transcript also
// references would let it leak into persistence and accumulate round over
// round.
//
// The card lands inside the trailing user message (a text block alongside
// the tool_results it follows): both providers accept mixed content in one
// user turn, OpenAI's converter emits the tool messages first so ordering
// holds, and the Anthropic cache breakpoint sits at len-2 — the message
// before this one — so the cached prefix never moves.
func appendTurnState(send, full []ai.Message, turnStart, round, maxRounds int, mode string, sess *SessionState) []ai.Message {
	if !turnStateEnabled() || len(send) == 0 {
		return send
	}
	scan := scanTurnState(full, turnStart)
	card := renderTurnState(scan, round, maxRounds, mode, sess.WritesUnverified())

	out := make([]ai.Message, len(send))
	copy(out, send)
	if n := len(out); n > 0 && out[n-1].Role == "user" {
		last := out[n-1]
		blocks := make([]ai.Block, len(last.Blocks), len(last.Blocks)+1)
		copy(blocks, last.Blocks)
		blocks = append(blocks, ai.Block{Type: "text", Text: card})
		out[n-1] = ai.Message{Role: "user", Blocks: blocks}
	} else {
		out = append(out, ai.UserText(card))
	}
	return out
}

// oneLine collapses newlines and runs of spaces so a multi-line excerpt
// cannot break the card's line structure.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncateRunes shortens s to at most n bytes without splitting a UTF-8
// rune mid-sequence.
func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}
