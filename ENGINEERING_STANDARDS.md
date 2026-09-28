# ENGINEERING_STANDARDS.md — Production behavior of the agent

`TEST_RULES.md` governs **correctness**. Nothing governed **how the agent
behaves in production**. `25_MINUTES.md` diagnosed the disease; the 2026-09-26
gauntlet run measured it against the real stack (project 18 "Momentum", fresh
workspace, real model, stopped at 1h): **FAIL on every behavioral axis**
(Appendix A). This document is the standard. Numbers, not vibes: any change to
the agent (prompt, tool, filter, template, model) must show before/after
against §2 to be accepted.

Companion reading: `25_MINUTES.md` (diagnosis), `TALON_AGENT_IMPROVEMENT_{1,2}.md`
(memory/context architecture), `TEST_RULES.md` (correctness doctrine).

## 1. Doctrine (from 25_MINUTES.md — non-negotiable)

Know enough for the current step, act, observe the result, retrieve more only
when blocked. "Read everything, understand everything, then act" is a defect,
not diligence. Written files survive interruption; surveys do not.

## 2. Behavioral SLOs (gated by the scorecard in §5)

| # | Metric | Standard | Run-1 measured (2026-09-26) |
|---|--------|----------|------------------------------|
| S1 | First file write — fresh project | ≤ 3 min | **28m34s** ✗ |
| S1b | First file write — existing project, small task | ≤ 60 s | not yet run (add scenario) |
| S2 | First route file (a screen) | ≤ 5 min | **53 min** ✗ |
| S3 | Route scaffold complete (every declared route exists + typecheck green) | ≤ 10 min | **2/6 plan routes** at 60 min ✗ |
| S4 | Read-class calls before the first write | ≤ 12, within 1 phase | **28 over 28m34s** ✗ (stream-deduped; transcript 22 — the first-published "51" double-counted running+terminal events, see counting note in A) |
| S5 | Throughput — screen-building turn | ≥ 4 screens/h | **2.0/h (2 of 6 plan screens)** ✗ |
| S6 | Runnable while building | typecheck + route-coverage green after every milestone | 0% of the turn ✗ |
| S7 | Plan-checklist truth | shipped unit marked in the same round it lands; close-out ≡ `plan_task_status` | 0/18 marked ✗ |
| S8 | Verify economy | ≤ 1 `run_checks` per coherent unit; ≤ 15% of turn wall time | **7 runs ≈ 4.3 min ≈ 7% ✅** (revised 2026-09-27 from "14 ≈ 10%" — double-counted; 36.5 s/op from `BASELINE.md`) |
| S9 | Tool contract | 0 prompt-named tools missing from the advertised set, unless gated **and** the prompt explains the gating | **7 stripped** ✗ (git_×4, run_shell, install_package, run_bundle) |
| S10 | Input economy | prefix sections (persona, rules, skills) byte-stable within a turn; tail sections bounded + fingerprinted (map ≤ 12 KB, known-files ≤ 150, ADR ≤ 12, memory ≤ 20) with every mid-turn rebuild logged with trigger + Δ bytes; history bounded by compaction (≤ 120 msgs / ≤ 300 KB body) | traced 2026-09-26: +751 tok = project-map refresh — by design, bounded, ordered after the stable prefix; attribution log shipped ✅; body 82→242 KB ⚠ |
| S11 | Cost accounting | every routed model priced; unknown model = e2e preflight failure | recorded $0 ✗ |
| S12 | Progress visibility | no gap > 90 s without a semantic event (what is being built, what just landed) | "round N" only ✗ |

Scoring: each metric pass/fail per run; **any ✗ on a required row ⇒ run FAIL**.
SLO numbers change only with before/after evidence, edited in this file.

## 3. Contract laws (enforced in code, not in prompt prose)

Prompt prose already says *"Write early… a turn that spends twenty rounds
reading without writing has failed"* — the run still read for 28 minutes.
Prose informs; laws enforce.

