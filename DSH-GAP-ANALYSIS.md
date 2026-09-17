# Talon ↔ DeepSeek Harness — Engineering-Process Gap Analysis

**Method.** I read the DeepSeek Harness (DSH) distribution installed on this machine
(`node_modules/@deepseek-ai/`, 224 packages) at source level — every in-scope package
README in full, plus the unminified `lib/types/*.d.ts` declaration files and the bundled
`lib/*.js` to verify exact schema field names, event payloads, and policy constants. In
parallel I read Talon's own implementation to the same depth: `internal/harness/*`
(sessionlog, events, compaction, goals, skills, subagent, workflow, ralph, sandbox,
approval, promptsec, pipeline, loop, agent, composition, instructions), `internal/orchestrator`,
`internal/api`, `internal/store`, `internal/ai`, `internal/tools`, `internal/guardrails`,
`internal/knowledge`, `internal/verify`, `internal/metrics`, and the frontend surfaces.

Everything below is quoted from code or docs on this machine, not inferred from memory.

---

## 1. The two mental models, side by side

| | **DSH** | **Talon** |
|---|---|---|
| Unit of work | **turn → step → tool call** (a "round" belongs to the goal domain, never the loop) | **turn → round → tool call** (round is a loop term, budget `AI_MAX_ROUNDS`, default 60) |
| Source of truth | the **event-sourced session log**; message history is *derived*, never stored | same — `internal/harness/sessionlog`, "carried over from the DSH session contract" |
| Composition | plugin registry; capability seams (`ctx.*`); "everything beyond call-the-model-run-the-tools belongs to plugins" | Go packages + `composition` preset layer; closer to a monolith with extracted seams |
| Turn budget | **none** — deliberately deferred to `agent/turn-stopping` | **60 rounds** (env `AI_MAX_ROUNDS`, clamped [1,500]) + **ramble breaker** at 3 tool-less max-tokens rounds |
| Extension surface | waterfalls (`tools/pre-execute`, `agent/pre-step`, …) + hooks + invariant companions | `promptsec` section registry + `pipeline` tool-scheduling + `guardrails` pre-exec decision |

Talon already mirrors DSH's single most important idea — the append-only event log with
derived history and a surface/log event split (`internal/harness/events/events.go`, 17 kinds).
The gaps are in the **layers around the log**, not the log itself.

---

## 2. The mirror map — every DSH subsystem and its Talon equivalent

