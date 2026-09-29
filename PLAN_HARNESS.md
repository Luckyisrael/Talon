# Talon — Harness Overhaul Plan (H-series)

**Status:** EXECUTED — all 15 phases (H1–H14 + the §7 IDE refit) landed and green.
Each phase shipped behind its flag, eval-gated in `make evals`
(TestPhaseH1…TestPhaseH14), committed separately; no regressions.

> **Errata — 2026-09-22 (W-series audit, `PLAN_WORLDCLASS.md` §1.2):** the "all
> green" claim above was false at audit time. `go vet` was red — copylock in
> `internal/ai/client.go` (`WithModel`/`WithRetryHook` struct-copied the health
> mutex) — fixed in **W0** with `TestPhaseW0GateGreen` keeping it fixed. Several
> phase *laws* also landed dark rather than flipped: `TrimConversation` still
> deletes instead of shadowing (H1), no `read_spill` tool (H8), the approval Seam
> has no callers (H4), and `SESSIONLOG`/`SUBAGENTS`/`PIPELINE` remain unset while
> `harness/{composition,settings,ralph}` are unwired (H9–H12 tail). The phase
> *machinery* did land and its tests are green; see `PLAN_WORLDCLASS.md` §5 for
> the item-by-item register of what remains.
**Sources of truth:** this document, distilled from the DeepSeek Harness (DSH) architecture
(its published package contracts, the base-bundle composition, and the agent-loop/session
design) mapped against this repository's actual current state (backend, frontend, infra,
evals — all read in full before this plan was written).
**Mode:** plan only; no edits made.

---

## 0. What DSH actually is (the research, compressed)

DSH is 224 small TypeScript packages composed at boot from a declarative wiring file
(`cordis.patch.yml`). Nothing is hard-wired: every capability is a package that registers
against a shared context (`ctx.*`) and communicates through one event taxonomy. Ten
principles make it work — each one is portable to Go without porting the framework:

1. **Everything is an event-sourced log; everything else is derived.** The session log
   is append-only and the LLM message history is *derived* from it (`deriveMessages()`),
   never stored. Replay is re-derivation. Compaction *shadows* older surface entries
   without deleting history, so replaying a session reproduces the exact conversation.
2. **Contracts are separate from implementations.** `dsh-agent` defines the Agent
   interface; `dsh-agent-loop` is merely the one concrete driver registered into it.
   `dsh-compaction` is the contract; `compaction-basic` is a backend. Swappable by design.
3. **The loop does only "call the model, run the tools, repeat."** Everything beyond that
   — guardrails, reminders, policies, UI projection — belongs to *listeners* on the event
   taxonomy, not code wired into the loop.
4. **Composition is declarative and layered.** A YAML file lists rows (id, package,
   config); later layers override whole rows; profiles (web/headless/sdk/acp) are
   different compositions of the same packages. "Which adapters exist is composition;
   which providers run is the user's settings document."
5. **Fail closed, loudly, with structured errors.** A sandbox mode that cannot be
   enforced returns `SANDBOX_UNAVAILABLE` instead of running unconfined. Missing
   approval answerers resolve `unavailable` and the request fails closed.
6. **The model is a swappable component; quality lives in the scaffolding.** Adapters,
   presets, and evals exist so quality is measured and enforced regardless of which
   model is plugged in.
7. **Bounded everything.** Parallel-tool caps (default 10), round caps (goals: 256),
   byte budgets (instructions: 65,536; spill inline: 50,000; tool-result pruner:
   8,192/4,096/1,024), timeouts per call. Nothing unbounded touches the model.
8. **One-shot, audited permission.** Sensitive actions pause for a single allow/reject
   decision; a grant applies only to the requested action; every request lands in the
   session's audit log.
9. **Never destroy history — shadow it.** Compaction, tool-result pruning, and
   checkpointing all preserve the log; they only change what the model *sees* next.
10. **Document the model experience.** Every DSH package carries a "Model Experience"
    section stating exactly what the model sees and when. That is a documentation
    discipline, not code — and Talon's whole quality story is model-facing.

Talon already shares the deepest DSH conviction — honesty (skipped gates,
`Analyzed:false`, crash markers, one quota source) — but expresses it in hard-wired
procedural code. This plan converts that procedural core into the harness shape while
keeping every existing quality layer (verify ladder, guardrails, failure index, skills)
as first-class listeners in the new architecture.

---

## 1. Architecture mapping — DSH pattern → Talon today → target

