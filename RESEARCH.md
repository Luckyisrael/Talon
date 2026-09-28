
I’d phrase the core issue this way:

> The system gives the model enough context to over-prepare, but not enough constraints to deliver a vertical slice early.

An LLM will often choose “understand everything first” if the prompt makes that appear safest.

## The two biggest problems

### 1. Full tool arguments are being replayed forever

This is your largest context leak:

- Tool **results** are capped and compacted.
- But the assistant’s original `tool_use` arguments still contain the complete file contents.
- Those assistant messages are replayed every round.
- Therefore, every `write_file` or large `multi_edit` permanently increases the input context.

The model no longer needs most of those full payloads. It can recover them from the workspace.

Render old write calls as compact artifact references instead:

```text
write_file app/profile.tsx
Result: wrote 142 lines
Artifact: artifact://turn-12/app/profile.tsx
SHA256: 91f3...
```

Only inject the full content again when the current step explicitly needs it.

Treat write payloads as **artifacts**, not conversational history. This is likely a bigger win than reducing your 8,529-token base prompt.

---

### 2. The model is optimizing for completeness instead of a first visible milestone

These behaviors are symptoms:

- Infrastructure before screens
- Type archaeology
- Full-file generations
- Growing request scope
- First screen at minute 46

Your plan currently describes *what* will be built, but it apparently does not force an execution order that produces user-visible value early.

The plan should be a dependency-ordered graph of vertical slices:

```json
{
  "tasks": [
    {
      "id": "screen-home",
      "type": "screen",
      "files": ["app/(tabs)/index.tsx"],
      "depends_on": ["theme"],
      "acceptance": [
        "Route renders",
        "No TypeScript errors introduced",
        "Primary UI elements are visible"
      ]
    },
    {
      "id": "feature-profile-edit",
      "type": "feature",
      "depends_on": ["screen-profile"],
      "acceptance": [
        "User can update profile",
        "State persists after reload"
      ]
    }
  ]
}
```

Then give the model a strict slice contract:

> Build the first screen with only the infrastructure directly required to render it. Do not create shared abstractions before they have a consumer.

For example, the first home-screen slice should not require a fully developed API layer, generic form engine, complete type hierarchy, or global state architecture unless the screen directly uses them.

## Rewriting “read-before-write”

The current wording may be interpreted too broadly. It should mean narrow dependency reading, not codebase archaeology.

Replace:

> Read before writing.

With something like:

> Before modifying a file, inspect that file and its direct local dependencies. Do not perform broad repository exploration unless the target file, import, or required convention is unknown. Do not read shared infrastructure until the current vertical slice references it.

That distinction matters.

## Compress the always-on context

Your base system prompt is 8.5k tokens, but the effective prompt is much larger:

- Tool definitions
- Rules
- Skills catalog
- AGENTS.md
- Template digest
- Project map
- Known files
- Full checklist
- ADRs
- Memory

Even if enrichment is triggered intelligently, too much of this appears to be always-on.

Use layers:

### Always include

- Core behavioral contract
- Safety/workspace constraints
- Current task state
- Relevant tool definitions
- Current slice checklist entry

### Retrieve when relevant

- ADRs
- Skill bodies
- Domain documentation
- Project memory
- Architecture decisions
- Existing implementation patterns

### Compress heavily

- Full plan checklist
- Known files
- Project map

For example, instead of re-appending all 18 checklist items every round:

```text
Plan progress: 4 done, 1 in progress, 13 pending.
Current slice: screen-profile
Depends on: theme, user-store
Acceptance:
- Route renders
- Profile fields load
- Save action updates state
```

The full checklist can remain stored and retrievable without consuming prompt space every round.

## Fix plan-state detection soon

The broken auto-marking heuristic is not a minor UX issue. It means the orchestrator and model lack a trustworthy shared picture of progress.

A task should transition based on explicit evidence:

- Planned files exist
- Required route/component is registered
- Relevant check passes
- Acceptance criteria are satisfied

Avoid relying only on filenames and compile state. Use the planned file manifest plus verification result:

```text
screen-profile:
  expected_files: ["app/profile.tsx", "components/ProfileCard.tsx"]
  required_symbols: ["ProfileScreen", "ProfileCard"]
  required_checks: ["tsc"]
```

If expected files exist and checks pass, mark it completed or propose completion for model confirmation.

## Make verification staged

Verification is essential, but running broad checks over an intentionally incomplete generated tree can create a cascade of irrelevant errors. That can push the model into more archaeology.

Use staged gates:

1. **Per-file syntax/local concerns**
2. **After each vertical slice:** TypeScript and lint
3. **After navigation/state integration:** app-level checks
4. **Near completion:** bundle or native build

Also snapshot baseline diagnostics before edits:

- Existing TypeScript errors
- Existing lint warnings
- Existing build failures

Then distinguish:

- Pre-existing failures
- Failures introduced by this turn

Otherwise the agent may try to repair problems outside its current task.

## Add a first-artifact execution budget

Your 60-round limit protects against infinite loops, but it is too coarse. Add phase-specific budgets.

For example:

```yaml
exploration:
  max_tool_calls_before_first_artifact: 10
  max_rounds_before_first_artifact: 4

implementation:
  max_rounds_per_vertical_slice: 8

verification:
  max_recovery_attempts_per_check: 2
```

The exact numbers should come from traces, but the principle is important:

> The agent cannot spend unlimited rounds preparing before creating the first required artifact.

You can still permit recovery when the model becomes genuinely blocked.

## Instrument the real bottleneck

You already log useful deltas, but I would add absolute per-round attribution:

```text
Round 14
Input tokens:
  system prompt:        11,430
  tool definitions:      6,180
  template digest:       2,940
  rules/skills/ADRs:     7,650
  project map:           1,120
  checklist:             2,300
  conversation history: 41,780
  prior write payloads:  22,460
  current tool results:   3,110
Total:                   98,970

Elapsed:
  prompt assembly:      120ms
  model generation:     84s
  tool execution:       31s
  persistence/events:    8s
```

Without this breakdown, it is easy to optimize the base prompt when the real cost is historical write payloads or repeated context layers.

## One architectural concern: shared `node_modules`

Symlinking every workspace’s `node_modules` into the shared template is risky:

- Installing a package for one project mutates the shared template.
- Different projects can unexpectedly affect each other.
- Native builds may modify dependencies.
- Your cached template analysis may become stale.
- Workspace-only write protections may either block writes through the symlink or accidentally allow writes into the shared template.

Prefer:

- Workspace-local installs, or
- A content-addressed package store such as pnpm’s model, with isolated per-workspace resolution

Shared dependency storage is fine; shared mutable resolution is the risky part.

## Suggested priority order

### P0

1. **Artifactize old write/edit arguments** so full file bodies stop replaying every round.
2. **Add a vertical-slice execution contract** with dependencies and acceptance criteria.
3. **Rewrite the read-before-write policy to require narrow reads only.**
4. **Add per-layer prompt token telemetry.**

### P1

5. Replace the always-appended full checklist with compact current-slice state.
6. Fix automatic task-progress detection.
7. Introduce staged verification and baseline diagnostic comparison.
8. Add first-artifact round/tool-call budgets.

### P2

9. Stream tool-argument deltas to remove the 191-second silent gap.
10. Deduplicate SSE tool-progress accounting.
11. Reduce or dynamically load specialist tool definitions.

Your foundation is good: persistence, bounded tool execution, verification, planning, and eventing are already in place. The main shift is from **always carrying the project in the prompt** to **carrying the current slice and retrieving the rest on demand**.