| DSH package(s) | Talon equivalent | Parity |
|---|---|---|
| `dsh-session` (event log, `deriveMessages`, surface ops) | `harness/sessionlog` + `harness/events` | ✅ **parity** — surface/log split, frozen payloads, shadow mechanism |
| `dsh-session-persistence` + `-jsonl` | `store/sessionlog.go` (SQLite `session_events` rows) | ✅ **parity on the seam**; ⚠️ different crash story (below) |
| `dsh-session-checkpoint-policy` | — | ❌ **MISSING** |
| `dsh-session` `interruptedTurnClosers` | — | ❌ **MISSING** |
| `dsh-compaction-tool-result-pruner` | `harness/compaction.Prune` | ✅ **identical constants** (8192/4096/1024) |
| `dsh-spill` + `-policy` + `-local` | `harness/compaction` spill (`SpillThresholdBytes` 256 KiB) | ✅ **parity in the package** |
| `dsh-compaction-basic` (LLM summarization) | `ai.Compact` + `OnCompacted` shadow | ⚠️ **PARTIAL — static one-line summary, no model-written checkpoint** |
| `dsh-token-meter` | `harness/events` `usage/record` + `metrics` counters | ⚠️ **PARTIAL — no replay fold, no context-pressure measure** |
| `dsh-session-projection` | in-memory derivation cache in `sessionlog.Log` | ⚠️ **PARTIAL — no read-model registry** |
| `dsh-session-projection-cache` | — | ❌ **MISSING** |
| `dsh-session-query` + `-sqlite` | — | ❌ **MISSING** |
| `dsh-goal` | `harness/goals` | ⚠️ **PARTIAL — 3 of 4 phases, no driver** |
| `dsh-goal-round-driver` | — | ❌ **MISSING** |
| `dsh-tool-goal` | — (goals mutated by the loop, not the model) | ❌ **MISSING** |
| `dsh-command-goal` (`/goal …`) | — | ❌ **MISSING** |
| `dsh-tool-todo` | orchestrator `plan:*` tasks + `mark_task_done` | ⚠️ **PARTIAL — tasks are SSE-only, not durable** |
| `dsh-plan-mode` | `ai/routing` `TurnPlan` tier | ⚠️ **different object** — Talon "plan mode" = a build phase, not a read-only guidance mode with reviewed exit |
| `dsh-sandbox` + `-policy` + `-local` | `harness/sandbox` (read-only / workspace-write / danger-full-access) | ✅ **parity on modes**; ⚠️ no escalation ladder |
| `dsh-permission-presets` | `harness/composition` presets | ⚠️ **PARTIAL** |
| `dsh-user-approval` | `harness/approval` | ✅ **parity** — 4 terminals, one-shot, audit pair |
| `dsh-user-questions` | `tools/ask_user.go` | ⚠️ **PARTIAL — no waterfall, no intent tagging** |
| `dsh-authorization` | — | ❌ **MISSING** (no interactive credential flows) |
| `dsh-session-telemetry` + `-otel` | `internal/metrics` (Prometheus counters) | ⚠️ **PARTIAL — no event-level telemetry, no OTel** |
| `dsh-session-stats` | `metrics` (turns/latency/tokens/cost sums) | ⚠️ **PARTIAL** |
| `dsh-message-feedback` + `dsh-command-feedback` | — | ❌ **MISSING** |
| `dsh-session-title` + `-llm` | — | ❌ **MISSING** |
| `dsh-invariants` | — | ❌ **MISSING** |
| `dsh-hook-protocol` + `-claude-code` + `-codex` | — | ❌ **MISSING** |
| `dsh-commands` | `CommandPalette.tsx` (file-jump only) | ❌ **MISSING — not a slash-command system** |
| `dsh-tool-bash` | `tools/run_shell.go`, `run_command` | ✅ parity (fresh shell, `[exit code: N]`) |
| `dsh-tool-fs` | `tools/fs.go` (read/write/edit) | ✅ parity |
| `dsh-fs-observation-policy` | `guardrails` read-before-write + `write-conflict` (H5 CAS) | ✅ **parity, domain-merged** |
| `dsh-tool-fs-search` | `tools/find_files.go`, `search_files.go` | ✅ parity (grep/glob) |
| `dsh-tool-str-replace-editor` | `tools/edit.go` | ✅ parity |
| `dsh-tool-web` | — (no web tools in Talon) | ❌ **MISSING** |
| `dsh-tool-subagent` | `harness/subagent` + `tools/delegate.go` | ⚠️ **PARTIAL — one-shot only** |
| `dsh-tool-subagent-control` | `delegate list_children` only | ⚠️ **PARTIAL — no `send_message`/`interrupt_agent`** |
| `dsh-tool-workflow` | `harness/workflow` + `run_workflow` | ✅ **parity** (pipeline/parallel/phase, schema validation) |
| `dsh-tool-ralph` | `harness/ralph` | ✅ **parity** (fresh rounds, immutable objective, bounded report) |
| `dsh-tool-skill` + `dsh-skill` + `-filesystem` | `harness/skills` + `read_skill` | ⚠️ **PARTIAL — no filesystem watch, no catalog-digest injection** |
| `dsh-agent-instructions` (AGENTS.md chain) | `harness/instructions` (65,536-byte budget) | ✅ **parity** |
| `dsh-llm-retry` | `ai/client.go` `openStream` retries + `routing` fallback model | ⚠️ **PARTIAL — retries not logged** |
| `dsh-tool-call-timeout-policy` | — (per-tool timeouts only) | ❌ **MISSING** |
| `dsh-repeat-tool-reminder` | — | ❌ **MISSING** |
| `dsh-system-prompt` | `harness/promptsec` (11 weighted sections, fingerprinted cache) | ✅ **parity** |
| `dsh-agent-presets` | `harness/composition` (starter/expert/fast) | ✅ parity |
| `dsh-time-context` | — | ❌ **MISSING** (no per-step clock) |

