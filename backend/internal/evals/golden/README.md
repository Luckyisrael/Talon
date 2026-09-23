# golden — W13 recorded-provider replay harness

Agent behavior as a reviewable diff. Each task fixture under `tasks/`
replays a scripted prompt through the **real** orchestrator loop — real
registry, real guard stage, real hooks seam, real FS — against a local
recorded provider. Free, offline, CI-able.

## Layout

| file | role |
| --- | --- |
| `tasks/<id>.json` | fixture: history, seed workspace, recorded script, pass criteria |
| `tasks/<id>.golden.json` | recorded transcript: outcome, rounds, tool sequence, files touched, request digests |
| `golden.go` | harness: provider, replay, criteria, diff |
| `golden_test.go` | the eval gate: determinism, budget, mutation |

## The three criteria channels

- **the log** — `expect.tools` (tool-order classes, names in sequence),
  `expect.outcome`, `expect.max_rounds` (the rounds-to-green budget),
  `expect.files_touched`;
- **the workspace diff** — `expect.content` markers over the final
  snapshot; a `!` prefix asserts absence (how the steer task proves it
  dropped what it was steered away from);
- **the grade scorecard** — `expect.eval` runs the same `evals.Grade`
  the build evals use (MWA, forbidden markers, router structure,
  expected files). `plan-existing-feature` deliberately has no grade
  channel: it writes nothing, and `build-finished` would honestly fail
  a build that produced no code — its criteria are log + diff.

Never prose: transcripts pin invariants only, so a golden diff in a PR
reads as behavior, not as a wall of model text.

## What the model sees

A replayed model sees exactly what production sees: the production
system prompt (`ai.SystemPromptBuild` or `ai.PlanSystemPrompt`),
enriched by the loop's own context sections, the four native tools
(`read_file`, `write_file`, `edit_file`, `list_files`), the guard
stage's refusals, and the fixture history. It does **not** see the
ladder's checkers: the seeded `.talon/config.json` declares every gate
off (an explicit empty ladder — a gate that never ran is never
claimed), so verification classifies strictly over zero skips.

## Why digests make prompt/tool/guardrail edits require review

Every provider request is digested as sha256 over its raw body — the
`(system, messages, tools)` triple — with the temp workspace root
normalized out. The golden stores those digests. Change
`ai.SystemPromptBuild`, a tool schema, a guard refusal, or the loop's
request shape and the digests stop matching: the suite goes red until
the goldens are re-recorded and the transcript diff is reviewed in the
PR.

## Record workflow

```sh
cd backend
TALONSTUDIO_UPDATE_GOLDEN=1 go test ./internal/evals/golden/ -run TestGoldenSuite
git diff internal/evals/golden/tasks/   # review the transcript diff
```

CI never sets the flag, so the workflow tree guard
(`git diff --exit-code`) proves the suite never rewrites its own
goldens.
