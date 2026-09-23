# Talon — World-Class Agent Plan (W-series)

**Status:** PROPOSED — plan only; no edits made.
**Method:** comparative audit of how Claude Code, OpenCode, Cursor, and the DeepSeek
Harness (DSH) are actually built, mapped against this repository's *verified* current
state (full code audit 2026-09-22: `make evals` run, grep-level evidence collected).
**Predecessor:** `PLAN_HARNESS.md` (H1–H14). Its architecture is right; its execution is
partial — several phases landed dark or half-built (§1.2). The W-series finishes the
H-law, then adds what *no* amount of H-work provides: senses, measurement, and open
seams.
**Convention:** every phase is flag-gated, eval-gated (`make evals` grows one
phase-guard test per phase, both polarities), and ships one PR-shaped slice at a time —
the repo's existing discipline.

---

## 0. How the reference systems are built

### 0.1 Claude Code (Anthropic)
- **Architecture beats prompts.** The strong behaviors are structural, not written in a
  prompt: a tool can be *denied*, a subagent gets an *isolated context window*, a hook
  can *block* an action. Their own rule: *"Put guardrails in hooks — an instruction like
  'never edit .env' in CLAUDE.md is a request, not a guarantee."*
- **Hooks = deterministic lifecycle callbacks.** `PreToolUse` / `PostToolUse` /
  `UserPromptSubmit` / `Stop` / `StopFailure` / `SessionStart` / `SubagentStart` /
  `SubagentStop` / `PermissionDenied` / `ConfigChange`. Handlers are shell commands,
  HTTP endpoints, MCP calls, prompts, or subagents; JSON in, JSON out; exit codes decide
  block vs proceed (2 = blocking, stderr fed back to the model); `async: true` runs
  fire-and-forget (e.g. run the test suite after every `Write` while the agent keeps
  working).
- **Subagents as context isolation.** Each child = own context, own system prompt, own
  tool subset, own model (route exploration to a cheap model), own permission mode
  (`plan`), hooks, optional preloaded skills. Built-in Explore/Plan agents; custom ones
  from frontmatter/files; **depth, concurrency, and spend caps**; background execution;
  fork mode that inherits parent history. Descriptions are routing — budgeted (15k
  tokens across custom subagents).
- **Plan mode is enforced, not suggested** — read-only regardless of allow rules (a
  2026 fix made plan mode hard-block writes even when an Edit allow rule matched), and a
  cheap Explore subagent auto-surveys the codebase during planning.
- **Memory tiers:** CLAUDE.md always loads (kept <200 lines), `rules/` splits overflow,
  skills hold reference material loaded on demand. Skills = SKILL.md, names+descriptions
  in every request, body on invocation.
- **Extensibility trinity:** skills (behavior), plugins (packaging), **MCP** (external
  capabilities, OAuth, scope hierarchy local>project>user).
- **Blast radius:** permission modes, allow-rule matchers, auto-mode hard-deny,
  OS-level sandboxing (Agent SDK sandbox), worktrees, checkpoints/rewind.

### 0.2 OpenCode
- **Client–server core:** a background service owns sessions, config, plugins,
  *permissions*, and tool execution; TUI/IDE/SDK are clients. One process = one
  authority.
- **Config as capability surface:** `model`, `default_agent`, **`permissions`**,
  `agents`, `commands`, `plugins`, `providers`, `mcp`, `skills`, `instructions`,
  `references`, **`formatter`**, **`lsp`** — LSP and formatter are first-class: the
  agent navigates by symbols and gets diagnostics from the language server, not only by
  grep and CLI runs.
- **Plugins** add hooks, transforms, and *tools* (not just knowledge) via a typed API;
  RPC for cross-plugin/client methods.
- **Multi-provider by config** (`providers`), MCP by config/CLI with OAuth.
- **Search without embeddings:** text search + file matching + **LSP-based
  navigation** — the honest alternative to Cursor's index: ground truth from the
  toolchain instead of vectors.

### 0.3 Cursor
- **A semantic layer as the product moat.** Local chunking into **syntactic/AST units**
  (tree-sitter-class parsing) → embeddings from a **custom model trained on agent
  session traces** (LLM ranks what **should** have been retrieved at each step of a
  real coding session; the embedder is aligned to those rankings) → **Merkle tree of
  SHA-256 file hashes** for incremental sync (only changed subtrees re-upload) →
  embeddings cached **by chunk content hash** → vector store (Turbopuffer-class) with
  client-side encryption keys (2026).
- **Hybrid retrieval wins:** semantic + exact-match ("Instant Grep") together beat
  either alone; grep remains a first-class agent tool.
- **Measured relentlessly:** semantic search improved answer accuracy **+12.5%**
  (6.5–23.5% by model), code-retention +0.3% (+2.6% on ≥1,000-file repos), fewer
  dissatisfied follow-ups — via an offline benchmark (**Cursor Context Bench**) plus
  online A/B tests. Retrieval changes are shipped behind measurement, not taste.
- **Agent surface:** Plan Mode, Explore subagent for parallel search, background
  agents, checkpoints, rules/skills/plugins/MCP unified, a **multi-provider model
  matrix** (Anthropic/OpenAI/Google/Grok + own Composer).

### 0.4 DeepSeek Harness (DSH) — the in-repo blueprint (`PLAN_HARNESS.md`)
- Everything is an **event-sourced log**; model messages are *derived*, never stored;
  compaction **shadows**, never deletes; replay = re-derivation.
- **The loop does only** "call the model, run the tools, repeat" — guardrails,
  reminders, policies, projection are **listeners** on one event taxonomy.
- **Composition is data** (`cordis.patch.yml` → our `talon.composition.yaml`);
  *composition decides which adapters exist; settings decide which providers run* and
  hot-reload. Profiles: server / **headless (real boot path in CI, prints a scorecard)**
  / sdk. `--dump-config` golden-diffed in CI.
- Spill carries **retrieval guidance** (`read_spill(locator)`), plan-mode is a package,
  skills are multi-source, subagents/workflow/ralph/goals are bounded (pool, cooldowns,
  depth 1), every package ships a **Model Experience doc** ("what the model sees").
- Their definition of done already says the quiet part: *the loop file contains zero
  domain knowledge; adding a guardrail is a new file + one composition row.*

---

## 1. Where Talon stands

### 1.1 Honest scorecard vs the four

| Capability | Claude Code | OpenCode | Cursor | DSH | **Talon (verified)** |
|---|---|---|---|---|---|
| Agent loop, rounds, steer, pause/answer/resume | ✓ | ✓ | ✓ | ✓ | ✅ **live** (`RunAgentLoop`, `steer.go`) |
| Verify ladder (typecheck→…→vision→interact) | manual/hooks | formatter/LSP/checks | run checks | none | ✅ **ahead** — but **fail-open** (`SkippedGates` ships) |
| Prompt-cache discipline / fingerprinted sections | ✓ | ✓ | ✓ | ✓ | ✅ **ahead** (`promptsec`, sorted tools) |
| Skills (progressive disclosure) | ✓ | ✓ | ✓ | ✓ | ✅ **live** (14 embedded + workspace + pinned externals) |
| Instructions chain (CLAUDE.md/AGENTS.md) | ✓ | ✓ | rules | ✓ | ✅ **live** (64KB budget, most-specific-wins) |
| Event-sourced log / derived history | internal | sessions | checkpoints | ✓ law | ⚠️ **write-only** — derive flag off (`sessionlog_bridge.go:191`) |
| Subagents / parallel fan-out | ✓ core | agents | Explore | ✓ law | ⚠️ **code exists, flag unset** — `delegate` never registers |
| Declarative composition / hot settings | settings | config json | settings | ✓ law | ❌ `harness/composition`, `harness/settings` **dead**; env-only |
| Deterministic hooks / user extensibility | ✓ core | plugins+hooks | plugins | listeners | ❌ **absent** — guardrails are the only deterministic layer |
| Semantic code index (AST+embeddings) | — (grep+plan) | LSP | ✓ **core moat** | — | ❌ **placeholder** (`embedding BLOB`, store.go:674) |
| LSP / symbol ground truth | — | ✓ **first-class** | ✓ | — | ❌ absent (grep + CLI only) |
| Multi-provider with real failover | ✓ | ✓ config | ✓ matrix | settings | ❌ **one endpoint**, same-endpoint model swap only |
| OS process sandbox / egress control | ✓ seatbelt-class | permissions | IDE sandbox | bash-sandbox | ⚠️ tier classify + workspace cwd; **no OS isolation** |
| MCP / external tools | ✓ core | ✓ core | ✓ core | deferred §4.5 | ❌ absent (deliberately deferred — revisit) |
| Agent-behavior evals (golden tasks, replay) | internal | — | ✓ Context Bench + A/B | headless scorecard | ❌ **`make evals` tests the harness, not the agent** |
| Plan mode that can read the code | ✓ enforced+Explore | ✓ | ✓ | plan-mode pkg | ❌ **blind** — one tool-less call, no map, no files |
| Checkpoints / rewind / diff review | ✓ | ✓ | ✓ | log | ✅ partial (per-turn file pre-images + diffs) |
| Permission modes (read/ask/accept/auto) | ✓ | ✓ config | ✓ | policy | ⚠️ modes exist; **approval Seam dark** (no callers) |
| Worktree/isolated parallel mutation | ✓ | worktrees | background agents | — | ❌ children share workspace (read-only only) |
| Spend/concurrency caps on fan-out | ✓ | — | — | pool+cooldown | ⚠️ pool=2 coded, **flag off**; no spend cap |