**Score:** of ~45 DSH capability subsystems, Talon mirrors **18 at parity**, **14 partially**, and is **missing 13 outright**.

---

## 3. Tier 1 gaps — the ones that change what the system *is*

These are gaps in **durability and recoverability**. They are the difference between "the
log is the source of truth" and "the log can be trusted after a crash."

### 3.1 No crash-recovery checkpoint policy
**DSH:** `dsh-session-checkpoint-policy` is a zero-config plugin that flushes the durable
log at three semantic boundaries, **fail-closed**:
1. before a model request reaches the adapter (`llm/stream` wrapper),
2. before a top-level tool body produces an external side effect (`tools/execute` wrapper; nested dispatches reuse the outer checkpoint),
3. at each `agent/pre-step` boundary, so the preceding step's response and tool results are stored before the next request is derived.

A checkpoint rejection means the adapter or the tool body **does not run**. Cancellation
landing mid-flush produces the canonical `ABORTED_BEFORE_DISPATCH` rather than entering the body.

**Talon:** nothing. `store.AppendEvent` does a synchronous SQLite insert (so each event is
individually durable — stronger than DSH's 200 ms write-behind batching for per-event
durability), but there is **no ordering discipline** that says "the request must be durable
before dispatch" or "a recorded tool call must be durable before its side effect." A crash
between a recorded `tool/dispatch` and its effect loses the record of intent.

**Why it matters:** without it, "the log is the source of truth" is true only when nothing
goes wrong. This is the single highest-leverage thing to port.

### 3.2 No synthetic crash closers — a crashed turn is lost, not closed
**DSH:** on `load`, `interruptedTurnClosers()` makes a single pass over the tail and
durably appends, in order: `tool/result` for every unanswered call (coded
`TOOL_OUTCOME_UNKNOWN` — "interrupted after it was recorded… retry only if read-only or
idempotent; if it may have side effects, first verify external state or ask the user" — or
`TOOL_NOT_STARTED`), then `step/end`, then `turn/end {reason: 'interrupted'}`. Seqs continue
from `last.seq+1`; timestamps reuse the last real event's time so replay stays deterministic.
Only a never-written torn tail fragment is discarded.

**Talon:** `sessionlog.New` loads persisted rows and re-derives the shadow boundary, but
**performs no repair.** A turn that crashed mid-tool leaves an unanswered `tool/dispatch`;
`TrimToValidHistory` trims ragged ends for *replay safety* but does not *close* the turn
honestly. The `verify` ladder's "SkippedGates" discipline and the orchestrator's crash index
handle *tool* crashes — nothing handles *process* crashes.

### 3.3 LLM retries are invisible to replay
**DSH:** `dsh-llm-retry` appends a non-surface **`llm/retry`** event (retry id, provider,
mode, policy key, failure, scheduled delay) *before* the backoff wait, then
**`llm/retry-started`** immediately before the attempt. Policy: normal mode = 5 retries on
`EMPTY_RESPONSE | RATE_LIMIT | SERVER | TIMEOUT | TRANSPORT`, exponential 500 ms → 10 s with
10 % jitter; always mode = no attempt limit. Nothing reaches the model.

**Talon:** `openStream` (the retry path I added) and `chatAnthropic`/`chatOpenAI` retry
in-memory, logging only to stdout (`chatAnthropic: retry attempt %d/%d`). The session log has
no `llm/retry` kind. A resumed session cannot tell that a transient failure was recovered,
and `metrics.aiTurnErrors` counts a turn that ultimately succeeded the same as one that
failed.

### 3.4 Plan tasks are not durable — the checklist vanishes on reload
**DSH:** `todo_write` snapshots are **`todo/write` events on the session log**; the current
list is the most recent snapshot, last-write-wins on replay, and a `todos` projection serves
the UI whole values. The list survives restarts and reopened sessions by construction.

**Talon:** plan tasks (`plan:screen:<slug>`, `plan:feature:<i>`) live as **SSE `task_update`
frames** emitted through `EmitTaskUpdate` → `writeBuildEvent`. They are upserted in the
frontend store (`AIChat.tsx`, keyed on `id.startsWith("plan:")`). **They are never written
to the session log.** Reload the page mid-build and the checklist is empty until the next
event arrives — the model and the UI disagree about progress. This is a clean, high-value
port: add a `plan/task` event kind and derive the checklist from the log tail.

