# workflow

The H10 declarative fan-out engine: a JSON spec the model writes, run over
the subagent service. See `workflow.go` for the full contract.

## What the model sees

- **run_workflow** takes a `spec` object: `{name, phases: [{title,
  parallel: [{prompt, schema}] | prompt}]}`. The tool description embeds a
  worked example.
- **Results** are the final JSON: every phase's results in order. A failed
  child is an explicit JSON `null` in its slot plus a `nulls` count on the
  phase — the run never dies with it.
- **Schemas** are per-item result contracts (`{"pass": "boolean"}`); the
  child is told to answer in exactly that shape. Prose answers wrap as
  `{"report": ...}` — holder-owned, never rejected.
- **Budgets:** the subagent pool bounds concurrent items (default 2); a
  phase with N items runs them concurrently when read-only.
- **Failure shapes:** an invalid spec returns a structured error naming the
  phase and what is wrong; the aggregate phase receives prior results
  embedded in its prompt.