| # | DSH pattern | Talon today | Gap | Target (H-phase) |
|---|---|---|---|---|
| 1 | Event-sourced session log; derived messages; replay; fork | `conversation_messages` stores final messages; `TrimConversation` **deletes**; resume rebuilds from stored rows | Log is not append-only; no replay guarantee; history destructive | **H1** |
| 2 | Durable anchors: each model call cites chunk seqs; cancelled stream appends `interrupted:true` + delivered prefix | Prompt.Status `running` crash marker; boot sweep; no mid-turn anchors | Mid-turn crash loses the partial transcript the user saw | **H1** |
| 3 | Agent contract / loop driver split; factory registry | single `Orchestrator.ChatWithTools` | loop not swappable; seams hard-wired | **H2** |
| 4 | Interception events: `agent/pre-step` (admit/reject/replace), `agent/request-error` (retry), `agent/turn-stopping` (steer) | phase transitions emitted; no interceptable steps | guardrails/reminders are loop-internal calls | **H2** |
| 5 | Steer / followup / inject message classes | a prompt during a running build simply waits | user cannot redirect a running build without aborting | **H2** |
| 6 | Guarded tool pipeline: policy → monotonic guards → around-dispatch → result inspection → finalization → observe | `Registry.Execute` + `validateArgs`; guardrail Check called by loop | pipeline not formal; no parallel pool | **H3** |
| 7 | Parallel-safe vs exclusive tools; bounded pool (10) | `IsReadOnly` marker exists; execution serial | parallelism never used | **H3** |
| 8 | Sandbox capability w/ modes (read-only / workspace-write / danger-full-access); fail-closed; one-shot escalation | `run_shell` tiers + `ValidateSandboxPath` (good, but bespoke per tool) | not one capability; fs tools unconfined | **H4** |
| 9 | Approval seam: one-shot, fail-closed, ask/never, audited | `GrantApproval(ScopeDeviceBuild)`; tier2 shell notion | not a single seam; not all sensitive paths covered | **H4** |
| 10 | fs observation policy: unseen → create-only; observed → replace **at version last seen**; edit requires read | `HasReadFile` guardrail (read-before-write) | **agent-vs-Monaco race**: no version check — user's unsaved edits can be clobbered | **H5** |
| 11 | Skills: multi-source merged registry, winner-per-name, load-on-demand | `go:embed` single source, hand-rolled front-matter | no project-local skills; no per-project knowledge growth | **H6** |
| 12 | Instructions chain (AGENTS.md) w/ byte budget, most-specific wins | none (knowledge is baked into the binary) | projects cannot teach the agent their own conventions | **H6** |
| 13 | System prompt = registry of ordered sections; per-preset persona; catalog stable across modes for request-cache | `SystemPromptBuild` assembles monolithically (already cache-aware via fingerprints) | section assembly hard-coded; tier discipline not a section | **H7** |
| 14 | Compaction: backend contract; shadow-not-delete; tool-result pruner tier (8k/4k/1k) below conversation compaction; `/compact` | `Compact` at 200k; `TrimSupersededToolOutput`; `clipOutput` head/tail | destructive trims; no pruner tier; no on-demand compact | **H8** |
| 15 | Spill store: oversized tool text → disk locator + retrieval guidance; nothing lost | `clipOutput` truncates 32KB (head 3/4 + tail 1/4) | long gradle/logcat output is silently lost to the model | **H8** |
| 16 | Subagents: one-shot + continuable children; scoped tools; lineage; discovery; fork joins parent composition (same tools) | none | biggest capability gap: no delegation | **H9** |
| 17 | Workflow: scripted fan-out with `agent()`/`parallel()`/`pipeline()`, schema-validated results, phases | none | parallel verification / multi-screen checks impossible | **H10** |
| 18 | Ralph: fresh-agent rounds over shared workspace (maxRounds 64) | none | "fix all failures" loops burn one context | **H14** |
| 19 | Goals: one durable objective per session; CAS mutations; round cap; blocked = code + explanation; survives restart | `maxRounds` config only | long builds cannot resume objective-driven after restart | **H11** |
| 20 | Profiles: web / headless / sdk / acp — same packages, different compositions | one HTTP+SSE+WS server | evals/CI run against fakes, not the real boot path | **H11** |
| 21 | Presets: per-session composition from declarative files | per-request Registry (equivalent spirit) | composition not declarative/configurable | **H12** |
| 22 | Repeat-tool-reminder: advisory nudge at 3/5/8 identical calls, per-agent, user message clears | `WriteCounts` (3rd-write nudge) — same idea, file-writes only | not generalized to all tools | **H3** (listener) |
| 23 | Session titles (LLM, first prompt, bounded words/bytes) | none | project lists show raw first prompt | **H13** |
| 24 | Hot-reloadable settings + per-request credential resolution ("composition decides adapters; settings decide providers") | `AI_*` env at boot only | model routing not tunable without restart | **H12** |
| 25 | Model Experience docs per package | tool descriptions (good) but no per-package "what the model sees" contract | doc drift risk | **H13** |
| 26 | Feedback-gated telemetry; token meter | `metrics.go` counters (good) | no per-session token meter surface to the user | **H13** |