### 3.5 No cross-session query or search
**DSH:** `dsh-session-query` + `-sqlite` provide `listSessions`, `readSession`,
`filterSessions`/`filterEvents`, `traceSession`/`traceEvent`, and FTS5 phrase search over a
**derived, disposable index that never opens the session store** — reconciled in one
serialized transaction per search, live sessions shadowed via connection-local temp tables.
Literal phrases only (FTS5 syntax inert), ranked by match-span count then document length,
cursor pagination with generation-bound staleness.

**Talon:** nothing. There is no cross-session read service; `sessions.go` lists prompts per
project. Every cold read replays the whole log.

**Why it matters:** this is the substrate for "what did I build last week" and for evals.
Talon has an `internal/evals` package that would sit directly on it.

---

## 4. Tier 2 gaps — observability, goals, and the human surface

### 4.1 Goals: Talon has the state, DSH has the state *plus a driver*
| | **DSH** | **Talon** (`harness/goals`) |
|---|---|---|
| Phases | `active` `paused` `blocked` `complete` | `active` `complete` `blocked` — **no `paused`** |
| Verbs | create/edit/pause/resume/complete/block/clear | create/complete/block (loop-owned) |
| Concurrency | CAS on `{id, revision}`; stale → `GOAL_STALE` | **same** — `ErrStale` is a faithful port |
| Cap | `defaultMaxGoalRounds` 256 | `MaxRounds` per goal |
| Blocking floor | **`blockedAfterConsecutiveRounds: 3`** — a `blocked` call is *mechanically rejected* until 3 consecutive rounds pass; the model judges whether the condition *actually* persisted | no floor |
| Authority | create/edit/pause/resume require a **direct human message** (host-attested `{kind:'user'}` by `Agent.followup()`/`steer()`); subagents cannot mutate goals | loop-owned, no human-attestation gate |
| Continuation | `dsh-goal-round-driver`: reservation-then-admission (reserves `roundsStarted+1`, only an entered `user/message` increments it), `agent/pre-step` race fences *before and after* downstream listeners, `ctx.sessions.flush()` + recheck before queuing, fail-closed teardown | **none — goals are state only** |
| Post-resume | an active goal is **disarmed** at every session-start edge; "the driver never revives work on its own" — only an explicit human `resume` rearms it | undefined |
| UI | `/goal`, `/goal edit …`, goal chip in the chat rail | **no goal UI at all** |

Talon deliberately mirrors DSH's "state, not a scheduler" posture — but DSH then ships the
scheduler as a *separate package*, and Talon does not have that package. If you want Talon
to mirror DSH here, the missing piece is the **round driver** (and the `paused` phase + the
mechanical 3-round blocking floor, which is what stops the model from declaring victory or
giving up too early).

### 4.2 Telemetry: counters vs. an event-level observability seam
**DSH:** `dsh-session-telemetry` defines the capture seam — "the harness's aspect ends at
`emit()`" — with capture modes (`live`/`on-demand`), one projection (`assistant/chunk` ships
only its first per `(turn,step)`, so seq gaps on the wire are routine), a severity pre-map
(`error` for `tool/result.isError`, `turn/end` error reasons, `agent-error`), a redaction
waterfall that ships no rules and withholds a record fail-closed on a throwing listener, and
a **sharing disclosure** vocabulary (`full` / `feedback-only` / `disabled`). The OTel backend
defaults to **DISABLED**, uses `FULL`/`FEEDBACK_ONLY` modes, two instrumentation scopes
(ledger vs ops) so receivers can alert on ops without summing them, and detects crashes by
`shutdown`-record absence. Consent is positive and fail-closed.

**Talon:** `internal/metrics` holds process-lifetime counters (requests, errors, latency,
AI turns/turn-errors/latency, input/output tokens, cost in micro-USD, `verifyFailures`,
exports) rendered as Prometheus text. That is honest operational health — "is the system
healthy right now" — but it is **not** event-level telemetry: no per-turn records, no
redaction, no sharing disclosure, no consent gate, and **no OTel exporter**.