### 1.2 H-series execution audit (why "EXECUTED" isn't)
1. **Red gate:** `make evals` fails `go vet` — copylock at `ai/client.go:222,233`
   (`cp := *c` duplicates `mu sync.Mutex`, :37), introduced by `75c6ef8`; the file is
   clean vs HEAD → CI (`.github/workflows/ci.yml:36`) is red on this commit. The
   `WithModel` comment says *"never struct-copied, so the health mutex is never
   duplicated"* directly above the struct copy — comment and code contradict.
2. **H1 law broken in prod:** spec says *"delete `TrimConversation`'s delete path — the
   current destructive trim is a replay hazard"* (`PLAN_HARNESS.md:256-258`). Reality:
   `TrimConversation(400)` still INSERT-into-`conversation_archive`-then-**DELETE**
   (`store/conversation.go:292`) after **every turn** (`api/prompts.go:1241,1349`), and
   **`conversation_archive` has no reader** — search/API never touch it. Old turns are
   written to a table nobody can read.
3. **H8 law broken:** spec requires retrieval `read_spill(locator)`
   (`PLAN_HARNESS.md:455-456`). Reality: spill fires (`orchestrator.go:1745`) and
   replaces content with a locator **no tool can read** (no `read_spill` registered;
   spill dir is outside the workspace sandbox). Oversized output is one-way.
4. **H9 partial:** `delegate_fork` (spec `:481-484`) doesn't exist; `sessionlog.Fork`
   has zero callers; `TALONSTUDIO_SUBAGENTS` unset in `backend/.env` → `delegate`,
   `list_children`, `run_workflow` **never register** in this deployment.
5. **H4 partial:** `buildApprovalSeam` and `sandbox.Escalate` have **zero callers** —
   tier-2 shell, keystores, and git push were spec'd onto the seam
   (`PLAN_HARNESS.md:357`); today everything routes through `ask_user` pauses.
6. **H12 not landed:** `harness/composition`, `harness/settings`, `harness/ralph` have
   **zero non-test imports**; `harness/goals` only in `cmd/talon-headless`; main.go still
   wires structs, not data. (Only `ai.AttachSettingsFile` — main.go:119 — shipped.)
7. **Read path never flipped:** dual-write runs; reads still replay legacy rows unless
   `TALONSTUDIO_SESSIONLOG=derive` — which is not in `.env`. The log is cost without
   benefit.
8. **Loose ends:** `run_gate` defined (`tools/verify.go:773`) but never registered;
   conversation search is escaped `LIKE`, not FTS (`store/conversation.go:274`) while
   memory search *does* use FTS5; `AI_MODEL_TIER` display override vs routing tiers is
   undocumented; +323/−27 uncommitted WIP sits in `ai/` + `orchestrator/` with an
   untracked `streaming_sink_test.go`.

### 1.3 The missing laws (concepts Talon does not yet obey)

1. **Determinism over requests.** Instructions are wishes; hooks/gates are guarantees.
   Talon has 6 guardrail rules and nothing else deterministic. *Violated by:* no hook
   system; verification is advisory (fail-open).
2. **Measure the agent, not the harness.** Cursor ships retrieval behind Context Bench
   + A/B code-retention; Talon's evals prove the machinery runs and never prove the
   agent got better or worse. *Violated by:* `make evals` = vet + unit + phase fixtures.
3. **Context isolation is the scaling primitive.** Verbose work (explore, big logs,
   review) belongs in children whose *summaries* return; Talon's main context absorbs
   everything because delegation is dark. *Violated by:* flag-off subagents; spill
   instead of child isolation.
4. **Ground the model in structure, twice.** Vectors find meaning; symbols/LSP find
   truth. Cursor: hybrid beats either; OpenCode: LSP over embeddings. Talon has grep and
   a one-shot file map. *Violated by:* placeholder embedding column; no LSP; no graph.
5. **Plan with eyes.** A plan made without reading the codebase is fiction on any
   non-empty repo. *Violated by:* plan mode = one tool-less call (`prompts.go:577-580`).
6. **Fail closed on safety and truth, fail open on convenience.** Plan-mode writes,
   secrets, egress, and "verified" claims must default-deny; Talon's verify ladder
   defaults to *ship-with-notes* (`SkippedGates` disclosed but non-blocking).
7. **Everything derived, nothing deleted.** The log is truth; storage is a cache.
   *Violated by:* trim-delete; archive-without-reader; legacy read path.
8. **Composition is data; flags are temporary.** Built-but-dark code rots silently —
   the audit found five dead packages and four dark flags. Ship it, or delete it.
9. **Extensibility without forking.** Hooks, MCP, plugin *tools*: the ecosystem adds
   capability; the core keeps the loop. Talon's plugin registry serves skills only.
10. **Budgets everywhere.** Depth, concurrency, spend, rounds, tokens — caps are
    features. Talon has `maxRounds` and quota; fan-out has a pool constant but no spend
    cap and the flag is off.
11. **One authority per resource.** OpenCode: the service owns tools/permissions.
    Talon's approval story is three mechanisms (guardrail table, ask_user, dead Seam)
    with no single seam of record.
12. **Comment = contract = test.** A comment that lies (`WithModel`) is a failing test.
    H13's Model Experience guard was designed to catch exactly this — enforce it.
13. **Isolation before parallelism.** Parallel mutation needs per-agent filesystems
    (worktrees/containers); Talon's children share the workspace, so they are locked to
    read-only forever.
14. **Replay everything.** Provider replay over recorded transcripts makes agent evals
    free and deterministic; without it, every quality question costs live tokens.
15. **Honesty markers are not policy.** `SkippedGates`/`RemainingTasks` are excellent
    *disclosure*; world-class needs the same signals wired to *blocking decisions*
    (export/release/CI).

---

## 2. The fix plan — W-phases

> Every phase: **Scope → Work → Flag → Eval gate → Risk.** Nothing flips default until
> its eval gate is green. `make evals` gains one phase-guard test per phase.

### W0 — Make the gate green (process; blocks everything)
**Scope:** the merge gate is the constitution; fix it first, plus doc-code coherence.
**Work:**
1. Fix `WithModel`/`WithRetryHook` field-by-field construction (make the comment true —
   build `Client` copies without `mu`; or embed a shared `*health` pointer). `go vet ./...` green.
2. Land or stash the dirty WIP (`ai/agentic.go`, `ai/messages.go`,
   `orchestrator/{orchestrator,session}.go`, untracked `streaming_sink_test.go`) — one
   coherent commit each; CI must run on what ships.
3. Errata block at the top of `PLAN_HARNESS.md`: status = "phases landed; flips and
   §2 laws partially unenforced — see PLAN_WORLDCLASS §1.2."
4. CI: `make evals` (vet+test) *and* `-race` both required; add a `git diff --exit-code`
   after build to catch generated drift.
**Flag:** none (behavior-preserving).
**Eval gate:** `TestPhaseW0GateGreen` — vet runs in-test via `go vet` on `internal/ai`
  (or staticcheck copylock equivalent) so a reintroduced `cp := *c` fails `make evals`.
**Risk:** low. The field-by-field copy must retain pacer/retryHook sharing semantics
  (shared pointers, not values).

### W1 — Fulfill the H-laws already written (close the audit)
**Scope:** eight verified defects, each with a spec line already demanding the fix.
**Work:**
1. **Trim → shadow** (H1 `:256-258`): `TrimConversation` stops deleting; compaction and
   retention write shadow markers the log already supports; `conversation_archive`
   becomes a read-through view (or is dropped once derive is default). `historyTurns`
   window applies to *rendering*, not to truth.
2. **`read_spill(locator)`** (H8 `:455-456`): register the tool; spill dir exposed
   through the sandbox as a read-only alias; locator line in pruned output links
   tool+byte-range; `read_file` on a spill locator returns byte-identical text.
3. **Conversation search → FTS5**: mirror the memory pattern (external-content FTS5
   over a extracted-text column maintained by triggers/append), `LIKE` path retained as
   fallback behind the same handler.
4. **`run_gate`**: register it (orchestrator-facing "run the named ladder stage now"
   tool) *or* delete the type — per the ship-or-delete law.
5. **`delegate_fork`** (H9 `:481-484`) + `sessionlog.Fork` wiring: fork seeds child from
   parent prefix; lineage recorded as events.
6. **Seam wiring** (H4 `:357`): `Escalate` becomes the tier-2 path (one-shot ask, audit
   event, `turn_approvals` as binding record — it already is for `build_apk`);
   keystores/export/git-push route through the same seam. `ask_user` remains the
   model-visible fallback when the seam is unavailable.
7. **`AI_MODEL_TIER`**: document as display-only in `routing.go` or remove — no
   undocumented env vars (settings hygiene).
8. Spill/prune retention audit: workspace cleanup + export paths exclude
   `backend/spill` (H8 risk `:467-469`).
**Flag:** each item independently (`TALONSTUDIO_TRIM_SHADOW`, etc.) where behavior
  changes; pure additions (tools) ship live.
**Eval gate:** `TestPhaseW1*`: re-derive after trim reproduces full history; spill
  round-trip byte-identical; FTS query matches LIKE results on fixture; seam produces
  exactly one audit event per grant; `run_gate` present in sorted catalog bytes.
**Risk:** trim-shadow interacts with the legacy read path — keep legacy reads intact
  until W2; shadow markers are additive (rollback free).

