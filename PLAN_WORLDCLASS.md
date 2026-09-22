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
| 8 | `Fork`/`delegate_fork` unused | **W1.5**, **W12** |
| 9 | Approval Seam/`Escalate` dead; tier-2 = ask-only | **W1.6**, **W9** |
| 10 | `AI_MODEL_TIER` undocumented | **W1.7** ✅ no change — already documented in `.env.example:27-29` (audit error) |
| 11 | Event log write-only (derive flag off) | **W2** |
| 12 | Plan mode blind (no tools/map/instructions) | **W3** |
| 13 | Plan not a durable artifact | **W3.4** |
| 14 | No semantic index; `embedding` placeholder | **W4** |
| 15 | Project map one-shot snapshot | **W4.6** |
| 16 | Memory FTS-only recall | **W4.5** |
| 17 | No LSP/symbol ground truth | **W5** |
| 18 | Verify fail-open; `SkippedGates` non-blocking | **W6** |
| 19 | Graders exist but out of CI | **W6.3**, **W13** |
| 20 | Single provider/endpoint; same-endpoint fallback | **W7** |
| 21 | No per-capability (vision) model | **W7.2** |
| 22 | No turn spend cap | **W7.3** |
| 23 | Dark flags: PIPELINE / PATCH_BUFFER / SUBAGENTS / derive | **W8.1** |
| 24 | Dead packages: composition / settings / ralph / goals | **W8.2**, **W14** |
| 25 | Flag surface undocumented (`.env.example` gaps) | **W8.3** |
| 26 | Three approval mechanisms, no single authority | **W9** |
| 27 | No standing grants (repeated ask_user pauses) | **W9.3** |
| 28 | No hooks / deterministic user extensibility | **W10** |
| 29 | No MCP; plugins serve skills only | **W11** |
| 30 | Children share workspace → locked read-only | **W12.1** |
| 31 | No fan-out spend/concurrency caps in prod | **W12.2** |
| 32 | No agent-behavior evals / golden transcripts | **W13** |
| 33 | e2e-live manual, never a gate | **W13.5** |
| 34 | 3,121-line loop monolith vs H2 spec | **W14.1** |
| 35 | Composition-as-data (H12) never landed | **W14.2** |
| 36 | Headless real-boot profile (H11) partial | **W14.3** |
| 37 | `--dump-config` golden diff never run | **W14.4** |
| 38 | Model Experience docs guard (H13.1) unenforced | **W14.5** |

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