### 4.3 No session titles, no feedback surface
Both are small, self-contained, and directly improve the product:

- **`dsh-session-title`** — three sources newest-wins (deterministic fallback from the first
  eligible human message, one async LLM provider, explicit `rename()`); every revision is a
  log-only `session/title` event; a user-sourced latest title **pins** the session; the LLM
  provider frames selected messages as JSON, measures against `maxInputBytes` *before*
  dispatch, and rejects malformed output, tool calls, and non-stop finish reasons.
- **`dsh-message-feedback`** — per-message ratings + notes in a **storage sidecar outside the
  session log** ("a rating can never be confused with conversation content"), optimistic
  per-message CAS, durability barrier (flush + re-read prefix before the sidecar write).
- **`dsh-command-feedback`** — `/feedback <text>` appends one `feedback/record`, acknowledges
  with the sharing-disclosure sentence.

Talon has neither; a grep for goal/title/feedback UI surfaces returns nothing.

### 4.4 Compaction: Talon truncates, DSH summarizes
Both trigger on token pressure. The difference is **what survives the cut.**

**DSH `dsh-compaction-basic`:** `thresholdRatio: 0.8` of the routed context window; three
escalating arms — model-free **tool-result pruning** (8192/4096/1024 — identical to Talon's
constants), **spill to disk** (private `0700` dir, exclusive `open('wx')`, injective path
segment encoding), and **LLM summarization** — where the summarizer replays the
conversation's own system prompt, tools, and shadowed messages **byte-for-byte** so the
auxiliary call reuses the provider's warm prefix cache, and the replacement is one
`user/message` with an 8-section checkpoint (Primary Request and Intent / Key Technical
Concepts / Files and Code / Errors and Fixes / Pending Jobs / Current Work / Next Step /
Critical Context). The lock is a **log-recorded bracket**: `compaction/start` before
summarization yields, `compaction/end` after the single surface mutation; a failed close
leaves the unmatched start as the intentional busy signal. A summary that does not shrink its
source is rejected. If nothing can be compacted safely, **nothing is written to the log.**

**Talon `ai.Compact`:** triggers above `maxContextTokens`, compacts to `3/4`, keeps the
first turn, walks backwards to a clean boundary (never orphaning a tool_use from its
result — the same invariant), and prepends the static line:

> `[Earlier context compacted: N turns removed to stay within the context window. The files already written are on disk — use read_file if you need to see them.]`

The `OnCompacted` hook then shadows the session log with a matching one-liner. The mechanism
is wired and honest; **the summary carries no semantics.** Every reason the model was working,
every error it hit, every decision it made is gone — only "the files are on disk" survives.
The dropped turns' *content* must be re-derived from files by a model that no longer knows
why it wrote them. This is the compaction gap that costs the most in practice, and DSH's
8-section checkpoint format is a proven template (the preamble at the top of this very
conversation is one).

**Also missing:** `dsh-token-meter` — the replay-aware fold that *measures* pressure. Talon
estimates tokens (`ai.EstimateTokens`) only to decide when to compact; there is no
`contextPressure`/`contextBreakdown`/`tokenUsage` projection and no shared accounting that a
UI occupancy bar could read.

### 4.5 Subagents: one-shot vs. continuable
**DSH:** two child shapes — one-shot, and **continuable** (durable session + one
process-local Activation; `startContinuable()` resolves at inbox acceptance and the child
owns its own turns). Backends: `spawn`, `fork` (seed = parent's balanced completed-turn
prefix up to and including the last `turn/end`; the in-flight turn is **never** included;
shipped fork tools cannot select a child route so the copied history stays KV-reuse-
eligible), plus out-of-process. Control tools: `send_message` (returns only acceptance, never
a reply), `interrupt_agent` (stops only the current turn; queued messages stay parked),
`list_agents` (running/idle/ready; one-shot children intentionally absent). Isolation: a
child joins the parent's composition but the seed carries **history only — fresh tool scope,
none of the parent's authority** — and every child's runtime context carries the
`subagent:delegation` statement.