### W2 — Read-path cutover (finish H1)
**Scope:** the log becomes truth for reads.
**Work:**
1. Parity harness: for fixture projects, assert `deriveFromLog()` ≡ legacy
   `GetConversation` (message-for-message, tool pairing intact) across normal,
   interrupted, answered, compacted, and trimmed transcripts.
2. Flip default: reads derive; `TALONSTUDIO_SESSIONLOG=legacy` becomes the rollback.
3. Raise the read window honestly: derived reads paginate from the log (the 60-turn
   cap becomes a rendering preference, not amnesia), jump-to-turn/`conversation/search`
   read the same projection.
4. Crash repair stays load-time (already correct: `repairOnLoad`, `log.go:74`) — document
   it as the integrity gate for derivation.
**Flag:** `TALONSTUDIO_SESSIONLOG=derive` default-on after two green CI weeks.
**Eval gate:** parity harness above + replay-determinism (H1's own gate: same log →
  byte-identical derived history twice) promoted into `make evals` permanently.
**Risk:** biggest behavioral change since H1; mitigated by dual-write already running
  and legacy path retained.

### W3 — Plan with eyes
**Scope:** plan mode stops being clairvoyance.
**Work:**
1. Plan turns get a **read-only tool subset** (`list_files`, `read_file`,
   `grep_codebase`, `find_files`, `project_map`, `explore`) — mutation remains
   impossible by tool absence, not by promise (aligns with law 6: enforced, not asked).
2. Inject the **project map + fingerprint** and the **AGENTS.md chain** into
   `planPrompt` assembly (promptsec registry reused; sections fingerprinted — cache
   discipline preserved).
3. Planning rounds: bounded (`maxPlanRounds`, default 8), tool-less fallback on
   provider without native tools; `ai.Plan` JSON contract unchanged.
4. **Plan artifact:** the approved plan is written to the workspace as
   `.talon/plan.md` (durable, user-editable, checkpointed) — the DSH/Claude-Code "plan
   is a file" model; the checklist syncs both ways with it (existing reconciliation
   logic reused).
5. Plan mode gets the Explore pattern: a cheap-model survey call before drafting when
   the map fingerprint is stale.
**Flag:** `TALONSTUDIO_PLAN_TOOLS=v1`.
**Eval gate:** plan turn over a fixture repo cites a file that exists (grep the plan
  JSON for real paths); no mutating tool in the plan registry (byte-assert); plan file
  written and re-read stable; cache fingerprint changes only in appended sections.
**Risk:** plan turns get slower/costlier — bounded by round cap and the cheap-tier
  routing already in `ClassifyTurn`.

### W4 — The senses: codebase index (Cursor-class understanding)
**Scope:** replace "grep and believe" with hybrid retrieval + symbol truth.
**Work:**
1. **Indexer service** (new `internal/index`): tree-sitter parse (TS/JSX/Go first) →
   definitions/references/call-edges → three indices the literature and Cursor both
   converge on: **vector** (embeddings), **graph** (symbols/call edges), **lexical
   (BM25/FTS5 — already have the pattern)**. Chunk by syntactic unit, hash by content.
