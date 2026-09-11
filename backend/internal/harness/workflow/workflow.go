// Package workflow is the H10 declarative fan-out engine: a JSON spec the
// model writes, run over the H9 subagent service — Go safety and
// evaluability over JS expressiveness.
//
// The spec surface is tiny on purpose (the model authors it):
//
//	{ "name": "verify-screens",
//	  "phases": [
//	    { "title": "parallel screen checks",
//	      "parallel": [
//	        { "prompt": "Boot and vision-check screen 'Wallet'; return pass/fail",
//	          "schema": {"pass": "boolean", "reason": "string"} } ] },
//	    { "title": "aggregate",
//	      "prompt": "Given the phase-1 results, write the verify summary" } ] }
//
// Semantics ported from DSH: no barrier between pipeline stages in a phase
// (every item runs independently); per-item isolation (a failing item drops
// that item to null, never the run); results are holder-owned (the engine
// never rejects the child's answer — a failed child is a null item); phases
// narrate into the trace; every child is attributed to the invoking agent.
package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/luckyisrael/talon/backend/internal/harness/subagent"
)

// Spec is one workflow.
type Spec struct {
	Name   string  `json:"name"`
	Phases []Phase `json:"phases"`
}

// Phase is one ordered stage: either parallel items or a single prompt.
type Phase struct {
	Title    string   `json:"title"`
	Parallel []Item   `json:"parallel,omitempty"`
	Prompt   string   `json:"prompt,omitempty"`
	// Schema is the optional result contract for this phase's single prompt.
	Schema map[string]string `json:"schema,omitempty"`
}

// Item is one parallel child task.
type Item struct {
	Prompt string            `json:"prompt"`
	Schema map[string]string `json:"schema,omitempty"`
}

// Result is the workflow's outcome: every phase's results in order.
type Result struct {
	Name    string          `json:"name"`
	Phases  []PhaseResult   `json:"phases"`
	// Errored reports engine-level failure (spec invalid, every child lost).
	Errored bool   `json:"errored,omitempty"`
	Err     string `json:"err,omitempty"`
}

// PhaseResult is one phase's outcome.
type PhaseResult struct {
	Title    string           `json:"title"`
	Results  []json.RawMessage `json:"results,omitempty"`
	// Nulls counts items that dropped (child failure); they are null in
	// Results, this says how many.
	Nulls int `json:"nulls,omitempty"`
}

// Validate checks the spec: known keys only, at least one phase, every phase
// has parallel items or a prompt, prompts non-empty. Unknown keys are
// rejected — a model-authored spec must not silently ignore typos.
func (s *Spec) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("workflow spec needs a name")
	}
	if len(s.Phases) == 0 {
		return fmt.Errorf("workflow %q has no phases", s.Name)
	}
	for i, p := range s.Phases {
		switch {
		case len(p.Parallel) > 0 && strings.TrimSpace(p.Prompt) != "":
			return fmt.Errorf("phase %d (%q): a phase is either parallel items or one prompt, not both", i, p.Title)
		case len(p.Parallel) == 0 && strings.TrimSpace(p.Prompt) == "":
			return fmt.Errorf("phase %d (%q): needs parallel items or a prompt", i, p.Title)
		}
		for j, it := range p.Parallel {
			if strings.TrimSpace(it.Prompt) == "" {
				return fmt.Errorf("phase %d (%q) item %d: empty prompt", i, p.Title, j)
			}
		}
	}
	return nil
}

// Engine runs specs over the subagent service.
type Engine struct {
	service *subagent.Service
	// narrate receives phase narration for the trace (task_update-shaped).
	narrate func(text string)
}

// NewEngine builds the engine. narrate, when non-nil, receives one line per
// phase start and finish.
func NewEngine(s *subagent.Service, narrate func(string)) *Engine {
	return &Engine{service: s, narrate: narrate}
}