**Talon `harness/subagent`:** one-shot `delegate` only, **pool 2**, **depth 1**
(`ErrDeep`/`ErrPoolFull`/`ErrCooldown`), 60 s post-429 cooldown, `list_children` for
discovery, `BEGIN CHILD REPORT` framing so the parent treats child output as data. The
pool/depth/cooldown discipline is a faithful adaptation; the gap is **continuable children
and the control tools** — Talon cannot steer a running child, and a child cannot ask its
parent.

---

## 5. Tier 3 gaps — extension model and guardrails

### 5.1 No invariant companions
**DSH `dsh-invariants`:** any package ships a `./invariant` companion that verifies *its own
durable relationships* at runtime; failure throws `InvariantError` with the stable code
`INVARIANT` and the full package name. The registry is product-independent, and the rule is
explicit: **"Real relationships, not synthetic assertions"** — confirming a method or a fixed
pure result is a unit-test concern, never a runtime invariant. Companions exist for ~30
packages (goal folds, approval asked/decided pairing, todo shape, sandbox mode values, plan
payload, hook pairing). Wiring is mechanically enforced by `pnpm run verify-package-invariants`.

**Talon:** none. There is no runtime invariant registry. The closest thing is
`TestPipelineTranscriptParity` — a *test* invariant, exactly as DSH's rule would say.

### 5.2 No hooks bridge, no slash-command grammar
**DSH:** `dsh-hook-protocol` runs command hooks only (`http`/`mcp_tool`/`prompt`/`agent` are
skipped with a warning); exit code 2 blocks, anything else is a non-blocking failure; merge
precedence `deny > ask > allow`; every step degrades to a contained outcome so a hook can
never crash a turn. `dsh-commands` gives slash commands a grammar (`command/run` before the
handler, `command/done` at settlement, both log-only), agent-scoped shadowing, and image
attachments on create.

**Talon:** `CommandPalette.tsx` is a subsequence-matching **file jump** palette (capped at
40 results) — not a command system. No hooks. Talon's `guardrails` package is a *better*
pre-execution gate than DSH's for this domain (below), but it is not externally extensible.

### 5.3 Missing guardrails that DSH has
- **`dsh-tool-call-timeout-policy`** — zero-config, cooperative-only; reads each tool's
  declared `timeoutMs`; signal-swap-then-restore so `tools/post-execute` never sees the
  aborted signal; `Error: tool call timed out after <ms>ms` / `TOOL_TIMEOUT`; nested outer
  deadlines read as ordinary upstream cancel. **There is no registry-wide default** — and the
  shipped `bash`/`read`/`write`/`edit` declare none. Talon has per-tool timeouts but no
  uniform cooperative-cancel discipline with signal restoration.
- **`dsh-repeat-tool-reminder`** — advisory loop hygiene: `thresholds: [3, 5, 8]`, chain keyed
  by `(tool, deep-sorted canonical args)` in a `WeakMap<Agent,Chain>`; **untracked tools are
  transparent** (neither increment nor reset — `grep X → todo_write → grep X` still counts);
  a user prompt clears the chain; delivered via `additionalContexts`, never as a content
  replacement; **past the highest threshold it goes silent**. Talon has nothing — its only
  loop-hygiene guard is the ramble breaker (tool-*less* rounds), which catches a different
  failure mode.
- **Sandbox escalation ladder** — DSH's denial marker
  `[sandbox: file access denied under <mode> mode]` carries a sanctioned one-shot retry path:
  the model retries the exact call once with `sandbox_permissions` (the narrowest wider mode)
  + one-sentence `justification`; the approval prompt *is* the consent; speculative or
  non-widening requests fail closed; a rejected escalation is final. Talon's `harness/sandbox`
  refuses with `SANDBOX: <reason>` and `SANDBOX_UNAVAILABLE` fail-closed — but there is **no
  escalation route**, so a legitimate write under read-only just ends.
- **`dsh-user-questions`** — the ask-waterfall with `AskUserQuestionIntent` (`plan-review`),
  `custom` answer override, and the rule "**Durable lineage is not authority**": a resumed
  session may ask, a live child is rejected with `DELEGATED_CALLER`. Talon's `ask_user` is a
  single-channel pause with no intent tagging and no delegation gate.