2. **Merkle-style incremental updates:** maintain per-file hashes (the workspace
   already fingerprints for `projectMapFP`); re-index only changed subtrees on
   `FSWrite` events (listener — no watcher, the repo's reconciliation-on-touch law).
3. **Embedding provider behind an interface:** API-based default; local (ONNX) opt-in;
   activate the existing `project_memory.embedding` column via **sqlite-vec** (schema
   ready, store.go:674); embed cache keyed by chunk hash (Cursor's exact move).
4. **Two new tools:** `codebase_search` (hybrid: vector + BM25 fused, reranked, returns
   paths+spans) and `codebase_graph` (callers/callees/definitions, direction+distance).
   Existing grep stays — hybrid beats either (Cursor's measured law).
5. **Memory recall upgrade:** `read_memory` = FTS ∪ vector, same fusion.
6. **Project map:** becomes a *view* over the index (never a one-shot snapshot);
   refresh on fingerprint change is automatic because the index is event-fed.
**Flag:** `TALONSTUDIO_INDEX=v1` (falls back to grep-only when off — no hard dep).
**Eval gate:** retrieval benchmark fixture (`evals/retrieval/`: queries with known
  gold paths): hybrid ≥ grep-only hit@5; incremental update after one file write equals
  full re-index (hash-equal); index build under time budget on the template workspace;
  `embedding` column populated and `read_memory` vector path covered.
**Risk:** build/deps (cgo or WASM tree-sitter, embedding provider credentials);
  mitigations: interface + flag + pure-Go lexical/graph layers ship first (graph+BM25
  alone already beat grep-only).

### W5 — Ground truth: LSP + formatter (OpenCode-class senses)
**Scope:** the language server becomes an instrument the harness listens to.
**Work:**
1. `internal/lsp`: start **typescript-language-server** (template projects) and
   **gopls** (backend) per workspace, restart-on-crash, LRU; config keys `lsp` and
   `formatter` in project config (OpenCode parity).
2. Tools: `lsp_diagnostics` (file/workspace), `lsp_references`,
   `lsp_definition`, `lsp_symbol` — cheap, deterministic, read-only.
3. Wire into verify: **static gate zero = LSP diagnostics** (instant, before
   `tsc --noEmit`), then the existing ladder unchanged; diagnostics feed the failure
   index (normalize LSP codes into `record_failure_pattern` shapes).
4. `lsp_organize_imports` / format via LSP where available; `run_prettier` remains the
   workspace formatter of record.
**Flag:** `TALONSTUDIO_LSP=v1`; missing server → structured `LSP_UNAVAILABLE` (never
  block — same fail-open-availability / fail-closed-truth split as sandbox).
**Eval gate:** fixture with a planted type error → `lsp_diagnostics` names it without
  running tsc; references resolve across files; unavailable server degrades to the
  current path with the skip recorded in `SkippedGates`.
**Risk:** process fleet per workspace (reaper must own lifetimes — reuse the device
  reaper pattern).

### W6 — Verification becomes policy (fail-closed truth)
**Scope:** "verified" must mean something you can build a release on.
**Work:**
1. **Gate policy matrix** per project: each ladder stage = `required | best-effort |
   off` (defaults: static **required**, runtime/vision/interact **required when a
   device is attached**, best-effort otherwise). Outcome gains `Verified(policy)` vs
   `VerifiedBestEffort` — the current single `Verified` becomes unreachable.
2. **Blocking consequences:** export/release/APK-ship refuses on `required` gates
   skipped or failing, with an explicit user override recorded in `audit_log`
   (fail-closed with a named human key — law 6/15).
3. **Grade scorecards enter CI:** the existing `internal/evals` `Grade*` machinery runs
   on a fixture app (clean, browser-wallet, secret-material, web-primitive cases) as a
   `make evals` stage — grader correctness gates merges; nightly `make e2e-live`
   (already scripted) runs the real stack and publishes the scorecard (W13 makes it a
   gate).
4. `SkippedGates`/`RemainingTasks` remain — now also *policy inputs*, not just prose.
**Flag:** `TALONSTUDIO_VERIFY_POLICY=strict|besteffort` (project-level), default
  `strict` for new projects after evals.
**Eval gate:** planted failure in a required gate → outcome ≠ `Verified` and export
  refused; best-effort gate missing device → `VerifiedBestEffort` + release allowed
  only with override; grader fixture suite green.
**Risk:** device-less CI can't satisfy runtime gates — hence policy classes, not a
  single global switch (best-effort keeps CI honest without lying).

### W7 — Provider router: no single point of failure (multi-provider)
**Scope:** one endpoint is one outage.
**Work:**
1. `internal/ai/router`: N provider configs (repeatable `providers` block in the
   settings document — `ai.AttachSettingsFile` already hot-reloads, main.go:119),
   health registry (the `noteFailure` machinery already tracks last error), **cross-
   provider failover** for the fallback path (today's `FallbackModel` stays
   same-endpoint per `routing.go:39-41` — add `fallback_provider`), circuit breaker
   with half-open probes, per-request provider pinning recorded in `usage/record` for
   honest cost attribution.
2. **Per-capability models:** `vision_model` separate from `build_model` (the vision
   gate currently inherits whatever serves chat); routing policy gains the key in
   `ROUTING_POLICY_JSON` semantics.
3. **Turn budgets:** max cost per turn (`ROUTING_PRICING_JSON` already prices) —
   exceed → honest stop with the spend reported (law 10).
4. Wire the full settings doc: routing policy + pricing + tier display + providers in
   one watched document; `harness/settings` either becomes that document's schema or
   is deleted (ship-or-delete).
**Flag:** `TALONSTUDIO_ROUTER=v1` (single-provider config keeps today's path byte-
  identical).
**Eval gate:** kill primary endpoint on the fake server → turn completes on secondary
  with `llm/retry` + provider pinned in usage; vision call routes to `vision_model`;
  budget exceed stops the turn with spend in the outcome; settings edit flips routing
  next turn without restart (H12's own gate, finally run).
**Risk:** cross-provider message-shape drift — mitigated because both serializers
  (`toAnthropic`/`toOpenAI`) already exist; failover only crosses protocols after
  parity tests per pair.

### W8 — Ship or delete: the dark-code reckoning
**Scope:** five dark flags and four dead packages become decisions, not limbo.
**Work:**
1. **Flip with evals, in order:** `TALONSTUDIO_PIPELINE=v1` (H3 tests already exist —
   `TestPhaseH3PipelineBound`) → `TALONSTUDIO_PATCH_BUFFER` (G6 test exists) →
   `TALONSTUDIO_SUBAGENTS` (W1 fork + W12 caps land first) → `SESSIONLOG=derive` (W2).
   Each flip = PR with before/after transcript-parity evidence.
2. **Dead packages:** `harness/composition` + `harness/settings` → W14 consumes them
   (composition becomes the boot file); `harness/ralph` → either wired as the
   fresh-context repair offer (H14 spec) or deleted with a note in PLAN_HARNESS;
   `harness/goals` → expose to server sessions ("continue the goal" after crash) or
   declare headless-only in the package doc (no silent half-life).
3. `.env.example` gains every supported `TALONSTUDIO_*`/`AI_*` var with comments —
   the flag surface becomes readable configuration, not folklore.
**Flag:** this phase *is* the flags phase.
**Eval gate:** per-flip parity tests; `TestPhaseW8NoDeadImports` — any package under
  `internal/harness` with zero non-test importers fails `make evals` (law 8 enforced by
  CI, permanently).
**Risk:** flipping subagents multiplies cost — gated by pool=2, 429 cooldown
  (failure-index), and W12 spend caps.

### W9 — One permission authority (OpenCode-class permission model)
**Scope:** modes, seam, and audit converge into one story.
**Work:**
1. **Permission modes** per session/project: `read-only | ask (default) | accept-edits |
   auto` — implemented as policy composition over the existing sandbox modes +
   approval seam + guardrails (no fourth mechanism).
2. The seam (W1.6) becomes the **single decision point**: every sensitive act
   (`tier2` shell, keystore, push, device build) is one `approval.Request` with one
   audit event; `turn_approvals` stays the binding record; UI renders the audit trail
   (`GET /v1/projects/{projectID}/audit` already serves it).
3. **Per-project command allowlist:** "always allow `npm run lint`" persisted
   (workspace `.talon/approvals.json`, hash-pinned like plugins) — turns repetitive
   ask_user pauses into recorded standing grants (law 11: one authority, one record).
4. Auto mode gets a **hard-deny class** (secrets, egress exfil, path escapes) that no
   allowlist can waive — matching Claude Code's auto hard-deny.
**Flag:** modes ship live (default = today's behavior: ask); allowlist behind
  `TALONSTUDIO_STANDING_GRANTS=v1`.
**Eval gate:** mode matrix test (read-only refuses write with structured error; ask
  produces exactly one audit event; auto allows tier-1, hard-denies secrets even with
  allowlist entry); standing grant replay on second invocation skips the prompt.
**Risk:** allowlist is prompt-adjacent trust surface — entries are exact-command +
  hash, not prefixes; workspace file is user-editable and diffable.

### W10 — Hooks: determinism as a product feature (Claude Code-class extensibility)
**Scope:** lifecycle callbacks users and we can rely on.
**Work:**
1. `internal/hooks`: event set `PreToolUse, PostToolUse, PostToolUseFailure,
   UserPromptSubmit, TurnStart, TurnStop, SessionStart, SubagentStart, SubagentStop,
   ApprovalRequest` (a subset of the existing event taxonomy — the log already carries
   most of these kinds; hooks are *subscribers with a decision*).
2. Handler types: **shell command** (stdin JSON, stdout JSON decision, exit-code
   semantics: 0 proceed, 2 block-with-stderr-to-model) and **Go plugins** (in-process).
   HTTP/MCP handlers come later behind the same interface.
3. Decision contract: `{decision: allow|deny|ask, reason?, additionalContext?}` —
   `deny.reason` feeds the model exactly like guardrail messages do today.
4. Ship with three built-ins that prove the pattern: `run-tests-after-write` (async),
   `block-.env-edits` (move the secrets class from prompt text into enforcement), and
   `slack-on-turn-fail` (the notification example).
5. Hooks config lives in project config; hook executions append to the log (auditable
   like everything else).
**Flag:** `TALONSTUDIO_HOOKS=v1`; absent config = byte-identical behavior.
**Eval gate:** blocking hook prevents dispatch and yields the model-visible reason;
  async hook runs after `Write` without delaying the round (log timestamps);
  hook failure closes the turn per DSH's law (*extension failure never corrupts the
  loop*); `TestPhaseW10HookDeny` fixture.
**Risk:** hooks are arbitrary code execution — but they are *user-authored in the
  workspace*, the same trust class as AGENTS.md and `.talon/skills` already accepted;
  document that boundary in the Model Experience doc.

### W11 — Open seams: MCP + plugin tools
**Scope:** the ecosystem extends the agent without forking Talon.
**Work:**
1. **MCP client** (revisit PLAN_HARNESS §4.5's "if a real need appears" — the need is
   now standard): stdio + HTTP transports, tool list merged into the registry under an
   `mcp__` namespace, namespaced schemas, per-server allow/deny, OAuth where remote.
   MCP tools flow through the **same** pipeline (policy → guards → dispatch → inspect) —
   no second execution path.
2. **Plugins gain tools** (today the registry serves skills only): a pinned plugin may
   declare tools with JSON schemas; they register per-workspace like
   `read_plugin_file` does now; verification pins + cache stay (main.go:148-153).
3. MCP/plugin tools are **read-only by default**; mutating MCP tools require explicit
   allow + sandbox mapping.
**Flag:** `TALONSTUDIO_MCP=v1` per workspace config.
**Eval gate:** a fixture MCP server's tool appears sorted in the catalog (byte-stable),
  executes through the pipeline with guardrail coverage, denied by policy without
  disappearing from the schema (H3's catalog-stability law); plugin tool registers only
  for pinned workspaces.
**Risk:** supply chain — mitigations: pins+hashes (existing), read-only default, same
  guardrails, namespacing.

### W12 — Isolation & parallelism (make delegation world-class)
**Scope:** when W8 flips subagents on, they must be *safe* to scale.
**Work:**
1. **Mutating children get isolation:** child runner gains a write overlay —
   **git worktree** per child (Talon's own repo is git; workspaces are git) or
   copy-on-write overlay dir with a merge-back step (diff → apply → verify). Read-only
   children keep today's shared-workspace path (zero cost).
2. **Caps (law 10):** depth 1 (already enforced twice — keep), max concurrent children
   (pool, composition row), **max spend per fan-out turn** (prices from
   `ROUTING_PRICING_JSON`), max wall-clock; 429 cooldown already spec'd in H9 — wire
   the failure-index pattern.
3. **Background children:** `delegate` defaults to background (H9 spec) with
   `list_children` durable/ready status — the trace renders a "Delegated" section
   (H9 frontend tail).
4. **Workflows over children** (`run_workflow`) flip on with W8 once caps exist;
   narrated as `task_update` events (engine already does this).
**Flag:** `TALONSTUDIO_CHILD_ISOLATION=worktree|readonly` (default `readonly` first).
**Eval gate:** two mutating children produce disjoint diffs with no lost update
  (parallel write fixture); spend cap stops a runaway fan-out with the partial report
  in the parent result; worktree cleanup on child dispose; background child status
  transitions visible in `list_children`.
**Risk:** worktree-per-child costs disk and git plumbing; merge conflicts surface as
  structured apply errors (H5 version stamps already model this class).

### W13 — Agent evals: measure the agent (the Cursor law)
**Scope:** quality becomes a number that gates changes.
**Work:**
1. **Recorded-provider replay harness:** record `(system, messages, tools) → response`
  turns to fixtures on the fake provider (the repo already fakes providers in evals);
  golden transcripts = scripted prompts with **expected tool sequences and side
   effects** (file diffs), replayed deterministically — free, offline, CI-able.
2. **Golden task suite** (`evals/tasks/`): scaffold-weather-app, plan-existing-feature,
   fix-broken-import, wallet-connect-flow, steer-mid-build, resume-after-crash — each
   with pass criteria expressed over the log + workspace diff + grade scorecard.
3. **Agent metrics** (extend `metrics.go`): rounds-to-green, tool error rate,
   guardrail reject rate, spill/prune incidence, context-pressure trajectory, cost per
   verified build — surfaced per session (H13 token meter already built the UX tail).
4. **Retrieval bench** (with W4): gold-path query set, hit@5 tracked over changes.
5. **CI gates:** prompt/tool/guardrail changes require golden-transcript review (diff
   the expected transcript in the PR — prompts become reviewable behavior); nightly
   `make e2e-live` scorecard published; regression in rounds-to-green > threshold
   blocks merge of the offending change.
**Flag:** n/a — evals are always on (the phase's deliverable *is* the gate).
**Eval gate:** the suite itself: replay determinism (same fixture → same transcript
  twice), suite runtime budget (< 3 min for golden set), and a mutation test (deliberate
  prompt regression flips a task red).
**Risk:** brittle goldens — express criteria over *invariants* (tool order classes,
  files touched, outcome kind) not byte-equality of prose.

### W14 — Finish the loop extraction (H2 end-state, composition as data)
**Scope:** the architecture the plan promised, actually booting.
**Work:**
1. Extract `RunAgentLoop`'s cross-cutting calls into listeners per H2/§2.4 table
   (guardrails → `ToolPolicy`, failure reminders → `ToolResult`, auto-attach →
   `ToolDispatch`, verify cadence → `FSWrite` counter, emitter → subscribe). The loop
   file keeps: claim → pre-step → request → append → dispatch → append → close.
2. `main.go` becomes a **composer** reading `talon.composition.yaml` (base → profile →
   preset, last-write-wins per id) — `harness/composition` and `harness/settings` get
   their only acceptable fate: consumed or deleted (W8.2).
3. `--profile headless` boots the **real composition** under the fake provider and
   prints the scorecard (H11's promise; also W13's nightly runner) — CI's third job.
4. `--dump-config` golden diff in CI (H12 risk mitigation, finally run).
5. Model Experience docs guard (H13.1): every tool and pipeline stage README has a
   "What the model sees" section — enforced by a `make evals` test, which also catches
   comment-code lies of the `WithModel` class (law 12).
**Flag:** `TALONSTUDIO_LOOP=driver|legacy` wraps-then-replaces (H2's own migration);
  composer ships alongside struct wiring until `--dump-config` parity is proven.
**Eval gate:** legacy vs driver produce identical transcripts on the fake provider
  (H2's gate); adding a guardrail = new file + one composition row with zero edits to
  existing files (DSH extensibility test, as a test); headless scorecard green twice
  consecutively in CI; config golden diff clean.
**Risk:** largest refactor of main.go — sequenced last because W0-W13 make every
  behavior it moves independently testable.

---

## 3. Sequencing

```
W0 gate green ─────────────┬─> W1 H-law fixes ─> W2 derive cutover ──────┐
                           │        ├─> W3 plan-with-eyes               │
                           │        └─> W9 permission authority          │
                           ├─> W13a replay harness (early: feeds all)    │
                           │                                             v
W4 index (lexical+graph → vectors) ──> W5 LSP ──> W6 verify policy ──> W13b golden tasks
                           │
W7 router (independent) ───┤
W10 hooks (after W1 seam vocabulary)
W11 MCP/plugins (after W10 decision contract)
W12 isolation caps ──> W8 flip subagents/pipeline/patch-buffer/derive
W14 loop extraction + composition (last; consumes everything)
```

**Tracks:**
- **Track S (safety & truth):** W0 → W1 → W2 → W6 → W9
- **Track X (senses):** W4 → W5 (parallel to S)
- **Track E (extensibility):** W10 → W11 → W12 → W8
- **Track M (measurement):** W13a early, W13b after W4/W6, nightly always
- **Track A (architecture):** W3, W7 anytime; W14 last

---

## 4. Definition of done (v2 — measurable)

1. `make evals` green **and** contains: vet gate, parity/replay determinism, gate-policy
   matrix, retrieval hit@5, golden-task suite, no-dead-imports, config-golden diff.
2. Every audit item in §1.2 has a landed PR referencing its W-phase (register below
   reads "closed").
3. Plan turns cite real files (fixture assertion); no plan-path mutation is *possible*
   (registry byte-assert).
4. "Verified" is unreachable without policy evaluation; release refuses required-gate
   failures without a logged human override.
5. A provider outage completes turns on a second provider, honestly priced and logged.
6. Spill/prune/trim are lossless-to-the-model (round-trip tests) — nothing the harness
   stores is unreachable by the agent or the user.
7. Delegation runs in prod config under spend/concurrency caps with disjoint-write
   isolation; workflow narrates into the trace.
8. Hooks can block a tool deterministically; a workspace ships three built-ins; MCP
   tools flow through the same guarded pipeline.
9. A prompt change without a golden-transcript diff cannot merge.
10. The loop file contains zero domain knowledge; composition is data; `harness/*`
    packages with zero importers fail CI.

## 5. Audit-item → phase register (nothing hangs)

| # | Verified finding | Closed by |
|---|---|---|
| 1 | `make evals` red — copylock `client.go:222,233`; lying comment | **W0** ✅ landed (`cb80d59`) |
| 2 | Uncommitted AI-core WIP (+323/−27) | **W0** ✅ landed (`f517661`) |
| 3 | `PLAN_HARNESS.md` status overstated | **W0** ✅ landed (`cb80d59`) |
| 4 | Trim deletes history; archive write-only | **W1.1** ✅ landed — trim shadows instead of deleting (flag `TALONSTUDIO_TRIM_SHADOW`); archive recovered at boot then dropped (gate: `TestPhaseW1TrimShadow`) |
| 5 | Spill one-way (no `read_spill`) | **W1.2** ✅ landed — via `read_file` locator resolution (repo-native; see notes) |
| 6 | Conversation search = LIKE not FTS | **W1.3** ✅ landed — FTS5 ∪ LIKE union behind the same handler (gate: `TestPhaseW1SearchFTS`) |
| 7 | `run_gate` defined, unregistered | **W1.4** ✅ landed (`ef736f3`) |
| 8 | `Fork`/`delegate_fork` unused | **W1.5** ✅ landed — `delegate_fork` registered behind `TALONSTUDIO_SUBAGENTS`; `sessionlog.Fork` production-wired (child log = parent prefix + `session/forked` lineage in both logs; gate: `TestDelegateForkSeedsChildFromParentPrefix` + `TestPhaseW1DelegateFork`); **W12** |
| 9 | Approval Seam/`Escalate` dead; tier-2 = ask-only | **W1.6** ✅ landed — tier-2 shell escalates through `Capability.Escalate` → the seam (one strictly-wider ask, one audit event per decision, `turn_approvals` binding via the new `tier2_shell` consent scope); push/export audit through the same seam; root cause of "dead" found and fixed: `newBuildSession` returned a fresh struct that dropped sandbox/promptID/seam, so the H4 ring and build receipts were dark too (gate: `TestPhaseW1SeamWiring` + `TestTier2ShellGrantFlowAuditsOnce` + `TestRunShellTier2SeamOutcomes`); **W9** |
| 10 | `AI_MODEL_TIER` undocumented | **W1.7** ✅ no change — already documented in `.env.example:27-29` (audit error) |
| 11 | Event log write-only (derive flag off) | **W2** — parity harness ✅ landed (`api/TestPhaseW2ReadParity`: 7 shapes incl. replay-determinism; found and closed 4 projection gaps — turn_end status/error/rounds, plan block, tool_result name, doc attachment refs); **W2.4** ✅ (`repairOnLoad` documented as the derivation integrity gate); **W2.2** flip (deferred: plan requires two green CI weeks) + **W2.3** derived pagination remain |
| 12 | Plan mode blind (no tools/map/instructions) | **W3** ✅ landed — W3.A read-only tool loop + **W3.2** per-project prompt (project map + AGENTS.md chain via promptsec, sections fingerprinted, one attached chain factory shared with the orchestrator) + **W3.5** cheap-model explore survey on fingerprint staleness (cached digest, failure degrades and retries, never fails the turn), all behind `TALONSTUDIO_PLAN_TOOLS=v1`; gates: `TestPhaseW3PlanTurnSeesFixtureRepo` + `TestPhaseW3PlanRegistryReadOnly` + `TestPhaseW3PlanRoundCap` + `TestPhaseW3PlanToollessFallback` + `TestPhaseW3PlanStreamDoorWithEyes` + `TestPhaseW3PlanPromptMapAndAgents` + `TestPhaseW3PlanPromptSectionLocality` + `TestPhaseW3PlanSurveyWhenStale` + `TestPhaseW3PlanSurveyFailureDegrades` (+ flag-off `TestPlanModeSendsNoToolsToModel` intact); artifact = row 13 |
| 13 | Plan not a durable artifact | **W3.4** ✅ landed — `.talon/plan.md` written at plan save (deterministic render under a sha256 marker; pre-image checkpointed → `RestoreCheckpoint` undoes creation/re-save; byte-identical re-save = no write, no checkpoint), backfilled/refreshed at build start, two-way synced: flips mirror in through the persistence hook (the one place all status changes flow — `taskPersistence` extracted so tests wire the real path), user checkbox edits import back via `store.ImportPlanTaskStatus` (deliberately unconditional — a human reopening work — and iteration walks rows so the user's own items never become tasks; reworded lines import nothing rather than the wrong row); single derivation `orchestrator.PlanTaskRows` feeds store, prompt checklist, and file; behind `TALONSTUDIO_PLAN_TOOLS`; gates: `planfile` pkg tests (render stability/edit detection/sync-preserves-text) + `api/TestPhaseW3PlanArtifactWrittenAndStable` + `orchestrator/TestPlanArtifact{SeedFlipRoundTrip,UserCheckImportsAtSeed,MidTurnImportOnFlip,FlagOff}` |
| 14 | No semantic index; `embedding` placeholder | **W4** — **W4.1** ✅ landed — `internal/index` (pure-Go `Analyze` seam: column-0 decls, imports, name-matched calls → content-addressed chunks/symbols/edges) + FTS5/bm25 lexical layer + graph queries (`code_files`/`code_chunks`(+fts)/`code_symbols`/`code_edges`, `project_memory_fts` trigger pattern) + reconcile-on-touch (`Reconcile` diffs per-file content hashes — only changed paths re-parse, deletes clear edge targets so state hash-equals a fresh build), behind `TALONSTUDIO_INDEX` (dark: off = grep-only, no hard dep); gates: `index/TestReconcileIncrementalEqualsFull` (one-file write digest == fresh full build) + `TestReconcileTemplateTimeBudget` (cold <10s, warm re-indexes 0) + `TestReconcileFlagOff` + `store/TestCodeIndexRoundTrip` (incl. FTS injection quoting + trigger delete) + extractor/chunk/resolve tests; **W4.2** folded (Reconcile's diff IS the Merkle-style increment — no watcher, per the plan's own law); **W4.4** ✅ landed — `codebase_search` (bm25, reconcile-on-query, grep-shared `path:line` output) + `codebase_graph` (definitions/callers/callees, distance 1–2, new `CodeEdgesFromSymbol`) registered flag-gated in the build session with the `StoreAdapter` fn-field law (tools never import store), all name-based switches wired (`coreToolSet`, read-timeout budget, probe list/`probeTarget`/`toolLabel`/`toolTarget`, probe-test rows), + **`evals/retrieval/`** gate: indexed ≥ grep-only **hit@5** over 8 gold-path queries (≥6 floor so `0≥0` can't pass; end-to-end through both real tools over one shared parser) + graph E2E pin; **W4.3** ✅ landed — provider behind interface (`internal/embed`: `Provider` + API default against any OpenAI-compatible `/v1/embeddings`, `FromEnv` nil-when-unconfigured, ONNX = local slot unimplemented by design) + embed cache keyed by chunk hash (pre-swap carry in `ReplaceFileIndex` + NULL-filling sweeps `EmbedCodeChunks`/`EmbedMemories` — dormant `project_memory.embedding` now populates; LE-float32 BLOB format pinned by round-trip; `CodeIndexDigest` excludes vectors) + background sweep at build (dormant without env); gates: `embed` request-shape/reordering/batching/rejection units + `FromEnv` dormancy, `store/TestReplaceFileIndexCarriesEmbeddings` + `TestEmbeddingExcludedFromDigest` + `TestEmbedCodeChunksSweepIdempotent` + `TestEmbedSweepFailureLeavesRowsNull` + `TestEmbedMemoriesSweepIdempotent`; **W4.5** ✅ landed — `read_memory` FTS ∪ vector via shared **`internal/recall`** RRF fusion (`K=60`, lexical-list-first ties): store cosine legs (`SearchMemoryVector`/`SearchCodeChunksVector` — brute-force over `VecFromBytes`, dims/zero-norm rows skipped, never errored) + item 4's deferred hybrid half (`codebase_search.VectorSearch`, honest `[vector leg unavailable]` note), provider seam `embedQuery` (nil env → byte-identical lexical behavior everywhere), fusion pinned end-to-end through both tools' real Execute; gates: `recall/TestFuse*` + `store/TestSearchMemoryVector*`/`TestSearchCodeChunksVector`/`TestCosineUnit` + `tools/TestReadMemory*{Fuse,VectorOnly,VectorOff,VectorError}*`/`TestCodebaseSearch*Vector*`; **W4.6** ✅ completed the remainder (map view, row 15) — **W4 complete** |
| 15 | Project map one-shot snapshot | **W4.6** ✅ closed — the map renders as a view over the index (`projectMapView`/`ProjectMapTextFromIndex`: reconcile-on-read + shared `renderMap` + shared `ExtractExports`/`ExtractImports` — byte-identical to the walk for one tree); the fingerprint stays only the prompt-cache trigger; `refreshProjectMap` prefers the view; fetch/refresh failure degrades to the walk (flag-off bytes verbatim) |
| 16 | Memory FTS-only recall | **W4.5** ✅ closed — `read_memory` fuses FTS + cosine vectors through `recall.Fuse` (vector leg optional: no provider → FTS exactly as before; provider error → degrade, lexical results intact) |
| 17 | No LSP/symbol ground truth | **W5** ✅ landed — `internal/lsp`: typescript-language-server/gopls per workspace behind `TALONSTUDIO_LSP` (dark: unset = no tools, no rung, registry byte-identical), device-reaper-fleet (LRU cap 4, idle reaper, restart-on-crash, kill-group lifetimes), config keys `lsp`/`formatter` in `.talon/config.json` (OpenCode parity), fake stdio server = test-binary re-exec (no real servers/network in CI); tools `lsp_diagnostics`/`lsp_references`/`lsp_definition`/`lsp_symbol` + `lsp_organize_imports` (W5.4 — server proposes, FS applies journaled) behind the `LSPConn`/`LSPResolve` seam, all name switches wired (`coreToolSet`, read budget, write budget, probe list/`probeTarget`/`describeTool`/`toolTarget`); **gate zero** prepends the lsp rung only when the flag is on — unavailable → structured `LSP_UNAVAILABLE` → `SkippedGates`, ladder continues to tsc (fail-open-availability), and `FormatTsc` renders tsc's own shape so `NormalizeError` + seeded `ts\d+` patterns match with no second rule set; **failure-index feed**: failed verify *verdicts* now consult `DefaultFailureIndex` (was error-only), run_typecheck-scoped seeds consulted by SHAPE on the verdict path, recorded patterns normalized at load (`runtimePattern`); `run_prettier` stays formatter of record with the `formatter` key swapping the binary (flag-gated); gates: `lsp/` (handshake, cross-file refs, tsc byte-exact shape, crash/restart, LRU/reaper, flag-off), `tools/` (verdict shape, 1-based positions, unavailable, sweep, organize-through-FS, formatter flag), `orchestrator/lspgate_test` (flag-gated ladder, unavailable→`SkippedGates` continues, failure names file without tsc) + `failurefeed_test` (verdict consult, cooldown, recorded-pattern normalization); honest: fake server stands in for real tsserver/gopls in CI |
| 18 | Verify fail-open; `SkippedGates` non-blocking | **W6** ✅ landed — gate policy matrix per project: `required\|best-effort\|off` via `.talon/config.json` `verify_gates` (defaults: typecheck/lint/bundle/gradle required; boot/vision/interact required iff `RunOptions.DeviceAttached` else best-effort — set from `adbTargetFor` at the prompts RunOptions and `adbTarget != ""` in the fork child; lsp best-effort per W5's never-a-block law, promotable; unknown → required fail-closed; invalid entry degrades to default, never off) + policy resolution `verify_policy` → `TALONSTUDIO_VERIFY_POLICY` → strict (unrecognized → strict, logged — a typo in a safety flag must not mean permissive); outcome gains `VerifiedBestEffort`/`VerifyPolicy` so bare `Verified`-as-unannotated is unreachable: no skips → `verified`, best-effort-class skips → `best-effort` (keeps `Verified=true`, annotated — protects evals `sample.Verified`), required skip under strict → `refused` (`Verified=false` + a "Verification policy:" line in FinalText), and the `maxVerifyAttempts` exit records `refused` with the repair-budget reason; `off` stages filtered out of `verify()` (never run, never recorded as skipped — default ladders unchanged); G4 passed+unavailable summary stays a **pass** (skip classification only sees `SkippedGates`); durable shadow `project_verify_state` (one row per project, newest sweep wins, nil slices → `[]`, `recordVerifyState` nil-store-safe and never failing the turn); shared `orchestrator.VerifyReleaseRefusal` (no record / refused / best-effort → refuse naming the reason/skips/pending tasks; verified → ship) enforced by `enforceVerifyPolicy` in export `Create` **before** the approval seam (403 + reason; `override:true` = the named human key → ship + one `verify_override` audit row per attempt, no dedup) and by the `build_apk` guardrail (deny only on recorded `refused`; the device_build approval doubles as the override key → allow + audit row per use; never-verified stays fenced by the existing scope rule); additive wire fields `verified_best_effort`/`verify_policy` (JSON response map + SSE finish payload), `outcomeLabel` gains `verified-best-effort`; gates: `orchestrator/verify_policy_test` (precedence + invalid→strict, class/config matrices, classify table incl. off-stage-never-refuses, off-filter through `o.verify`, full-loop best-effort/required-skip/ladder-failure with store assertions) + `store/verify_test` (roundtrip/upsert/not-found) + `api/export_verify_test` (refused/best-effort/no-record → 403 zero-audits, override → ship + exactly-one audit row, verified → clean ship, Create-level 403 short-circuit before seam/job) |
| 19 | Graders exist but out of CI | **W6.3** ✅ landed — named `scorecard` stage in `make evals` (`evals: vet scorecard test` → `go test -run 'TestFixtureScorecard\|TestGrade' ./internal/evals/`) pinning the four Grade fixtures: clean → `Passed()`+1.0, browser-wallet → critical `no-browser-wallet`, secret-material → critical `no-secret-material`, web-primitive → non-critical `native-components-only` with `Passed()` still holding — grader correctness now gates every merge; **W13.5** ✅ — `make nightly` → `e2e/run-live.sh` exports `E2E_REPORT` (default `e2e/reports/nightly-latest.md`, gitignored) and `cmd/e2e`'s `report()` writes the scorecard markdown there on every exit path — nightly publishes durably, locally, under the secret-free CI |
| 20 | Single provider/endpoint; same-endpoint fallback | **W7** ✅ landed — providers block in the one watched settings document (`providers: [{name, protocol, base_url, api_key, model}]`), health registry + three-state circuit breaker (open → half-open single probe → closed; threshold/cooldown as test-overridable vars), cross-provider failover via `routing.fallback_provider` with the model+endpoint latched together on success, round-start skip of an open primary (`SelectStartProvider`), provider pinning in `usage/record` + `llm/retry` (`Provider` fields, empty flag-off) + `outcome.RoutedProvider`, all behind `TALONSTUDIO_ROUTER` (truthy `1\|true\|on\|v1`; flag off byte-identical); **W7.4** ✅ — one four-section settings document (routing + pricing + providers + model_tier) behind `ai.AttachSettingsFile` (extras-only docs valid, last-good/mtime discipline shared); gates: `orchestrator/TestRouterFailsOverAcrossProviders` (failover + latch + breaker round-start skip + pinned events) + `ai/TestBreakerStateMachine` + `TestSelectStartProviderSwitchesOnlyWhenOpen` + `TestProvidersAndClientForProvider` + `TestLoadPricingDocOverlay` + `TestTierOverridePrefersDocumentOnlyWhenRouted` |
| 21 | No per-capability (vision) model | **W7.2** ✅ landed — `routing.vision_model` served through `ai.ForVision` at both vision seams (capture_screenshot's `visionAdapter` and the G4 `gateVision`); flag off or unset = the chat model, byte-identical (gate: `api/TestVisionGateRoutesToVisionModel` — body.model asserted on-wire both ways) |
| 22 | No turn spend cap | **W7.3** ✅ landed — `routing.max_turn_cost_usd` prices each round (served model, document-over-env `ai.LoadPricing`) and stops at the top of the next round naming spend and cap in FinalText, `outcome.CostUSD` + `BudgetStop`, nil error (deliberate like Exhausted, all finished rounds flushed first), no second request dispatched; wire keys `cost_usd`/`routed_provider`/`budget_stop` ride response + SSE finish only when routed (gate: `orchestrator/TestRouterBudgetStopsTurnHonest` — one request, `$` in the text, CostUSD ≥ cap) |
| 23 | Dark flags: PIPELINE / PATCH_BUFFER / SUBAGENTS / derive | **W8.1** ✅ PIPELINE + PATCH_BUFFER flipped default-on (rollback `0/off`; gates: `TestPipelineTranscriptParity`, `TestPatchBufferDefaultOn`, orchestrator/api/evals/tools suites green); **deferred by gate:** SUBAGENTS → W12 caps first (cost multiplication risk, plan :422), `SESSIONLOG=derive` → two green CI weeks (W2.2, rollback documented in `.env.example`) |
| 24 | Dead packages: composition / settings / ralph / goals | **W7.4** ✅ `harness/settings` deleted — consumed by the one watched settings document (`ai.AttachSettingsFile`, four sections: routing/pricing/providers/model_tier; H12's settings-flip gate moved to turn level: `orchestrator/TestSettingsEditFlipsRoutingNextTurn`); **W8.2** ✅ `harness/ralph` deleted with note in PLAN_HARNESS §H14 (zero importers, no W-phase consumer), `harness/goals` declared headless-only in its package doc (sole importer `cmd/talon-headless`), law test `evals/TestPhaseW8NoDeadImports` now enforces fate-per-package (composition exception = staged boot file); **W14** remains for composition |
| 25 | Flag surface undocumented (`.env.example` gaps) | **W8.3** ✅ audited every `Getenv`/`LookupEnv` read (non-test) against the union of root + `backend/.env.example`: added `AI_MAX_ROUNDS`, `ROUTING_POLICY_JSON`, `ROUTING_PRICING_JSON`, `TALONSTUDIO_HEADLESS_PROMPT`, `TALONSTUDIO_SANDBOX`, `TALONSTUDIO_TOOL_POOL`, `TALONSTUDIO_PIPELINE`, `TALONSTUDIO_PATCH_BUFFER`; harness-flags header rewritten (two flags default-on since W8.1); embed trio confirmed read via constants (`Getenv(EnvURL)` class), not folklore. Residual undocumented = deploy secrets outside plan scope (AUTH_* endpoints, R2_*, keystore, TALON_HOME/TALON_PLUGINS) |
| 26 | Three approval mechanisms, no single authority | **W9** ✅ modes live, default `ask` = today byte for byte: `read-only \| ask \| accept-edits \| auto` composed over the existing sandbox+seam+guardrails (no fourth mechanism) — session override via additive `permission_mode` request field over `.talon/config.json "permission_mode"`, invalid/absent collapses to ask. The seam is the single decision point: it now also gates the **device-build** act (`RunOptions.Approval` → one `approval.Request`, one audit event, `turn_approvals` still the binding record; nil seam = pre-W9 table read unchanged). Hard-deny class injected as `Seam.HardDeny` (adapter over the shell's `tierBlocked` — secrets/egress/host, one classifier, never waivable, checked before auto AND before any standing grant); human button presses (push/export/device-build) are never auto-allowed. Gates: `approval/TestModeMatrix`, `api/TestSessionCarriesPermissionMode`+`TestProjectConfigAndOverridePrecedence`+`TestReadOnlyProjectRefusesUserActions`, `orchestrator/TestDeviceBuildGateAuditsThroughSeam`+`TestDeviceBuildGateComposesMode` |
| 27 | No standing grants (repeated ask_user pauses) | **W9.3** ✅ behind `TALONSTUDIO_STANDING_GRANTS` (truthy set; unset = today's one-shot contract, file never read/written): an `AllowedOnce` for a model-initiated scope records `{scope, action, sha256(action)}` into workspace `.talon/approvals.json` — exact scope+action match only (never prefixes), hash-pinned like plugins (tampered → fails closed to a prompt), user-editable/diffable; the next identical ask replays without prompting with a `standing-grant replay` audit reason. Human button scopes are never recorded or replayed; the hard-deny class never consults a grant. Gate: `approval/TestStandingGrantLifecycle` (record→replay, flag-off parity, tamper, human scope, hard-deny-beats-grant) |
| 28 | No hooks / deterministic user extensibility | **W10** ✅ behind `TALONSTUDIO_HOOKS` (truthy set; absent = no config read, byte-identical): `.talon/config.json "hooks"` declares callbacks over the ten-event lifecycle (`PreToolUse, PostToolUse, PostToolUseFailure, UserPromptSubmit, TurnStart, TurnStop, SessionStart, SubagentStart, SubagentStop, ApprovalRequest`) with the decision contract `{decision: allow\|deny\|ask, reason?, additionalContext?}`. Handlers: shell commands (stdin JSON payload, stdout JSON decision; exit 0 = proceed, exit 2 = block with stderr as the reason) and in-process built-ins — `block-env-edits` (sync gate on path mutators, templates allowed), `run-tests-after-write` (async, needs `command`), `slack-on-turn-fail` (async, needs `webhook`). Sync decisions gate (PreToolUse deny → model-visible tool_result; ask → approval seam, fail-closed without one; failure → latch → turn closes at the next boundary with `HookStop`, honest text, nil error — extension failure never corrupts the loop); post-event feedback rides the tool result as labeled context; async entries never gate and never delay (spawned from `Check`, ceiling-capped). `ApprovalRequest` sits after policy-never (absolute) and never grants under read-only or hard-deny; `UserPromptSubmit` can refuse a prompt before storage. Every execution audits: session-log `hook/exec` rows + always-on server log. Gates: `evals/TestPhaseW10HookDeny` + `TestPhaseW10AsyncHookRunsAfterWrite` + `TestPhaseW10HookFailureClosesTurn` (deny-dispatches / async-by-log-timestamps / failure-closes-turn against a real loop), `hooks/TestLoad*` + `TestCheckDecisionContract` + built-ins |
| 29 | No MCP; plugins serve skills only | **W11** ✅ behind `TALONSTUDIO_MCP` (truthy set; absent = no config read, byte-identical): `.talon/config.json "mcp.servers"` declares stdio (argv + env) and http (url + headers, `${ENV}` expanded) servers with per-server `allow`/`deny`/`mutate` lists; each dialled server's tools join the ONE registry as `mcp__<server>__<tool>` (dialed + listed at session construction — one dead server skips with a log line + its stderr tail, never the session, never the native catalog) and ride the shared pipeline unchanged: validateArgs → guard stage → dispatch → executor posture ring. **H3 pinned:** denied tools stay advertised byte-identically across a policy flip and refuse at call time with the policy reason (`api/TestPhaseW11MCPCatalogStable` marshals defs across a deny flip — byte-equal). **Read-only by default:** mutation needs the workspace's explicit `mutate` allow AND a write-capable posture — permission_mode read-only or `TALONSTUDIO_SANDBOX` read-only both refuse at the executor (before the path ring; read-only-classified MCP tools still run). **Plugin tools (item 2):** pinned manifests may declare `tools` (JSON schema + direct-exec argv); `skills.RegistryProvider.PinnedTools` materializes only sha256-verified bytes into the cache dir (pins+hashes = the supply-chain boundary), registers `plugin__<plugin>__<tool>` per-workspace like `read_plugin_file` — read-only always, a `mutating` declaration skips loudly (no workspace allow path for plugins yet — honest deviation), unpinned workspaces get none. Transports: stdio subprocess in its own process group (Close = group SIGKILL incl. wrapper children; 15s bounded handshake; stderr tail rides dial errors), http JSON or single-event SSE replies. Supply-chain: namespacing (native never shadowable), policy refusal-not-hiding, schema re-guarded at the tools layer. Gates: `evals/TestPhaseW11MCPThroughPipeline` (sorted byte-stable catalog + result lands in the conversation + validateArgs refuses pre-dispatch with zero server calls) + `TestPhaseW11MCPGuardCoverage` (guard stage denies an MCP call: model-visible reason, `guardrail_reject` emitted, `hook/exec` audited, fixture untouched), `api/TestPhaseW11MCPCatalogStable` + `TestPhaseW11PluginToolPinnedOnly`, `mcp` package tests (config validity/derivation/env-expansion, http round-trip + SSE decode, stdio via test-binary fixture, dial-failure stderr tail), `skills/TestPinnedTools*` (pins gate, tamper fails one plugin closed only, cache-only fallback), `tools/TestMCPTool*` + `TestPluginTool*` (namespace, deny-stays-listed, stdin/stdout contract, stderr failures) |
| 30 | Children share workspace → locked read-only | **W12.1** ✅ `TALONSTUDIO_CHILD_ISOLATION` (default `readonly` = byte-identical: read-scoped children on the shared workspace; a `mutating:true` spawn refuses `DELEGATION_ISOLATION` *before* reserving a slot); `worktree` gives each mutating child a private detached `git worktree add --detach` checkout with merge-back in three stages — capture (per-worktree index `git add -A` + binary patch so creations ride), apply (`git apply --binary`; a conflict is a structured `CHILD_WORKTREE: merge apply` error carrying the patch, parent state preserved — the lost-update class surfacing, never winning), verify (reverse `--check` proves it landed) — with cleanup on every path (success, conflict, failure, early exit: discard never merges a failed child's partial state), the untracked `.talon` config surface carried in (untracked files never reach a checkout), and a per-workspace lock serializing parallel merge-backs. Child builds now **Remove** the four delegation tools from the registry (depth-1's second wall was unadvertised-but-executable — law 12 fixed). Gates: `api/TestPhaseW12MutatingChildrenIsolated` (readonly refusal + non-git structural refusal + two parallel mutating children → disjoint diffs, both `shared.txt` updates survive — no lost update, registration+directory cleanup), `worktree` package (conflict/discard/no-change/.talon-fidelity) |
| 31 | No fan-out spend/concurrency caps in prod | **W12.2–W12.4** ✅ depth 1 (unchanged) + pool 2 (unchanged) + **spend cap** (`TALONSTUDIO_CHILD_SPEND_CAP`, default $2/turn, 0=unlimited: each child's `CostUSD` from `ROUTING_PRICING_JSON` accumulates on the Service, whose lifetime *is* the turn) + **wall-clock** (`TALONSTUDIO_CHILD_WALL_SECS`, default 600) + the H9 429 cooldown — every cap a structured `DELEGATION_*` refusal at the reserve seam (shared by sync and background paths), each keyed into the failure index (`knowledge/TestLookupDelegationRefusals` — the delegation patterns precede the generic throttle so "just retry" never contradicts a cooldown/cap). **Background children (W12.3):** `delegate` defaults to background — `SpawnBackground` reserves synchronously (refusals immediate, running state visible at once), runs detached, and lands report/rounds/error durably on the record where `list_children` collects them framed as data. **W12.4:** `run_workflow` rides the same Service, so pool/spend/wall bound it by construction (narration unchanged). Gates: `subagent/TestSpendCapStopsRunawayFanout` + `TestWallClockBoundsChild` + `TestSpawnBackgroundDurableReport` + `TestMutatingChildRefusedUnderReadonlyPosture`, `evals/TestPhaseW12SpendCapStopsRunawayFanout` (partial report in the parent result: phase 1 lands, phase 2 all-null under the cap, next spawn `DELEGATION_SPEND`), `evals/TestPhaseW12BackgroundStatusVisibleInListChildren` + `tools/TestDelegateDefaultsToBackgroundAndListCollectsReport` (running → done + report through the tool) |
| 32 | No agent-behavior evals / golden transcripts | **W13.1–W13.4** ✅ — `internal/evals/golden`: six recorded tasks replay through the real `RunAgentLoop` (real registry/guard/hooks/FS) against sha256-digested raw request bodies (temp root normalized to `<workspace>`); system prompts = production consts, so prompt edits trip digests; transcripts pin invariants only (task/outcome/rounds/tools/files_touched/request digests — never prose). Three green clauses: `TestGoldenSuite` (criteria + <3min), `TestGoldenReplayDeterminism` (byte-identical double replay), `TestGoldenMutationDetection` (prompt-drift + behavior-regression reds must go red). Re-record only via `TALONSTUDIO_UPDATE_GOLDEN=1` (never in CI; tree guard proves no self-rewrite) → behavior change = a reviewed `*.golden.json` PR diff. **W13.3** ✅ five observers + 14 Prometheus families (rounds-to-green, tool-error, guardrail-reject, spills/prunes/pressure, cost-per-verified-build) wired at api/orchestrator seams. **W13.4** ✅ closed by W4 — `evals/retrieval` hit@5 + grep baseline + non-vacuous floor run inside `make evals` |
| 33 | e2e-live manual, never a gate | **W13.5** ✅ — named `golden` stage in `make evals` (`vet scorecard golden test`) + named CI step "Golden transcripts (agent-behavior gate)"; `make nightly` → `e2e/run-live.sh` exports `E2E_REPORT` (default `e2e/reports/nightly-latest.md`, gitignored) and `cmd/e2e`'s `report()` writes the scorecard markdown on every exit path — a durable nightly artifact; rounds-to-green regression gate = fixtures' `max_rounds` + golden rounds pin (red blocks merge); CI stays secret-free (goldens + scorecard gate from fixtures, live nightly local) |
| 34 | 3,121-line loop monolith vs H2 spec | **W14.1** ✅ — `internal/orchestrator/looplisteners.go` makes §2.4 concrete: the six cross-cutting rows extracted to seams (`toolPolicy` → ToolPolicy with built-ins first + driver-only composition vetoes; `skillDelivered` → ToolDispatch `Stage:"skill"`; `autoAttachSkills` + `enforcePeriodicVerify` moved verbatim; `fsWrite` → FSWrite per successful write; `emitDispatch`/`emitResult` → ToolDispatch/ToolResult); the loop keeps claim → pre-step → request → append → dispatch → append → close with seam calls. `TALONSTUDIO_LOOP=driver\|legacy` — only `driver`/truthy arms the bus, everything else fails closed to today's byte-identical path (read once per batch; idle subscriptions inert). Bus = mutex snapshot + per-listener recover: policy panic fails the *call* closed naming the listener, observer panics log and skip. Gates: transcript parity across polarities on a scripted provider (legacy publishes zero events, driver observes all four channels), policy veto from a new file with zero loop edits (DSH extensibility half; composition row = W14.2), panic isolation, 13-case flag matrix, and the W13 golden suite re-replays all six tasks under `driver` bit-identical — digests included (H2's eval gate) |
| 35 | Composition-as-data (H12) never landed | **W14.2** ✅ — `harness/composition/yaml.go` is the loader (three-layer document, LWW per id, whole-config replacement, first-appearance order frozen, missing file = dark boot, malformed/invalid row = error, `MarshalJSON` dump shape with base-name path + `rows:[]`); new `internal/composer` is the ONE apply layer both binaries share — `Load` resolves `TALONSTUDIO_COMPOSITION` (explicit path fails loud when missing) or walks up from the working directory, `Apply` maps `harness/loop` → `SetMaxRounds` (boot override of the `AI_MAX_ROUNDS` seed, env bounds enforced, no silent clamp) and `policy/<name>` → registry → `SubscribeLoopPolicy`, **unknown component = error**; `policycomponents.go` registry (duplicate registration panics) + shipped `policy/deny-tools`; `cmd/server` is a composer (apply + row-id log) and the repo ships `talon.composition.yaml` (base loop row = shipped default 60 → behavior-neutral; `headless` profile overlay = 6 for the CI budget). Gates: overlay precedence + whole-config replacement + order stability + dump determinism, 6-case bad-row matrix, Load resolution (explicit-missing/env-profile/cwd-walk), and **the DSH composition-row half** — `TestPolicyRowVetoesEndToEnd` vetoes from one YAML row with zero Go edits, inert under legacy. Deviation on file: only loop + policy rows apply; the plan-example rows keep their live `.talon`/env contracts (each flip needs its own parity gate) so they fail loudly instead of pretending |
| 36 | Headless real-boot profile (H11) partial | **W14.3** |
| 37 | `--dump-config` golden diff never run | **W14.4** ✅ — `cmd/server --dump-config` resolves the composition and prints the LWW document as JSON **before** db/AI/listener open (secret-free everywhere), byte-stable by construction: base-name path, sorted map keys, apply-order rows, `rows:[]` never null; output committed as `talon.composition.golden.json`. Gates: `make dumpconfig` — a new named stage folded into `evals` (`vet scorecard golden dumpconfig test`) — and the CI step "Composition golden diff (dump-config)" beside the golden-transcripts step; hand-verified `GOLDEN_DIFF_CLEAN`, headless-profile overlay resolves `maxRounds: 6`, explicit missing path exits 1 |
| 38 | Model Experience docs guard (H13.1) unenforced | **W14.5** |
| 39 | Spill retention (H8 risk `:467-469`): cleanup/export vs `backend/spill` | **W1.8** ✅ audited — no finding: spill lives at `<datadir>/spill` (sibling of the DB, outside every workspace), cleanup targets `workspacesDir/<id>` + `resolveInside`-contained paths, export bundles an explicit asset list — disjoint by construction; one retention note (global hash store, no GC) in the notes |

## 6. Explicit non-goals (kept from PLAN_HARNESS §4, re-confirmed)

1. No Cordis-style runtime DI — Go composition at boot, typed.
2. No JS workflow engine — declarative JSON spec stands.
3. No PTC/prompt-tool-calling mode — native function calling, both serializers.
4. No OS VM-per-session — worktree/overlay isolation first; OS sandbox (seatbelt-class)
   tracked as a later hardening phase only if Talon ships a hosted multi-tenant runtime.
5. No replacement of the verify ladder — it is the domain crown; this plan makes it
   *binding*, not bigger.

---

*Prepared from: full backend audit (2026-09-22, `make evals` executed), the complete
DSH/H-series spec in `PLAN_HARNESS.md`, Claude Code architecture docs (subagents,
hooks, plan mode, skills, MCP), Cursor's published indexing/semantic-search research
(Merkle sync, session-trained embedder, Context Bench), OpenCode V2 docs (service
core, permissions, LSP/formatter, plugins, MCP), and the structural-index literature
(tree-sitter → vector+graph+BM25 with Merkle-diff updates). Corrections welcome —
especially sequencing: Tracks S/X/E/M/A are independent enough to reorder.*