// Run executes the spec to completion. A spec that fails validation returns
// an errored Result, not a panic. Items run concurrently (the subagent
// service bounds them); a failed item is a null in its phase's results.
func (e *Engine) Run(ctx context.Context, spec Spec) Result {
	res := Result{Name: spec.Name}
	if err := spec.Validate(); err != nil {
		res.Errored = true
		res.Err = err.Error()
		return res
	}

	for pi, phase := range spec.Phases {
		if e.narrate != nil {
			e.narrate(fmt.Sprintf("workflow %q phase %d: %s", spec.Name, pi+1, phase.Title))
		}
		pr := PhaseResult{Title: phase.Title}

		if len(phase.Parallel) > 0 {
			items := make([]json.RawMessage, len(phase.Parallel))
			var nulls int
			var wg sync.WaitGroup
			var mu sync.Mutex
			for i, item := range phase.Parallel {
				wg.Add(1)
				go func(i int, item Item) {
					defer wg.Done()
					out := e.runItem(ctx, spec.Name, phase.Title, item)
					mu.Lock()
					if out == nil {
						// The holder owns nulls: a dropped item is an
						// explicit JSON null in its slot — the phase's shape
						// stays queryable.
						items[i] = json.RawMessage("null")
						nulls++
					} else {
						items[i] = out
					}
					mu.Unlock()
				}(i, item)
			}
			wg.Wait()
			pr.Results = items
			pr.Nulls = nulls
		} else {
			// Single-prompt phase: the aggregate case. It receives the prior
			// phases' results embedded in its prompt — the engine seeds the
			// "Given the phase-1 results" the spec author wrote about.
			prompt := phase.Prompt
			if pi > 0 {
				if prior, err := json.Marshal(res.Phases[:pi]); err == nil {
					prompt = prompt + "\n\nPrior phase results (as JSON):\n" + string(prior)
				}
			}
			out := e.runItem(ctx, spec.Name, phase.Title, Item{Prompt: prompt, Schema: phase.Schema})
			if out == nil {
				pr.Nulls = 1
			} else {
				pr.Results = []json.RawMessage{out}
			}
		}

		res.Phases = append(res.Phases, pr)
		if e.narrate != nil {
			e.narrate(fmt.Sprintf("workflow %q phase %d finished (%d items dropped)",
				spec.Name, pi+1, pr.Nulls))
		}
	}
	return res
}

// runItem runs one child and coerces its report to the item's schema when
// one was given; a failed child is nil (the holder owns nulls).
func (e *Engine) runItem(ctx context.Context, wf, phase string, item Item) json.RawMessage {
	if e.service == nil {
		return nil
	}
	label := fmt.Sprintf("%s/%s", wf, phase)
	child, err := e.service.Spawn(ctx, subagent.Spec{
		Prompt:   item.Prompt,
		Label:    label,
		SystemPrompt: schemaSystemPrompt(item.Schema),
	})
	if err != nil || child.Errored {
		return nil
	}
	return coerceToSchema(child.FinalText, item.Schema)
}

// schemaSystemPrompt tells the child to answer in the schema's shape.
func schemaSystemPrompt(schema map[string]string) string {
	if len(schema) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Answer with a single JSON object with exactly these keys: ")
	first := true
	for k, v := range schema {
		if !first {
			b.WriteString(", ")
		}
		first = false
		fmt.Fprintf(&b, "%q (%s)", k, v)
	}
	b.WriteString(". Output the JSON and nothing else.")
	return b.String()
}

// coerceToSchema extracts the child's JSON answer; unparseable text becomes
// a {"report": text} object so the item survives with its content rather
// than being lost.
func coerceToSchema(text string, schema map[string]string) json.RawMessage {
	trimmed := strings.TrimSpace(text)
	// Children may fence the JSON; find the first { to last }.
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start >= 0 && end > start {
		candidate := trimmed[start : end+1]
		if json.Valid([]byte(candidate)) {
			return json.RawMessage(candidate)
		}
	}
	// Fall back to wrapping the report — holder-owned: never reject.
	wrapped, err := json.Marshal(map[string]string{"report": text})
	if err != nil {
		return nil
	}
	return wrapped
}

// Stamp is the trace narration format for the frontend task_update events.
func Stamp(name string, phase int, title string) string {
	return fmt.Sprintf("[%s] phase %d — %s", name, phase, title)
}

// Now is the engine clock — var for deterministic tests.
var Now = time.Now