### 5.4 Skills: catalog discipline
**DSH:** filesystem roots by rank (100 `.dsh/skills` → 600 bundled, one-level discovery,
strict boolean frontmatter that drops a whole skill on a bad value), **no TTL** —
invalidation is provider-driven via `invalidate()`, **definitions are never cached** so body
edits need no versioning, and the catalog is injected at `agent/pre-step` by digesting **the
published entries, not the rendered prose** (so reframing can never force a republish), with
an empty envelope that explicitly retires older names. A user's `/name` gesture is the sole
entry point for `disable-model-invocation` skills and injects *after* every other injection.

**Talon `harness/skills`:** embedded + per-project workspace skills, embedded wins name
clashes, one-line-per-skill manifest, pay-per-read bodies, workspace skills skipped silently
when malformed, fingerprinted manifest section. That is the right shape — the gaps are
**no filesystem watch** (a new skill file needs a restart) and **no catalog-digest
injection** (the manifest is re-rendered rather than diffed by entry digest).

---

## 6. Where Talon is *ahead* — the comparison has to be fair

These are things DSH explicitly defers or lacks that Talon has, and they should not be
discarded in the mirror exercise:

| Talon capability | DSH equivalent |
|---|---|
| **Verify ladder** — 7 ordered gates (`typecheck` → `lint` → `bundle` → `gradle` → `boot` → `vision` → `interact`), stops at the first real failure, each G4 gate returns `passed=true` with an "unavailable" summary when its infra is missing so the ladder is safe to run on every repair, and `SkippedGates` lists exactly which checks never ran | **none** — DSH is a generic harness with no compile/lint/build gates |
| **Failure index** — 17 normalized `(tool, error) → fix` patterns (`NormalizeError` collapses hashes/paths/numbers to placeholders so one signature matches across files and runs), with `CooldownRounds` suppressing re-injection, plus `record_failure_pattern` for runtime additions | **none** — DSH surfaces failures only as provider error codes |
| **Domain guardrails** — deterministic pre-execution decisions: `browser-wallet-package`, `secret-key-in-code`, `seed-phrase-in-code` (Solana-specific, matched against precise API/module shapes), plus `read-before-write` and the **`write-conflict` H5 version-stamp CAS** | `dsh-fs-observation-policy` covers read-before-write/`FS_STALE_VERSION`; the *domain* content rules do not exist in DSH |
| **Round budget + ramble breaker** — `AI_MAX_ROUNDS` (60, clamped) and 3 consecutive tool-less max-tokens rounds ends the turn as `Stalled` | DSH **deliberately has no turn budget** — "a policy that bounds runaway turns must cancel from `agent/turn-stopping`" |
| **Self-healing repair loop** — up to 3 repair iterations with per-gate feedback prose | none |
| **Atomic file staging** — `PatchBuffer.Stage` + promote-after-checks-pass + `checkpoint` (file snapshots, pruned to 30) for undo | none (DSH's checkpoint is crash-recovery, a different object) |
| **Vision verification** — `verify.Analyze` decodes a screenshot and reports `BlankFraction`/`Blank` above a 0.97 threshold, failing on capture health rather than misreading pixels | none |
| **Live device pipeline** — Metro per-session, adb reverse, frame-diff streaming, touch input, APK build | out of DSH's scope entirely |

The honest read: **Talon is the domain product; DSH is the harness kernel.** Talon should
mirror DSH's *process infrastructure* (durability, observability, extension, recovery) and
keep its domain machinery, which DSH does not have and should not acquire.

---

## 7. What to build, in priority order

Ordered by (value to the product) ÷ (porting cost), from the analysis above.

**Status legend:** ✅ shipped · 🔶 found already present on audit · ⬜ remaining

**P0 — recoverability (the log becomes trustworthy)**
1. ✅ **Crash closers** in `harness/sessionlog` — `sessionlog.Repair()` closes an
   interrupted final turn on load: a synthetic `tool/result` for every unanswered
   call (unknown outcome vs never started, each with the rule for deciding
   whether a retry is safe), then a `turn/finished` marked `interrupted`. The
   pre-crash events are preserved byte for byte.
2. ✅ **Checkpoint policy** — the assistant turn is flushed the moment it is
   appended (before, `executeTools` ran the side effect *before* the
   `tool/dispatch` record was written), and two fail-closed barriers force the
   log to stable storage via `wal_checkpoint(TRUNCATE)` — before each model
   request and once before a round's tool fork. A barrier that cannot be
   satisfied refuses to run the round.
3. ✅ **Durable retries** — `llm/retry` event appended by the retry hook before
   every backoff; carries attempt, failure class, detail, backoff, model.
4. ✅ **Durable plan tasks** — the `plan_task_status` backlog was already being
   written; the read path was missing. `GetConversation` now returns it and the
   frontend seeds the checklist on restore, so it survives a reload.

**P1 — context economy**
5. ✅ **Model-written compaction summary** — `CompactWithSummary` makes one
   auxiliary call over the dropped span producing DSH's 8-section checkpoint;
   reuses the conversation prefix for cache warmth, shape-gated, degrades to the
   static line.
6. ✅ **Token meter** — every model call appends `usage/record`; `Log.TokenUsage`
   folds cumulative spend (ignoring the shadow — spent is spent) and
   `Log.ContextPressure` reads the replayed conversation against the exported
   compaction window. One accounting for the rail, a future occupancy bar, and
   the compactor.

**P1 — observability and the human surface**
7. 🔶 **Session titles** — already shipped: synchronous truncated-prompt fallback
   at creation, one bounded LLM call after the turn, hard byte cap, rename pin.
8. ⬜ **Event-level telemetry with an OTel exporter**, defaulting to disabled, with the sharing-
   disclosure vocabulary and a redaction seam that ships no rules. Talon's Prometheus
   counters stay as the ops health path.
9. ⬜ **Message feedback sidecar** outside the session log + `/feedback`.

**P2 — goals and delegation**
10. ✅ **Goal round driver** — the round driver and CAS were already present; the
    `paused` phase and the `blockedAfterConsecutiveRounds: 3` floor are now
    shipped, with stall accounting that resets on an advancing round.
11. ⬜ **Continuable subagents** + `send_message` / `interrupt_agent`, keeping Talon's pool-2 /
    depth-1 / cooldown discipline.
12. ✅ **Sandbox escalation ladder** — `Capability.Escalate` runs a refused
    operation under a strictly wider mode through the existing approval seam:
    strictly-wider-only (a lattice, never equal), one-shot (the capability's own
    mode is untouched), justified-and-audited. Re-checked under the wider mode
    before the human is asked; nil seam fails closed.

**P2 — extension model**
13. ⬜ **Invariant companions** — a product-independent registry where each package checks its
    own durable relationships at runtime; follow DSH's rule strictly (real relationships,
    never synthetic assertions — that keeps it from becoming a test-suite-in-production).