- **L1 — Tool contract.** The set of tools any prompt tells the model to use
  must be a subset of the advertised set, minus tools whose absence the prompt
  itself explains (device-gated). Violator today: `filterToCoreTools`
  (`internal/ai/agentic.go`) — the 6,500-token threshold is dead-on-arrival
  (the system prompt alone is 8,529), so every round it strips tools the
  registry *does* hold and `SystemPromptBuild` *does* instruct by name:
  `run_shell`, `run_command`, `install_package`, `run_bundle`, `run_gradle`,
  `build_apk`, `git_status`, `git_diff`, `git_log`, `git_commit`, `git_branch`,
  plus `delete_file`, `run_gate`, `try_edit`, `keep_edit`. Consequences seen in
  Run 1: no installs, no probes, **0 git commits despite "Work git-native"**,
  no bundle gate. Fix: model-aware threshold (or none ≥128K context) and/or
  core ⊇ {install_package, run_bundle, run_gradle, git_status, git_diff,
  git_commit, delete_file}, or make the comment's "next-round pagination" real.
  Ship with a test that extracts tool names from every prompt constant and
  fails on any name that is registered-but-filterable-and-neither-in-core-
  nor-gated-and-explained. **Write the failing assertion first** (TEST_RULES §3).
- **L2 — Exploration budget, mechanically enforced.** The orchestrator counts
  read-class calls (`read_file`, `list_files`, `grep_codebase`, `find_files`,
  `search_files`, `explore`, `inspect_package`, `read_skill`, `project_map`)
  before the first `write_file` of a build turn: at **12** → inject a hard
  "write now" reminder into the next model call; at **18** → emit a user-facing
  warning event; at **24** → end the turn honestly ("surveyed without writing;
  re-prompt with a narrower scope"). Also: one read per path per turn — a
  repeat read of an unchanged path returns the cached transcript entry plus a
  nudge, not the bytes.
- **L3 — Scaffold-first milestone.** Milestone 1 of every build turn: every
  route the plan/layout declares exists as a compiling stub (tabs, onboarding
  target, stack screens), then fill them one by one. New verify rung — **route
  coverage**: every `Tabs.Screen` name, every `Redirect` href, every
  `router.push` literal resolves to a file; runs as part of `run_checks`. The
  app must be statically launchable from minute 5 (S3/S6).
- **L4 — Plan-time feasibility.** The plan loop checks the template digest
  (§4) before promising: every `packages[]` entry must resolve via
  `inspect_package` or carry an explicit install step; features unreachable in
  the offline template are marked deferred **in the plan**. The build never
  discovers mid-turn that the plan was impossible (Run 1 promised svg,
  bottom-sheet, notifications, file-system — none installable, found by the
  model after planning).
- **L5 — Prompt budget.** System prompt ≤ 4,500 tokens (today 8,529 + growth).
  Domain depth (design bar, device protocol, wallet rules) moves behind
  `read_skill` — the mechanism exists. Anything that varies mid-turn lives in a
  fingerprinted `promptsec` section so the prefix cache survives; a system
  block that grows during a turn is a bug until its source is identified.
  CI asserts the token count.
- **L6 — Cancel hygiene.** The byte-identical restart checks `ctx.Err()`
  before retrying: no ghost attempt after a deliberate client hang-up.
- **L7 — Pricing is a precondition.** e2e preflight fails when the routed
  model has no `ROUTING_PRICING_JSON` entry; "cost recorded as 0" is a broken
  gauge, not a log line.

## 4. Context economy (25_MINUTES §1–§3, against our code)

- **Template digest**, built once per template version and cached by hash
  (structure, important files, generation rules, do-not-modify). Orientation =
  digest + `project_map`, done in seconds. A turn that re-reads template source
  (Run 1: 20 `read_file` + 20 `inspect_package`, mostly template) is a
  regression signal, not thoroughness.
- **`project_map` is the manifest** (already self-refreshing) — answers "where
  are routes / state / components" without opening files; never re-derive it.
- **Targeted retrieval**: a task pulls only routing config, touched screens,
  components, theme, store, `package.json`. `node_modules`/Pods/lockfiles only
  for native or dependency tasks — `inspect_package` covers deps.
- **Parallel batch reads** (already the rule) and pruning of superseded tool
  outputs when compaction runs; compaction ceiling ≤ 120 msgs / ≤ 300 KB body.

## 5. The behavioral scorecard — the second test class

Correctness (`TEST_RULES`) stays: it proves endpoints behave. **Behavior is the
new class**: real model, real workspace, medium-hard scenario (TEST_RULES §7),
run on a fresh project *and* an existing project, scored against §2, artifact
at `e2e/reports/behavior-latest.md` — repeatable per TEST_RULES §2.

- Extend `evals.Sample` with the timeline: `FirstWriteAt`, `FirstScreenAt`,
  `ScaffoldAt`, `ToolCallsByClass`, `TasksMarked/Total`, `RunnableFrom`.
  The grader already grades files and rounds; it grades **time** now.
- `e2e/run-gauntlet-continue.sh` is the harness: G-gates + SLO table, one
  report.
- **Rule: no prompt/filter/tool/model/template change is accepted without
  before/after scorecard numbers attached.** Absolute regressions on required
  rows block the change.

## 6. Fix hierarchy (the default order)

Hierarchy rule: **a law the system already claims outranks a performance
improvement, which outranks a diet.** Within a tier, smallest verifiable
change first; every fix ships with its guard (test, log line, or both) and
its failure modes written before the code (TEST_RULES §3).

### P0 — the system must not lie or stall — *fix cycle 2026-09-26*

| # | Fix | Evidence / guard | Status |
|---|-----|------------------|--------|
| P0.1 | **L1 tool contract** — `coreToolSet` ⊇ every prompt-named tool (git_*, run_shell, install_package, run_bundle, run_gradle, delete_file); strip threshold re-scoped 6 500 → 100 000 est-tok (the 6 500 was a Groq constant applied to every route and fired on every request, because the system prompt alone is 8.5 K) | fails-first `TestPromptPromisedToolsSurviveCoreFilter` + `TestFilterKeepsCoreTools` | ✅ |
| P0.2 | **S10 trace → attribution** — the +751 mid-turn growth is the project-map refresh: bounded (12 KB), fingerprinted, ordered *after* the stable prefix (persona/rules/skills never change mid-turn). The missing piece was attribution, not stability: the enrich loop now logs trigger + Δ bytes on every rebuild | `orchestrator: system prompt rebuilt in round N: project-map Δ…` log line; S10 row rewritten | ✅ |
| P0.3 | **Template digest (§4)** — template structure map + preinstalled deps rendered once at boot into `h.templateDigest`, appended to the build system prompt as a constant block ("static, do not survey"); empty template ⇒ no block | `renderTemplateDigest`; byte-stable per process (cache-safe) | ✅ |
| P0.4 | **L2 exploration budget** — read-class streak per turn: advisory note at 12, user-visible warning event at 18, read-class refusals at 24 until a write-class batch lands; batch-level counting (H3's unit); armed on build turns only (`ExplorationBudget: bo.Mode == "build"`) — plan and child loops stay unbounded | fails-first `TestExplorationBudgetLadder` + `TestExplorationBudgetDisarmedByDefault` | ✅ |
| P0.5 | **L6 cancel hygiene** — a deliberate cancel returns before the "restarting byte-identically" log, so a stopped turn stops (Run 1's ghost attempt after the stop button) | ctx check before restart in `openAIStreamAttempt` | ✅ |
| P0.6 | **Scorecard calculator** — `e2e/scorecard.py`: SLO table from a run's SSE log + DB rows, compared against Appendix baseline | artifact `e2e/reports/behavior-latest.md` per run | see §5 |

### P1 — screen throughput and round cost (next cycle)

Root cause, measured on Run-2 (`/tmp/opencode/run2-server.log` +
`run2-build.log`, 47 rounds / 57m07s): **wall time = round time** (round
durations p50 37 s, p90 196 s, max 470 s, sum 3 410 s ≈ the whole turn;
tools are only ~6 min of it — installs 3 min, checks 1m35s). The long
rounds are *generating infrastructure files* (44.6 k out-tok total) on a
request that grows every round: in-tok 16.8 k → 73.5 k (sum_in 1.93 M,
body 67 → 294 KB) because every round replays every prior `write_file`
payload verbatim. And nothing in the system orders the work: first route
lands at **46m05s** — cleanup (30 s) → deps + node_modules type
archaeology (~16 min) → store/utils/config → components → *then* screens.
Grep proof: no "routes first" instruction exists anywhere in
`SystemPromptBuild`.

Failure modes (written before the code, TEST_RULES §3):

- **FM-L3a** — prose alone does not order work: the prompt already says
  "write early, not after exhaustive survey", and Run-1's prose ban on
  surveying still produced 28 min of reading. The ordering rule needs a
  code-side nudge (L2's pattern), or it will be ignored again.
- **FM-L3b** — a naive "routes first" that demands finished screens
  breaks S6 (typecheck must stay green); skeletons must be *compiling*
  screens, and the nudge must fire on **any** `app/**.tsx` write, not on
  completion, or it nags a turn that is legitimately mid-scaffold.
- **FM-L9a** — trimming tool_use args from the wire can make the model
  re-read a file it wrote; trim only *superseded* calls (file edited
  since), only older than K rounds, and only on the wire copy — the
  stored conversation keeps full fidelity (the `TrimSupersededToolOutput`
  rule).
- **FM-L8a** — a heartbeat proves *visibility*, not progress; S12 must
  keep measuring gaps between real events and stall detection stays with
  `stallbody` — the heartbeat must never be able to satisfy a stuck turn.
- **FM-L10a** — a blanket node_modules read ban breaks legitimate reads
  (configs, root files); scope the refusal to survey-style paths under
  `node_modules/` and answer with `inspect_package`, never silence.
- **FM-L10b** — the guard keys on the file tools; Run-3 proved the
  survey simply moves to shell the moment the direct path refuses:
  `run_shell` ran `find node_modules/expo-notifications -name "*.d.ts"`
  and repeated `grep -rn … node_modules/…` (one composing right into
  the turn's only 183 s S12 gap) while the direct attempts were
  correctly refused twice. The refusal must be tool-shape-independent —
  shell commands that READ dependency internals get the same hint, or
  L10 is a detour sign, not a wall.
- **FM-L10c** — shell commands that BUILD or MAINTAIN must never be
  refused: `npm install/rebuild/ci`, `rm -rf node_modules && npm ci`,
  `cat node_modules/<pkg>/package.json` (FM-L10a parity). Scope the
  shell check to read-survey verbs naming a non-exempt node_modules
  path, and let package-manager-prefixed commands pass untouched — a
  refusal that breaks the build is a worse bug than the survey it
  stops.
- **FM-L11a** — the close-out paragraph orders the model to "check the
  build-plan checklist" but never names `mark_task_done`, the only
  mechanism that moves it: a compliant model answers in prose and every
  row stays pending (Run-2: 0/18). Features — whose file mapping is too
  loose for the heuristic by design — and route-mismatched screens
  ("Home" living in `app/index.tsx`) have **no** automatic path at all,
  so the tool call is their only honest marker.
- **FM-L12a** — marks are gated on the turn's *final* verify, which runs
  only on a normally-completed turn: a cancelled or failed turn credits
  nothing (Run-2: build cancelled at round 47 with two route files on
  disk and a green `run_checks` in the same turn → 0/18). S7's standard
  is "marked in the same round it lands", so a passing `run_checks`
  mid-turn must credit the files written **before** it.
- **FM-L12b** — that mid-turn credit must not reach files written in the
  *same batch* as the check (a parallel write may not have been compiled
  by it): snapshot the touched set before dispatch, or the checklist
  claims compile truth it never had.
- **FM-L12c (accepted)** — slug matching deliberately under-claims
  (`app/index.tsx` never matches screen `home-dashboard`) and the audit
  flips unverifiable completions back rather than trust them; L11's
  explicit tool call is the honest cover for those rows. No guessing
  added.
- **FM-L4a** — the plan prompt's prose ("only list packages not in
  this set") is advisory; Run-1 listed 6 packages — 4 absent from the
  template — anyway and the plan turn closed `completed`, deferring
  discovery to the build (10 errored `inspect_package` probes, features
  promised at plan time and still absent at hour 58). Advisory prose
  never fails a turn; the gate must be code at the plan doors.
- **FM-L4b** — an existence probe (`npm view`) is the wrong bar: all
  six of Run-1's packages **exist**; they failed as installations
  against that workspace. Feasibility means *resolvable in this
  workspace* (`npm install --dry-run` against the real template), not
  "the name is registered".
- **FM-L4c** — probe flakiness must not fail healthy plans: a timeout
  or registry outage reports *unknown* and passes the turn; only a
  definitive resolution failure (404 for a package, ERESOLVE against
  the workspace) fails it. Availability ≠ feasibility.
- **FM-L4d** — the probe must not mutate: plan mode is read-only by
  tool absence (W3, law 6), so the check is `--dry-run` — no
  package.json edit, no node_modules write. A feasibility gate that
  mutates the workspace during plan breaks the contract it enforces.
- **FM-TELa (L13)** — a ledger that does not add up is worse than no
  ledger: every logged request must satisfy `system + tools + payloads +
  results + history + envelope == body_bytes` exactly, with each content
  bucket measured from the same objects bound for the wire (the post-trim
  copy, never the stored transcript) and `envelope` a *named* remainder
  (JSON structure, request fields, cache markers) rather than a silent
  gap — unattributed bytes are §5's `?` crime in log form.
- **FM-TELb (L13)** — write payloads are the leak RESEARCH's P0.1 names,
  so they get their own bucket: `write_file`/`edit_file`/`multi_edit`/
  `try_edit` inputs land under `payloads`, never folded into `history` —
  otherwise the instrument built to size the leak cannot see it.
- **FM-TELc (L13)** — attribution observes, never acts: the ledger reads
  values already bound for send and must not mutate or re-route them. A
  measurement that changes the thing measured invalidates every S10
  number taken so far.
- **FM-TELd (L13)** — phase timers must cover the round or they lie:
  `prep` and `gen` timed around the actual model call, every tool batch
  logging its own duration; a bucket missing while wall time passed is
  S12's silent-gap crime in metrics form.

| # | Fix | Evidence / guard | Status |
|---|-----|------------------|--------|
| L3 | **Routes-first contract** — (i) `SystemPromptBuild` gains the ordering rule: scaffold every planned `app/` route as a *compiling* skeleton within the first minutes, before infrastructure; (ii) code-side route ladder in the build loop: no `app/**.tsx` write by +5 min → advisory line in the round card, by +10 min → warning event (L2's pattern — prose failed, code enforces). Route coverage stays the gate it is today | fails-first ladder test; Run-2 S2 = 46m05s, S5 = 2.1/h → **Run-3 S2 = 13m59s, S5 = 7.5/h ✅ (first pass), S3 = 6/6 routes**; warning fired once at +11 min | ⬤ |
| L8 | **Build heartbeat (S12)** — the build drain loop gets the same 15 s heartbeat plan mode already has; forward tool-argument deltas as progress if cheap. True Run-2 gap = 191 s (parser correction: scorecard must gap-check **all** events, not only tool events — its current 514 s figure counted tool events only) | S12 row; Run-2 top-5 true gaps 191/117/81/79/70 s → **Run-3: 183/48/43/36/24 — one breach instead of five**; the one is a provider-side generation silence (model composing a shell grep), heartbeat-covered, heartbeats still excluded per FM-L8a | ⬤ |
| L9 | **Wire-trim superseded tool_use inputs** — old `write_file`/`edit_file` payloads collapse to a placeholder on the wire copy (FM-L9a rules); expectation: body growth drops well under the observed ~5 KB/round, prefill stops scaling with turn age | test: trimmed / not-trimmed / stored-fidelity; Run-2 max body 293,899 B, 24 rebuilds → **Run-3 max body 254,524 B (−13%), 20 rebuilds**; S10 still ✅ | ⬤ |
| L10 | **node_modules survey guardrail** — `read_file`/`grep_codebase`/`find_files` **and shell read-survey verbs** (`grep`/`find`/`cat`/`sed`/`ls`/…) against `node_modules/**` refused with an `inspect_package` hint (FM-L10b: the survey moved to `run_shell` the moment the file tools refused); package-manager commands and FM-L10a exemptions pass untouched (FM-L10c) | fails-first guard tests (file tools + shell verbs + build commands allowed); Run-2: ~16 min of .d.ts archaeology; Run-3: 2 direct refusals proved the hole shell still had | ⬤ |
| L11 | **Close-out names the mechanism** — the checklist sentence in `SystemPromptBuild` tells the model to call `mark_task_done` (batch the finished items, named as the checklist lists them) instead of only "check the checklist" in prose | fails-first prompt-contract test; Run-2 S7 = 0/18 with the tool always advertised (`coreToolSet`) | ⬤ |
| L12 | **Mark at every passing compile** — `executeTools` credits `markScreenTasksFromFiles` + `auditScreenTasks` right after a passing `run_checks`, using a touched-set snapshot taken before dispatch (FM-L12b); the final verify gate stays exactly as it is | fails-first executeTools test (pass marks / fail does not / same-batch write does not); Run-2: cancelled turn credited nothing | ⬤ |
| L4 | **Plan feasibility gate** — both plan doors (Send + stream) probe the plan's new packages with `npm install --dry-run` in the real workspace (FM-L4b resolution bar): a definitive failure names the package and **fails the plan turn** — unknown (flaky network) passes (FM-L4c), preinstalled packages skip the probe, and the probe never mutates (FM-L4d) | fails-first door tests (infeasible fails / unknown passes / preinstalled skipped); Run-1: 4 of 6 plan packages unshipped, 10 errored probes at build hour 0 | ⬤ |
| L13 | **Request ledger + phase metrics (RESEARCH P0.4)** — every model call logs a `request-ledger` line attributing `body_bytes` to system / tools / payloads / results / history with a *named* envelope that sums exactly (FM-TELa; write-class bodies in their own bucket so RESEARCH P0.1's leak is visible — FM-TELb; sizes read from the wire-bound objects only — FM-TELc); every enrichment logs a `prompt-sec` section table (`Sizes()` reconciles with `Assemble`); every round logs `round-metrics` prep/gen and a `round-tools` duration (FM-TELd) | fails-first `TestRequestLedgerAccountsEveryByte`, `TestRequestLedgerDoesNotMutateWhatIsSent`, `TestSizesReconcileWithAssemble` (red at build → green); E2E artifact = the four serverlog line types per run; unblocks L5's premise and quantifies B-phase | ⬤ |

Still-open P1 rows from the previous list:

- **L4 plan feasibility** — closed this cycle by the L4 row above:
  both plan doors now gate the parsed plan's new packages through
  `npm install --dry-run` in the real workspace (resolution, not
  name-existence — Run-1's four "not installable" packages all existed
  on npm), failing the plan turn with the package named instead of
  hour 58 of the build; unknown probes pass, preinstalled packages skip.
- **L5 prompt diet** — `SystemPromptBuild` ≤ 4 500 tok: design-bar detail,
  device protocol, navigation semantics move behind `read_skill`; also
  offsets P0.3's digest in the budget.
- **S7 checklist truth** — closed this cycle by L11 (prompt names the
  mechanism) + L12 (every passing `run_checks` credits the screens it
  compiled, cancelled turns included); Run-2's 0/18 came from both
  failure modes at once.
- **S11 pricing preflight** — blocked: needs real Atria model prices before
  a number can be recorded (unknown price stays an e2e failure).

### P2 — hygiene

- S12 progress semantics (every round names what it built, not "round N").
- S1b existing-project small-task scenario (≤ 60 s first write).
- Route/quality overrides per deployment, operator-facing threshold config
  for P0.1's strip valve when a small-context route arrives.

## Appendix A — Run-1 baseline (project 18 "Momentum", 2026-09-26)

- Plan turn (prompt 55): ~3 min, `completed` — 6 screens, 12 features,
  6 packages (4 absent from the template and not installable).
- Build turn (prompt 56): 10:10:11 → 11:10:11 — **38 rounds, 1h00m0s,
  avg 1m35s/round**; stopped by client disconnect (real stop path; status
  `cancelled`, transcript intact, resumable).
- Timeline: +28m34s first write (`utils/date`, `constants/theme`,
  `store/use-app-store`) … +53 min first screen (`app/(tabs)/home`) …
  +58 min `habits.tsx` … stop at **2/6 plan routes**.
- **Counting note (2026-09-27):** the SSE emitter writes `running` then a
  terminal event per call, so every tool count first published here was
  doubled (46 `write_file` events were 23 calls). Real counts, paired by
  `e2e/scorecard.py` and confirmed against `conversation_messages.tool_use`:
  28 read-class before the first write (10 `read_file`, 10
  `inspect_package` — **all 10 errored**, probing packages the plan promised
  and the template never shipped, 6 `read_skill`, 1 `list_files`,
  1 `grep_codebase`, 1 `find_files`), then 41 write-class (24 `write_file`,
  16 `edit_file`, 1 `multi_edit`) and **7 `run_checks`**.
- Advertised tools constant at 18/round; **7 prompt-named tools filtered**
  (L1); 0 git commits; 0 plan tasks marked; tab bar declares 5 routes,
  2 files exist; `/onboarding` redirect target missing.
- Independent `tsc --noEmit` at stop: **0 errors** — correctness held,
  behavior failed. Context: hist_msgs 8→97, body 82→242 KB,
  system 8,529→9,280 tokens (+751 = project-map refresh, traced);
  cost recorded $0.
- Verdict: **FAIL** — this appendix is the baseline future runs are scored
  against. Reproduce the table: `python3 e2e/scorecard.py --db
  backend/talon.db --project 18 --prompt 56 --fresh`.

## Appendix B — Run-2 (project 19 "Momentum", 2026-09-27, after the P0 fixes)

Same plan prompt (→ prompt 57, `completed`, 6 screens / 18 tasks) and same
build prompt (→ prompt 58, stopped by the harness at **57m06s**, status
`cancelled`, 47 rounds, avg 1m13s/round). Full table:
`e2e/reports/behavior-latest.md`; Run-1 re-scored alongside it as
`e2e/reports/behavior-run1.md`.

| Row | Run-1 | Run-2 | Δ |
|---|---|---|---|
| S1 first write | 28m34s ✗ | **4m18s ✗** | 6.7× faster — the exploration ladder works; still 78 s over |
| S2 first route file | 53 min ✗ | 46m05s ✗ | ~flat |
| S3 scaffold | 2/6 ✗ | 2/6 ✗ | flat |
| S4 reads before write | 28 ✗ | **14 ✗** | halved; 2 calls over the limit |
| S5 throughput | 2.0/h ✗ | 2.1/h ✗ | flat — the wall-clock problem is now screen work, not exploration |
| S6 runnable | ? (stale) | ? (stale) | unchanged — stop lands between write and re-check |
| S7 checklist truth | 0/18 ✗ | 0/18 ✗ | flat (P1) |
| S8 verify economy | ~7% ✅ (derived) | **3% ✅ (measured)** | timestamped stream closed the derivation |
| S9 tool contract | 18 advertised, 8 stripped ✗ | **41 advertised ✅** | fixed; install_package ×3, run_shell ×2, run_command ×3, delete_file ×17 now actually called |
| S10 input economy | unmeasured ? | **system 10,399→10,399 tok, 24 attributed rebuilds ✅** | traced + bounded + attributed, measured |
| S11 cost | $0 (L7) | $0 — `no pricing for model "Atria-Dawn-Preview"` logged | still blocked on pricing data |
| S12 progress | unmeasured ? | max 514 s gap ✗ → **corrected to 191 s** when the scorecard's gap parser was fixed (it counted tool events only — see L8) | now measured — silent-round heartbeat is a P1 |

- Verdict: **FAIL** — 7 required rows. But every row the P0 cycle targeted
  (S9, S10, S8) now passes *measured*, and S1/S4 improved 6.7×/2×. The
  remaining wall: first screen at 46 min (infrastructure before routes)
  and zero checklist marks — both P1.
- Reproduce: `python3 e2e/scorecard.py --db backend/talon.db --project 19
  --prompt 58 --stream /tmp/opencode/run2-build.log --serverlog
  /tmp/opencode/run2-server.log --fresh`.

## Appendix C — Run-3 (project 20 "Momentum", 2026-09-27, after L3/L8/L9/L10)

Same committed prompts (`e2e/prompts/momentum-{plan,build}.txt` — one
variable: the fixes), fresh project, stack recipe `e2e/run-behavior.sh`.
Plan → prompt 59 `completed` (6 screens / 17 tasks, 0 questions). Build
→ prompt 60 **`paused` at the 60-round budget**, 48m03s, 60 rounds, avg
48 s/round (Run-2: 72 s/round) — `paused` is the resumable
round-exhaustion state, not a failure; the turn was mid-fix-loop with
all 6 routes written and the last check dirty. The binary was built
before L11/L12/L4 landed, so S7/S6 below do **not** carry them.

| Row | Run-2 | Run-3 | Δ |
|---|---|---|---|
| S1 first write | 4m18s ✗ | 11m49s ✗ | regressed — round-1's 19-read batch, then a 9.5-min opening think (21:24:59→21:34:24, no tool calls) |
| S2 first route file | 46m05s ✗ | **13m59s ✗** | 3.3× faster (root `app/_layout.tsx` rewrite; first *plan-screen* route at +28m58s); +10-min warning fired once at 21:36:34, first writes landed the same second |
| S3 scaffold | 2/6 ✗ | **6/6 plan routes**; last check dirty ✗ | every route exists; a green typecheck never landed before the budget |
| S4 reads before write | 14 ✗ | 19 ✗ | regressed — one parallel round-1 mega-batch; the L2 warning's batch-level counting fires advisory, not refusal, below 24 |
| S5 throughput | 2.1/h ✗ | **7.5/h ✅** | first-ever pass (standard ≥4/h) |
| S6 runnable | ? (stale) | last dirty, 3/11 clean ✗ | paused mid-fix-loop |
| S7 checklist truth | 0/18 ✗ | 0/17 ✗ | flat — build predates L11/L12 (both committed after this binary was built); Run-4 carries them |
| S8 verify economy | 3% ✅ | **9% ✅** (11 run_checks ≈ 4m26s) | pass |
| S9 tool contract | 41 ✅ | 41 ✅ | pass |
| S10 input economy | 10,399→10,399 tok ✅ | **10,607→11,603 tok, 20 rebuilds ✅** | pass; +996 tok of project-map drift over 60 rounds, bounded |
| S11 cost | ? (L7) | ? (L7) | gateway `/v1/models` carries no pricing field — still honestly unmeasured |
| S12 progress | max 191 s, top5 191/117/81/79/70 ✗ | **max 183 s, top5 183/48/43/36/24 ✗** | one breach instead of five; the one is a provider-side generation silence (22:00:18→22:03:21, model composing a shell grep), heartbeat-covered — heartbeats stay excluded per FM-L8a, and stall detection belongs to `stallbody` |

- **L10 fired and exposed its hole**: 2 `node-modules-survey`
  refusals on the direct path, and the model routed the identical
  survey through `run_shell` (`find`/`grep`/`sed` over .d.ts — one
  composing straight into the 183 s gap). FM-L10b/c written and fixed
  the same day: shell read-survey verbs now refuse with the same hint,
  package-manager/build commands never do.
- **L4's failure repeated live, in a binary predating L4**: `ls -d
  node_modules/@react-native-community/datetimepicker` → *No such
  file*, `inspect_package` ×2 errored — the plan again promised
  unshipped packages; the L4 gate (same cycle) fails such plans at the
  door.
- Tool mix: read_file 29, write_file 24, run_checks 11, run_shell 10,
  read_skill 7, edit_file 7, multi_edit 6, grep_codebase 4,
  install_package 3, inspect_package 2, node-modules-survey 2,
  context_build 1, explore 1, route_scaffold 1, batch_conflict 1,
  compact 1.
- Verdict: **FAIL** — 7 required rows. Every row the fix cycle targeted
  moved the right way (S2 −32 min, S3 2/6→6/6, S5 → pass, S12 five
  breaches→one, L10 refusals firing); S1/S4 regressed and need the next
  cycle's attention.
- Reproduce: `python3 e2e/scorecard.py --db backend/talon.db --project 20
  --prompt 60 --stream /tmp/opencode/run3-build.log --serverlog
  /tmp/opencode/run3-server.log --fresh` (report committed at
  `e2e/reports/behavior-latest.md`).