**Already at parity or ahead — keep, refit only as needed:** verify ladder (DSH has
nothing like it — it is Talon's domain crown), guardrail content checks, failure
index with cooldowns, `KnownFiles`/`SeenSkills` context economy, prompt-cache discipline
(sorted tools, projectMap fingerprint), quota single-source, metrics live gauges, crash
honesty markers, evals + phase-guard polarity tests, GO_DESLOP style law.

---

## 2. Target architecture (Go, idiomatic — the pattern, not the framework)

DSH is TypeScript with a runtime plugin framework. Talon is Go. **We adopt the
architecture, not the machinery**: Go interfaces + one in-process event bus + a boot-time
composition root, with compile-time safety where DSH pays runtime dynamism.

### 2.1 New package layout

```
backend/internal/harness/
  events/       event taxonomy: session/*, agent/*, tool/*, fs/*, approval/*, goal/*
  sessionlog/   append-only log; DeriveMessages; replay; shadow compaction; Fork
  agent/        Agent contract, live registry, per-agent scoped context
  loop/         THE concrete driver (orchestrator core, refit)
  pipeline/      tool pipeline: policy → guards → dispatch(pool) → inspect → observe
  sandbox/      modes + policy + fail-closed runner + escalation handshake
  approval/     one-shot seam + audit events
  skills/       multi-source registry (wraps existing internal/agent content)
  instructions/ AGENTS.md chain with byte budget
  promptsec/    system-prompt section registry (ordered, fingerprinted)
  compaction/   pruner tier + backend contract + basic backend
  spill/        oversized-output store (locator + retrieval guidance)
  subagent/     delegation seam + in-process provider (one-shot, continuable)
  workflow/     declarative fan-out engine (JSON spec, not JS)
  goals/        durable build objectives (CAS, rounds, blocked)
  presets/      declarative composition (talon.composition.yaml) + preset files
```

Existing packages keep their domains: `tools/*` tool implementations register into the
pipeline; `guardrails`, `verify`, `runtime`, `redroid`, `metro`, `release`, `devclient`
stay as-is — they become *consumers* of the seams instead of being called by the loop.

### 2.2 Event taxonomy (the contract everything speaks)

```go
// session lifecycle
SessionCreated, SessionEvent(appended), SessionFlush
// agent lifecycle + interception (listeners may decide)
AgentCreated, AgentDisposed, AgentStatus,
AgentPreStep   → decision: Admit | Reject | ReplaceMessages
AgentRequestError → decision: Retry | Terminal
AgentTurnStopping  → decision: Close | Steer(keep open)
// tools
ToolPolicy, ToolDispatch, ToolResult, ToolObserve
// filesystem (guarded)
FSRead, FSWrite
// permission
ApprovalRequest, ApprovalDecided
// goals
GoalCreated, GoalEdited, GoalRound, GoalBlocked, GoalCleared
```

Rules carried over verbatim from DSH semantics:
- An **entered** `AgentPreStep` decision appends its complete message batch before the
  driver claims again; a **rejected** decision appends nothing.
- A cancelled stream appends an `interrupted: true` anchor carrying the delivered
  prefix — the next request contains exactly what the user saw.
- Undispatched tool calls after cancellation get synthetic `tool/call` +
  `ABORTED_BEFORE_DISPATCH` result pairs, so the log never dangles a call.
- `AgentRequestError` retry does not re-enter the loop; a listener answers `{retry}`
  without calling next; unhandled failure is terminal for the turn, not the loop.
- Plugin (listener) failure closes the turn directly — extension failure never corrupts
  the loop invariants.

### 2.3 Composition root (talon.composition.yaml)

main.go stops wiring structs and starts *reading* a composition file — the Go analogue
of `cordis.patch.yml`. Rows are (id, component, config); last-write-wins per id across
layers (base → mode → project preset). This is the single biggest cultural shift: **the
architecture becomes data**.

```yaml
# talon.composition.yaml (base layer)
- id: sessionlog
  component: harness/sessionlog/sqlite
- id: agent-registry
  component: harness/agent
- id: loop
  component: harness/loop
  config: { maxParallelToolCalls: 4, maxRounds: 60 }
- id: sandbox
  component: harness/sandbox/local
  config: { mode: workspace-write, workspaceRoot: $WORKSPACE_DIR }
- id: approval
  component: harness/approval
  config: { policy: ask }
- id: pipeline-guards
  component: harness/pipeline
  config:
    repeatReminder: { thresholds: [3, 5, 8] }
    toolResultPruner: { thresholdChars: 8192, headChars: 4096, tailChars: 1024 }
    spill: { maxInlineBytes: 50000 }
- id: skills-embed
  component: harness/skills/embedded
- id: skills-fs
  component: harness/skills/filesystem
  config: { roots: ["$PROJECT/.talon/skills"] }
- id: verify-ladder
  component: verify            # existing package, now a listener
- id: guardrails
  component: guardrails        # existing, now a pipeline stage
```

Modes are overlays: `--profile server` (today's product), `--profile headless`
(evals/CI: boot the real composition, run a scripted build, print a scorecard, exit),
`--profile sdk` (thin JSON-RPC for the companion mobile app to talk to instead of
duplicating HTTP shapes). Presets per project tier can later swap tool sets and prompt
sections declaratively.

### 2.4 The loop after the refit (H2 end-state)

What stays: phase machine (as events), rounds, ramble detection, compaction trigger,
context economy (KnownFiles/SeenSkills/WriteCounts become pipeline listeners),
ask_user pause/answer binding, verify gate, crash honesty.

What changes: every cross-cutting call moves from *invoked* to *listened*:

| today (loop calls) | after (listener reacts to) |
|---|---|
| `guardrail.Check(...)` | `ToolPolicy` event |
| failure-index reminder injection | `ToolResult` (error-shaped) |
| `MarkSkillSeen` / auto-attach | `ToolDispatch` |
| `NoteFileWrite` nudge | `FSWrite` |
| `EmitToolCall` running/completed | `ToolDispatch`/`ToolResult` (emitter subscribes) |
| verify scheduling after N writes | `FSWrite` counter listener |

The loop file shrinks to: claim → pre-step → request (adapter) → append anchors →
dispatch tool calls (pool, exclusive barriers) → append results → repeat → turn close.
DSH's own words become our rule: *"everything beyond 'call the model, run the tools,
repeat' belongs to plugins listening on the event taxonomy."*

---

## 3. The H-phases

Each phase: scope → concrete work → migration/flag → eval gate → risk. The flag pattern
follows the patch-buffer precedent (`TALONSTUDIO_PATCH_BUFFER`): every phase ships
dark, flips on per-project, and the old path stays until the eval gate is green.
`make evals` grows one phase-guard test per H-phase (both polarities), so the merge gate
extends automatically.

---

### H1 — Event-sourced session log (foundation; everything else depends on it)

**Scope:** replace stored-message history with an append-only event log; derive the
model-visible history; make compaction shadowing; fork support; mid-turn durability.

**Work:**
1. New table `session_events(seq INTEGER PRIMARY KEY, session_key, project_id, type,
   payload JSON, created_at)` — one writer path (`sessionlog.Append` freezes + validates
   payload, mirroring DSH's lossless-JSON discipline).
2. `DeriveMessages()` projection with incremental cache; surface events
   (`user/message`, `assistant/message`, `tool/result`, `question/*`) declare how they
   join the ordered surface; non-surface events (chunks, phase marks) never produce
   messages but remain in the log.
3. Compaction = shadow marker events; `DeriveMessages` honors them; **log rows are
   never deleted** — delete `TrimConversation`'s delete path (keep the name, change the
   semantics to shadowing; the current destructive trim is a replay hazard).
4. Anchors: every completed model call appends an `assistant/message` anchor; a
   cancelled/interrupted stream appends `interrupted:true` + delivered prefix; cancelled
   tool calls get synthetic `ABORTED_BEFORE_DISPATCH` pairs.
5. `Fork(sessionKey, atSeq)` — child session from a stable prefix (used by H9).
6. Durability checkpoint before each model request (DSH's
   session-checkpoint-policy pattern): flush events so a crash mid-turn resumes from the
   last complete step, not the last complete turn.
7. Read path: prompts.go resume/answer flows read through `DeriveMessages`; the legacy
   `conversation_messages` table is frozen (write-through only) for one release, then
   dropped behind a migration.

**Migration:** `TALONSTUDIO_SESSIONLOG=v1|legacy` flag; v1 writes both, reads v1; flip
default after evals; drop legacy table in a later release.
**Eval gate:** replay determinism (same log → byte-identical derived history across two
runs), interruption replay (append `interrupted` mid-stream → resumed request contains
exactly the delivered prefix), fork isolation (child prefix unaffected by parent tail).
**Risk:** biggest schema change in the repo. Mitigated by: log is additive, legacy path
frozen not deleted, replay tests gate the flip.
**Why first:** DSH builds everything on the log; so do we. H2–H14 all read/write it.

---

### H2 — Agent contract, loop driver split, interception events, steer semantics

**Scope:** extract `Agent` from `Orchestrator`; make the loop the only concrete driver;
add interceptable pre-step/request-error/turn-stopping; add steer/followup/inject.

**Work:**
1. `harness/agent`: `Agent` interface (Followup, Steer, Inject, Cancel(keepInbox),
   WhenIdle), live registry, per-agent scoped registration (tools, prompt sections,
   listeners unwind with the agent).
2. `harness/loop` moves out of `orchestrator` as the driver factory. Orchestrator keeps
   domain state (verify attempts, session reconcile) but no longer *is* the loop.
   Creation is a rollback-covered transaction (setup before publish; setup throw never
   leaks a half-registered agent).
3. Message classes on the prompt stream: **followup** = queue next turn (today's
   behavior), **steer** = wake the current turn with next-step input (new: the user
   redirects a running build without aborting it — "actually use reanimated-v4" lands
   mid-build), **inject** = context without waking. SSE gets a `steer` route; AIChat
   composer grows a "send to running build" affordance (steers show as distinct
   dimmed bubbles, honestly marked "steer — not a new turn").
4. `AgentRequestError` retry decision moves out of `doWithRetry`'s fixed 4-retry policy:
   listeners (routing) can answer retry-with-backoff, retry-with-cheaper-model (existing
   tier logic becomes a listener), or terminal.

**Migration:** `TALONSTUDIO_LOOP=driver|legacy` — driver wraps the existing
ChatWithTools first, then replaces it.
**Eval gate:** steer lands mid-turn (a steered turn's next request contains the steer
text; an unsteered one does not); request-error retry listener fires; legacy and driver
produce identical transcripts for the same scripted prompt on the fake provider.
**Risk:** steer changes UX contract — needs frontend co-design; ship inject-only first.

---

### H3 — Tool pipeline + bounded parallelism + generalized repeat-reminder

**Scope:** formalize the guarded pipeline; use `IsReadOnly` for real; advisory
repeat-reminder across all tools.

**Work:**
1. `harness/pipeline` stages: policy (allow/deny/ask — guardrails move here) →
   monotonic guards → dispatch (bounded rolling pool, default 4 given the 8-req/min
   provider limit; exclusive tools are ordering barriers) → result inspection
   (failure-index, reminders) → content finalization (pruner/spill) → observe-only
   notification (metrics, trace emitter).
2. Repeat-reminder listener: identical tool+args at 3/5/8 → advisory reminder to analyze
   the last result or change approach; per-agent tracking; a new user message clears
   the count. (Generalizes today's WriteCounts to every tool; keep the
   stronger *enforced* 3rd-write nudge for file writes specifically.)
3. Tool catalog stability rule (from DSH plan-mode): the set of tools advertised never
   changes mid-session even when policy tightens — schemas stay byte-stable for the
   provider's request cache; refusals happen at policy time, not by hiding tools.
4. `maxParallelToolCalls` config row (composition file), default conservative.

**Eval gate:** two read-only tools dispatched in one step overlap in the log (timestamps
interleave); an exclusive tool never overlaps; reminder fires at 3 and not at 2; catalog
bytes stable across a policy change.
**Risk:** parallel tools double provider traffic only if they call the model — they
don't (tools are local), so the pool is safe under the rate limit; but parallel *output*
interleaving must stay model-ordered in the log (DSH: "policy, durable results, and
result context remain model-ordered").

---

### H4 — Sandbox + approval seams

**Scope:** one `Sandbox` capability and one `Approval` seam; run_shell tiers and fs
validation converge onto them; every sensitive path routes through the seam.

**Work:**
1. `harness/sandbox`: modes `read-only` (no writes; `/dev/null` semantics preserved),
   `workspace-write` (project dir + tmp), `danger-full-access`. Runner enforces per
   call; **unforceable mode → structured `SANDBOX_UNAVAILABLE`, never unconfined**.
   run_shell's tier0/tier1/tier2 map onto modes (tier2 = escalation flow).
2. `harness/approval`: one-shot requests → `allowed-once | rejected | cancelled |
   unavailable`; policy `ask` (default) / `never`; every decision appends an audit
   event to the session log (user, action, scope, outcome — joins the existing honest
   UI story: the Settings page could show the audit trail).
   Wire `GrantApproval(ScopeDeviceBuild)` and tier-2 shell through it; export/release
   keystores and git push adopt the same seam (they are sensitive today by convention
   only).
3. fs tools (read/write/edit) get sandbox-checked paths — today only `run_shell` and
   `ValidateSandboxPath` guard; the capability makes confinement uniform.

**Eval gate:** write outside workspace denied with structured error; escalation request
produces exactly one audit event and one grant; `never` policy rejects deterministically
without prompting.
**Risk:** low — converges three existing bespoke mechanisms; behavior-preserving by
design.

---

### H5 — Version-stamped file observation (fixes the agent/Monaco race)

**Scope:** upgrade read-before-write to versioned replaces.

**Work:** the fs observation listener records (path, mtime/hash) at every read; a write
to an observed file must name the version last seen (write tool input gains an optional
`expect_version`; the pipeline supplies it automatically from the model's last read of
that file — the model never has to think about it). Unseen file → create-only. Mismatch
→ structured error naming the conflict: "file changed since you last read it (user edit
at 14:22) — re-read and re-apply." The frontend already tracks drafts; the IDE's own
save path gets the same guard, so agent-clobbers-draft and draft-clobbers-agent both
become impossible instead of racy.

**Eval gate:** stale-version write rejected with the remedy text; fresh read then write
succeeds; concurrent Monaco draft + agent write produce one rejection, never a silent
merge.
**Risk:** UX: model-facing friction. Mitigated by pipeline-injected versions (invisible
to the model) and remedy-first error text.

---

### H6 — Skills registry (multi-source) + project instructions chain

**Scope:** skills from more than the binary; projects teach the agent their own
conventions.

**Work:**
1. `harness/skills`: provider interface; embedded provider (today's go:embed) +
   filesystem provider (`<workspace>/.talon/skills/*/SKILL.md` — same front-matter
   format, so existing authoring knowledge transfers). Merged catalog, one winner per
   name (embedded wins ties — binary knowledge stays authoritative), load-on-demand
   body read; `MarkSkillSeen` economy unchanged.
2. `harness/instructions`: AGENTS.md chain — user-global
   (`$TALON_HOME/AGENTS.md`) + project (`<workspace>/AGENTS.md` + nested
   `app/AGENTS.md`, `features/AGENTS.md`...). Byte budget (65,536), broader files
   omitted before the most-specific is truncated; chain refreshed on next filesystem
   touch (no watcher — same reconciliation-on-touch discipline as DSH). The agent can
   *write* the project AGENTS.md (it's a workspace file) — durable conventions the
   user can read and edit in the tree, a natural complement to write_memory.
3. Generated apps can ship a starter AGENTS.md from the template so every workspace
   begins with the MWA/RN rules in editable form.

**Eval gate:** filesystem skill wins catalog listing, loads on demand, marks seen once;
nested instructions budget behavior (omit broader, truncate most-specific); chain change
visible after next fs touch.
**Risk:** project-authored skills are prompt-injection surface. Mitigation: filesystem
skills are listed to the model but bodies are fetched only on explicit read_skill —
same trust boundary as today's web_search — and rules/core.md keeps
alwaysApply authority.

---

### H7 — System prompt as a section registry

**Scope:** monolithic SystemPromptBuild becomes ordered, fingerprinted sections.

**Work:** `harness/promptsec` registry with stable order:
`persona → rules/core (alwaysApply) → device facts → working-discipline (tier) →
project map (fingerprinted) → memory digest → ADRs → failure reminders → plan
checklist → instructions chain → skills catalog`. Each section is independently
fingerprinted; the loop re-sends the prefix only when the changed section sits at the
head (generalizes today's projectMapFP discipline to every section). Per-project
presets may suppress or add sections (a "teaching" preset can add the SCL starter
guide; an "expert" preset can drop spelled-out steps).

**Eval gate:** unchanged sections produce byte-identical prompt bytes; a memory write
changes only the memory section's fingerprint; section order is a total order (no
nondeterminism).
**Risk:** none structural — this is a refactor of an already cache-aware path with
tests locking bytes.

---

### H8 — Compaction architecture + spill store

**Scope:** shadow-based compaction with a pruner tier; oversized output spills instead
of truncating.

**Work:**
1. Tool-result pruner (runs *before* full compaction): 8,192 threshold, 4,096 head /
   1,024 tail with a pointer line ("…N lines elided; full output in spill
   <locator>"). Replaces `clipOutput`'s silent truncation at the *model-visible*
   layer while the full text goes to spill.
2. Spill store: `ctx.spill`-equivalent — save oversized text (gradle, logcat, project
   maps) under the project; return opaque locator + byte count + retrieval guidance
   ("read with read_spill(locator) or the logcat tool filtered"). Failure-index
   patterns then survive long builds (today a 32KB clip can lose the middle of the
   error that mattered).
3. Full compaction backend contract + basic backend (today's 200k Compact) refit to
   *shadow* on the H1 log rather than rewrite history. `/compact`-equivalent: a
   "Condense context" affordance in the chat rail (one useful reduction below
   threshold) — honest UI: shows what was condensed and remains expandable.

**Eval gate:** pruner output contains head/tail + locator; spill retrieval returns
byte-identical text; compaction shadow preserves replay (re-derive of the whole log
reproduces pre-compaction history exactly — the H1 replay test extended).
**Risk:** spill files are project data → workspace cleanup and export paths must
include/exclude them deliberately (exclude from release export; include in git push
only behind .gitignore).

---

### H9 — Subagents

**Scope:** child-agent delegation — the largest new capability.

**Work:**
1. `harness/subagent`: service contract (create one-shot / continuable child, collect
   result, interrupt, lineage, discovery without loading). First backend: **in-process**
   (a second Agent on the same loop, scoped registry, same provider client).
2. Two model-facing tools: `delegate` (one-shot: self-contained prompt + optional
   schema → validated structured result; default background) and
   `delegate_fork` (child seeded with parent history prefix — for "continue this
   analysis" style work; shares parent composition = same tools as parent).
3. Use cases (documented in the tool description so the model reaches for them):
   parallel package research ("inspect these 5 packages and report their export
   surfaces" — 5 children, bounded pool), screen-by-screen verification fan-out,
   README/docs generation while the main build continues, adversarial code review of a
   finished feature (a child with read-only tools reviews the diff).
4. Rate-limit reality (the hard constraint this repo already lives with): pool default
   2 children; children inherit the turn's model routing; the failure-index gets
   provider-429 patterns with a cooldown that **suppresses fan-out for N rounds**.
   Discovery tool (`list_children`) mirrors DSH's list_agents with durable/ready status.
5. Continuable children appear in the frontend as a collapsible "Delegated" section of
   the trace (AgentTrace grows a row kind); no separate chat surface.

**Eval gate:** one-shot child returns its final text as the tool result (nothing
interleaves into the parent transcript); fork child's first request contains the parent
prefix and cites no tools the parent lacks; 429 cooldown suppresses a second fan-out;
child crash produces a structured parent-visible error, not a parent crash.
**Risk:** context and cost multiplication; prompt-injection via child output (treat
child results as untrusted data in the parent — same discipline as web results);
runaway recursion (children cannot spawn children — depth 1, enforced in the seam).
**Sequencing note:** depends on H1 (fork) and H2 (scoped agents).

---

### H10 — Declarative workflow fan-out

**Scope:** DSH's scripted workflows, translated to a *declarative* spec — Go safety and
evaluability over JS expressiveness.

**Work:** `harness/workflow` engine running a JSON spec the model writes:
```json
{ "name": "verify-screens",
  "phases": [
    { "title": "parallel screen checks",
      "parallel": [
        { "prompt": "Boot and vision-check screen 'Wallet'; return pass/fail",
          "schema": { "pass": "boolean", "reason": "string" } },
        { "prompt": "Boot and vision-check screen 'Settings'; ...", "schema": {...} }
      ] },
    { "title": "aggregate",
      "prompt": "Given the phase-1 results, write the verify summary" } ] }
```
Engine semantics port DSH's: no barrier between pipeline stages, per-item isolation (a
thrown stage drops that item, never the run), holder-owned results (never rejects),
phases narrate into the trace as task_update events, every child attributed to the
invoking agent. Tool: `run_workflow` — model reaches for it when a task decomposes into
many independent pieces (its description says exactly that).

**Eval gate:** spec validation (unknown keys rejected); parallel items overlap; a
failing item nulls only itself; aggregate phase sees phase-1 results; result is the
script's final JSON.
**Risk:** spec authoring is harder for the model than JS. Mitigation: tiny spec surface
(phases, parallel, prompt, schema) + one worked example embedded in the tool
description.

---

### H11 — Durable goals + headless profile

**Scope:** long builds that survive restarts; the real boot path in CI.

**Work:**
1. `harness/goals`: one current objective per project (text, phase, rounds, revision
   history, CAS mutations so a stale view cannot clobber newer state; blocked = stable
   code + human explanation; round cap default = today's maxRounds). A crashed
   mid-build resumes the *objective* on next prompt: "continue the goal" reconstructs
   from the H1 log anchors + checkpoint. UI: goal chip in the chat rail (rounds,
   phase); blocked goals render the explanation verbatim.
2. `--profile headless`: same composition, no HTTP/WS, scripted run (env/config-driven
   project + prompt, fake provider), prints the evals Scorecard, exits nonzero on
   critical-check failure. Evals currently wire fakes around the build; headless runs
   the *real* boot (reconcile, workspace, pipeline, log) under the fake provider — the
   honest end of the "no fake paths in CI" principle. CI gets a third job.

**Eval gate:** goal survives a simulated restart (kill between rounds, re-boot,
re-derive, continue); blocked goal code stable across restarts; headless run green in CI
twice consecutively.
**Risk:** goal continuation must not double-apply file writes — H5 version stamps and
H1 anchors make this checkable; eval asserts no re-execution of completed rounds.

---

### H12 — Presets + composition + hot-reloadable model settings

**Scope:** composition becomes data; model routing becomes runtime-tunable.

**Work:**
1. `talon.composition.yaml` (§2.3) becomes the wiring source; main.go is a composer.
   Overlay layers: base → mode (server/headless/sdk) → per-tier project preset. Row
   semantics: last-write-wins per id, whole-config replacement (no merge ambiguity —
   DSH's exact rule, stated in their patch file).
2. Model settings hot-reload (the DSH settings discipline — "which adapters exist is
   composition; which providers run is settings"): routing policy/pricing/tier
   overrides move from boot-time env to a settings document watched for change
   (`ROUTING_POLICY_JSON` semantics preserved). The web Settings page gains an
   operator section; no restart to swap model tiers.
3. Project presets: e.g. `starter` (all skills, spelled-out discipline, SCL guide
   section), `expert` (compressed discipline, no starter guide), `fast` (cheap-model
   routing default, pruner tighter). Preset switch only while the project has produced
   nothing (DSH's rule) — an in-flight build never changes composition mid-turn
   (cache stability).

**Eval gate:** overlay precedence test (base value overridden by mode overridden by
preset); settings change flips routing on the *next* turn without restart; mid-turn
preset switch rejected with remedy.
**Risk:** config-as-code drift — mitigate with `--dump-config` (DSH has it; we get it
free from the composer) diffed in CI against a checked-in golden.

---

### H13 — Model experience docs, session titles, token meter

**Scope:** the documentation and observability disciplines.

**Work:**
1. **Model Experience doc contract:** every `internal/harness/*` and `internal/tools/*`
   package README gains a "What the model sees" section (inputs, outputs, budgets,
   failure shapes, what changes caches). CI guard: a phase-guard eval asserting the
   section exists for every registered tool/pipeline stage — doc drift fails `make
   evals`, which is the merge gate. This turns DSH's best documentation practice into
   a test.
2. Session/project titles: LLM-titled from the first prompt (bounded words/bytes, the
   DSH session-title pattern), falling back to the honest truncated prompt; dashboard
   lists become human.
3. Token meter per session: the chat rail gains the per-turn `in · out` the TurnFooter
   already shows, rolled per session; metrics.go unchanged (already right).

**Eval gate:** docs guard green; title LLM bounded and falls back; meter sums match
store usage rows.
**Risk:** none material.

---

### H14 — Ralph lane (optional, last)

**Scope:** fresh-agent iteration for whole-objective repair loops.

**Work:** `harness/ralph`: run N fresh-child rounds against the shared workspace toward
one immutable objective (e.g. "all verify gates green"); each round is a fresh context,
workspace + session log are the only memory; a bounded structured report crosses
rounds (what passed, what remains, the one blocker). `maxRounds` default 8 (not 64 —
our rounds are minutes, not seconds). Trigger: exhausted repair attempts on a goal
(H11) offer "let a fresh agent take the whole objective" in the exhaustion UI.

**Eval gate:** rounds are truly fresh (no history carryover beyond the report);
workspace state carries; terminal conditions (done / concrete blocker) reachable.
**Risk:** burns budget; hard-cap rounds and make the offer explicit, never automatic.

> **W8.2 note (2026-09):** `internal/harness/ralph` was **deleted** — it had zero
> non-test importers (law 8) and no phase in `PLAN_WORLDCLASS.md` planned to consume
> it (W14 consumes `composition`/`settings` only). The package's fate was an explicit
> either/or in PLAN_WORLDCLASS W8.2: wire the fresh-context repair offer under its own
> spec, or delete with this note. We deleted. Last tree that had it: commit `e787549`
> (`git show e787549:backend/internal/harness/ralph/ralph.go`).
> If the repair offer is ever wanted, re-introduce it greenfield against this spec.

---

## 4. What we deliberately do NOT adopt

Honesty about the tradeoffs, in both directions:

1. **Cordis runtime DI/hot-reload.** Go's composition happens at boot under compile-time
   types; a plugin framework's dynamism would trade away the safety the codebase's
   GO_DESLOP law is built on. We keep declarative composition (data) + compile-time
   components.
2. **A JS workflow engine.** Declarative JSON spec (H10) — evaluability and injection
   safety over expressiveness. DSH isolates scripts in a worker thread; we avoid the
   problem class entirely.
3. **224-package granularity.** ~16 focused Go packages under `internal/harness/` plus
   the existing domains. Cohesion beats symmetrical decomposition at this size.
4. **ACP/SDK surface now.** The companion app speaks the same HTTP API (by design);
   an SDK profile (H12) is where that could converge *if* the mobile app outgrows
   HTTP — not before.
5. **MCP client.** After H6's skill providers, if a real need appears.
6. **PTC (prompt-tool-calling) mode.** Our dual-protocol adapter already covers the
   two wire formats we actually use; native function calling is the default everywhere.

---

## 5. Sequencing and dependency graph

```
H1 session log ─────────────┬──> H8 compaction+spill (needs shadows)
                             ├──> H9 subagents (needs Fork + scoped agents)
                             │        └──> H10 workflow (runs over children)
                             │                └──> H14 ralph (optional)
H2 agent/loop split ─────────┘──> H3 pipeline (needs seam) ──> H7 prompt sections
      │        └── steer UX (inject-first rollout)
H4 sandbox+approval ──> H5 versioned fs (approval-independent, sandbox-checked writes)
H6 skills+instructions (independent — can run parallel to H2/H3)
H11 goals+headless (needs H1; headless needs H12's composer early — do composer first in H11)
H12 presets/composition/settings (composer lands with H2; overlays with H11)
H13 docs/titles/meter (anytime after H3; docs guard last so it checks the final set)
```

Suggested tracks (two workstreams, one reviewer cadence):
- **Track A (state):** H1 → H8 → H9 → H10 → (H14)
- **Track B (seams):** H2 → H3 → H7 / H4 → H5 / H11+H12 / H6 anywhere / H13 last

Every phase: flag-gated, eval-gated, one PR-shaped slice at a time, PLAN.md updated to
live status as each lands (the repo's existing discipline).

---

## 6. Definition of done (the world-class bar)

1. A build turn can be **replayed byte-identically from the log** after any crash,
   restart, or compaction — the H1 eval, permanently in `make evals`.
2. The loop package contains **zero domain knowledge**: no guardrail names, no verify
   calls, no reminder text — all of it in listeners, each independently testable.
3. Adding a new guardrail, reminder, or verify stage is a **new file + one composition
   row**, touching no existing file (the DSH extensibility test).
4. The model can **delegate, fan out, and iterate fresh** (H9/H10/H14) under the
   provider's rate limit with cooldowns that make the system degrade gracefully, not
   fail.
5. A user typing during a running build is a **first-class event** (steer), and the
   agent and the user **cannot clobber each other's edits** (H5).
6. `--profile headless` runs the **real boot path** in CI and prints a scorecard.
7. Every model-facing surface has a **Model Experience doc** that CI verifies exists.
8. All of it still passes the existing phase-guard evals — the old guarantees are the
   new floor, not casualties.

---

## 7. Frontend direction (user directive, collected at the H2 boundary)

The IDE is refit around the DSH agent-workspace model — the code editor is
**removed** as the centerpiece and replaced by a live agent surface. Captured
verbatim from the user before the H2 steer contract was designed:

- **Take out the code editor.** The project view becomes a file tree + the
  agent workspace, not a text editor.
- **The chat section renders exactly how the DSH harness renders**: thinking,
  tool calls, bash commands, the emulator view — a full-fidelity transcript,
  not just prose.
- **The text-editor pane now renders the chat streaming** in DSH style, using
  the existing loading components and other components; the general UI is
  improved as part of it.
- **Tasks**: show tasks, render markdown files like DSH, show pending vs
  completed, create task files.
- **Show git diff.**
- **Improve the entire planning process and how it renders** — it should act
  like DSH does (plan cards, task files, live marks, resume affordances).

UX decisions confirmed by the user at collection time:
- **Steer UX (AIChat)**: while a build runs, the chat input sends **straight
  into the running loop as a steer** — dimmed, labeled "steering the running
  build" — with a small **"queue instead"** toggle to defer the message to the
  next turn.
- **Agent trace / rounds**: each agent round renders as **one collapsible row**
  — intent line, tool-call chips (green/red by result), one-line outcome, round
  budget visible.

Consequences for the harness phases:
- **H2 steer events** must carry everything the transcript needs to render
  DSH-style: thinking blocks, tool dispatch/result pairing, bash command
  surfaces, emulator/screenshot events. The SSE stream is the transport; the
  event log (H1) is the durable record both derive from.
- **H6 task files** become a first-class UI surface, not just a prompt
  section.
- **Git diff** joins the toolset as a rendered surface (H3 pipeline feeds it).
- The **plan-mode refit** (planning process + rendering) folds into H7's
  prompt-section work and its UI tail.

The IDE refactor itself lands phase-by-phase: each H-phase ships its backend
contract plus the frontend piece that renders it, so the GUI is never left
showing a surface the backend no longer produces.

---

*Prepared after a full read of the DSH package contracts (agent-loop, agent, session,
tools, sandbox, bash-sandbox, user-approval, fs-observation-policy, skill, goal,
subagent, workflow, spill, compaction, plan-mode, persona, presets, instructions,
repeat-tool-reminder, and the base-bundle composition) and a full read of this
repository. Corrections welcome — especially on sequencing: Tracks A and B are
independent enough to reorder within their tracks.*