14. 🔶 **Repeat-tool reminder** — already shipped: `[3,5,8]` thresholds, per-turn
    counters, resets at turn start, advisory note appended to the result.
15. ✅ **Tool-call timeout policy** — `harness/tooltimeout` maps a tool to its own
    budget with no registry-wide default (a read answers in milliseconds, a
    device build in minutes; one number kills real work or bounds nothing). The
    timeout is cooperative — the derived context's deadline is the signal — and
    a call that reaches it is reported as an error naming the budget, never a
    truncated success. A parent deadline wins over the tool's own, so a nested
    cancel reads as upstream.

**P3 — search and reach**
16. ⬜ **Session query + FTS5** — derived disposable index, never opening the session store;
    literal-phrase queries, reconciliation in one transaction per search. Substrate for the
    `evals` package and for project history.
17. ⬜ **Slash-command grammar** + **filesystem skill watch + catalog-digest injection** +
    **`dsh-time-context`**-style per-step clock.

---

## 8. The one-sentence summary

Talon already carries DSH's central idea — an append-only session log whose message history
is derived, with a surface/audit event split — and it carries the domain machinery DSH
deliberately does not have. What it is missing is **the durability discipline around the log**
(checkpoints, crash closers, durable retries, durable plan tasks), **the read and
observability layers on top of it** (projections, query/FTS, telemetry, titles, feedback), and
**the extension model** (invariant companions, hooks, escalation, loop-hygiene reminders).
Port P0 first: four small, self-contained changes that turn "the log is the source of truth"
from a design statement into a post-crash guarantee.
