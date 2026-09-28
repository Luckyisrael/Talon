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
- **FM-E1a (L14)** — the pre-write mega-batch cap must stop the flood,
  not the work: before the turn's first write-class batch, a batch
  carrying more than `maxPreWriteReadBatch` (10) read-class calls
  dispatches only the first 10 and refuses the rest with the count and
  the corrective action; batches containing a write, batches after the
  first write, and disarmed (plan/child) turns run untouched — a cap
  that fires in those lanes blocks the targeted survey the doctrine
  explicitly allows after writing.
- **FM-E1b (L14)** — the cap and the 12/18/24 ladder must not
  double-count or shadow each other: cap-refused calls deliver nothing,
  so only dispatched reads add to the streak; the hard stop keeps its
  existing semantics (crossing batch counted then refused, write tools
  never refused); the cap's refusal names the *batch cap* — never
  "exploration budget exhausted" — so the model knows which rule it hit;
  and a capped batch that also passes the reminder still carries the
  advisory note (one rule's message may not silence another's).
- **FM-E2a (L14)** — the warning event must fire whenever the streak
  crosses `exploreBudgetWarning`, even when the same batch also jumps
  past the hard stop: the current ladder checks hard first, so a jump
  (17 → 25) refuses everything and never emits the only user-visible
  step between advisory and refusal — with mega-batches legal, jumps
  are the common case, not the rare one.
- **FM-C1a (L15)** — the richer plan contract must not lie to old data
  or seed unverifiable tasks: legacy plans whose `"features"` are plain
  strings load exactly as before (the shaped form is additive, one
  unmarshal path accepts both); a feature's declared `files` are
  validated at the plan door — relative in-tree paths only, no `..`,
  no `node_modules`, no globs — and a malformed plan is *rejected with
  the offending feature named* rather than accepted envelope-only
  (RESEARCH P0.2's crime) or silently repaired; `acceptance` is prose
  the checklist renders and never parses.
- **FM-C1b (L15)** — features with declared files get the screens'
  honest bar, and only those: marked complete only on the
  verified-success path with every declared file written this run, and
  the audit flips a completed feature back to pending when a declared
  file does not exist (closing RESEARCH P1.6's model-asserted-only lie
  channel). A feature *without* files keeps today's model-only marking —
  a heuristic guessing at loose mappings is what "under-claims before it
  lies" forbids, per the screen heuristic's own rule.
- **FM-C1c (L16)** — the prefix cache only pays for what sits behind
  it: the session-stable sections (persona → rules → device facts →
  discipline → instructions → skills) must all render *before* the
  volatile ones (project map, known-files, memory, ADRs, checklist), so
  a map refresh or a checklist flip re-sends only the volatile tail.
  Today instructions (weight 1000) and skills (1100) render after every
  volatile section, so the most common mutation of all — a file write
  moves the project map — invalidates the two largest stable sections
  behind it (RESEARCH P1.5's cache-bust in weight form).
- **FM-C1d (L16)** — the checklist must shrink without hiding the
  truth: completed items collapse to one `Done (N): name, …` line that
  keeps *every* name (a collapse that drops names invites the
  re-derivation the section exists to prevent — S7's crime); pending,
  in-progress, and failed stay itemized with their markers; and a
  declared feature's files and acceptance ride its line — the model
  cannot aim at files it cannot see, and a bar required for marking is
  a bar it must be shown. A render that hides a remaining task, a
  blocked flag, or a done name is a lie by omission.
- **FM-D1a (L17)** — inaction needs a clock the model cannot skim. A
  build turn with no successful write by +3 min (S1's standard) shows a
  first-write advisory line on the turn-state card; past +6 min it
  escalates to one latched warning event. L2/L3's code-side pattern,
  because prose ("write early, not after exhaustive survey") failed in
  all three runs — S1: 28m34s, 4m18s, 11m49s against ≤3 min — and
  Run-3's round-1 9.5-minute silent generation proves a round-count
  budget is blind to wall time, so the gate reads the clock, never the
  round number. Build mode only, silent after the first write, at most
  one warning event: a gate that nags every round teaches the model to
  ignore gates (FM-L3b's lesson).
- **FM-D1b (L17)** — one voice on the card. While nothing has been
  written, the first-write note owns the card: demanding "scaffold
  routes" before any write at all is downstream noise — the first-write
  line already names a route skeleton as the artifact to write. The
  route gate's note resumes the moment the first write lands, and its
  warning event keeps exactly today's semantics (the L3 ladder test
  stays pinned and green).
- **FM-B1a (L18)** — a payload write with no later write to the same
  path replays its full body every round for the life of the turn: L9's
  trim collapses *superseded* inputs only, and Run-3 wrote 24 distinct
  paths — every one of them lone, every one replayed (RESEARCH P0.1:
  body 67 → 294 KB). The disk is the record. A write whose `tool_result`
  is success and whose age passes the same 4-round working-memory
  window collapses to a disk pointer — recent writes keep their bodies,
  and a write whose result *failed* never reached disk, so its payload
  is the conversation's only record and is never collapsed (today's
  superseded trim does not check this either; closing it belongs to
  this fix). Collapsing keeps the tool_use's ID, position, and pairing
  intact on the wire copy only — the stored transcript stays full
  fidelity (FM-L9a's invariants, carried over).
- **FM-TELe (L19)** — the ledger must measure the wire, not the
  transcript's intent. L13 attributed the stored conversation's blocks,
  but the serializer is the gate: `toOpenAI` drops `thinking` and
  `plan` blocks, `toAnthropic` drops `thinking` too ("without the
  original signature the API rejects them") — yet their bytes were
  counted in `history` and never sent. Run-4's live line proves the
  lie: `envelope=-1894 → -26156` — a remainder named "JSON structure
  and request fields" cannot go negative without the account being
  wrong — and any future block type inherits the same silent
  overcount. The fix is structural: `AttributeRequestBody` reads the
  **marshaled body itself** with `json.RawMessage` spans — every
  bucket is a disjoint byte span carved out of the request that goes
  out, so `system+tools+payloads+results+history+envelope == body`
  with `envelope ≥ 0` by construction; dropped content cannot be
  counted because it is not in the body, both provider paths share one
  attributer, and FM-TELb's payload bucket survives via each
  `function.name` / `input` (write-class names → payloads, everything
  else → history).
- **FM-H6a (L20)** — a catalog that is never loaded is worse than no
  catalog: `skills.NewCatalog` starts with an empty merged map, and
  `Reload()`'s own doc says it is "called at composition start" — but
  it has no callers anywhere, so the H6 bridge stays empty for the
  life of the process. Run-4's `prompt-sec` line is the proof:
  `skills/catalog=662` is exactly the workspace-plugins guidance with
  a zero-skill base — not one of the 14 embedded skills' names or
  descriptions reached the prompt — while `agent knowledge ready (14
  skills)` shows the knowledge layer itself loaded fine. The same
  empty catalog sits behind `read_skill` (`pluginSkillLibrary` → the
  live snapshot over `o.skills`), so a deliberate load answers "none"
  and H6's law — listed in the manifest, bodies only through
  `read_skill` — fails in both directions; input-side auto-attach is
  the only channel left, which is exactly why L5's diet must not ship
  before this fix. The fix is structural: `NewCatalog` performs the
  initial load itself — construction *is* composition start, so a
  composition cannot forget it — and `Reload` stays the refresh seam
  (touch-reconciliation refresh remains unwired; no run has shown a
  need, so it is out of scope here).
- **FM-H6b (L20's completion)** — loading the catalog is not the same
  as populating it: `EmbeddedProvider.Load` bridges
  `knowledge.Registry` → `knowledge.Skill` with `{Name, Body}` only,
  so `Description` — the one field `Manifest()` exists to render, and
  the one the manifest tells the model to match against — never
  crosses. Run-5 measured the exact symptom: `skills/catalog=1281` =
  619 B of name-only manifest (254 B preamble + 365 B of `- **name** — `
  rows rendering with nothing after the dash) + 662 B of unchanged
  plugins guidance, where
  L20 predicted ~5.5 KB, and **0 model-initiated `read_skill` for the
  second run running** (4/4 calls were auto-attach synthetics). The
  chain reads *listed but unnamed* — a discovery surface the model can
  see and cannot choose from, which fails H6's law in the same
  direction as having no catalog at all. The field was not merely
  uncopied — the registry exposed no read path for it at all
  (`SkillBody`/`SkillNames` only) — so the fix is a `Description`
  accessor plus one field carried at the bridge, still contract-tier:
  without it L20's own
  acceptance evidence never arrives, and any "is discovery working?"
  run stays unanswerable. Guard: a test that asserts the *rendered
  manifest* carries a description body for an embedded skill — not
  merely that the provider loaded, which is exactly the assertion
  that passed while the field was missing.
- **FM-429a (candidate L23)** — a retry budget measured in attempts
  cannot outlast a rate window measured in minutes: `openStream`
  backs off `attempt*3s` (3s, 6s = 9 s total) because that ladder was
  written for flaky 502s, so Run-5's `Cluster RPM rate limit exceeded`
  bursts burned the whole budget in ~15 s and killed a healthy turn at
  round 8 while the 60 s window that would have cleared it was still
  open. Symmetric failure on the other side too: a backoff long
  enough for a minute-window would stall a 502 recovery, so 429 needs
  its own policy (honor the window/reset hint, cap the budget in
  *seconds*, keep the 502 ladder unchanged).
- **FM-B2a (L21)** — the always-on prompt replays what `read_skill`
  exists to defer. Run-4 measured `persona=22168 B` (≈ 5542 tok
  against L5's 4500 budget), and two of the diet's three sections are
  duplicates of skills that already ship: `## Design bar` ≈
  mobile-craft and `## Navigation semantics` ≈ expo-router-nav (full
  coverage verified: push/replace, modal/sheet, one-way doors, the
  wallet nuance) — while `## The live device` has no skill home at
  all, so moving it blindly would make device protocol unreachable
  behind FM-H6a's broken discovery chain. The diet must not lose
  information, and the device laws must not move: unique facts missing
  from the skills (`@/constants/app-styles`, the vision-gate
  sentence) merge into mobile-craft *before* the sections go; a new
  `device-session` skill carries the protocol verbatim (per-unit
  verify order, which tool for which check, maestro authoring,
  lifecycle detail); the prompt keeps a compact always-on surface law
  — the four device tools' absence rule, "do not fabricate device
  verification", the 10/30 lifecycle, the `.maestro` scope line, and
  the skill pointer — because S9's device-gated exemption and Phase
  A's guard both exist *because the prompt explains that surface*
  (laws stay on, how-to moves). Budget: persona ≤ 4500 tok
  **including** P0.3's template digest and the platform allowlist
  (Run-4: persona 22168 B, compiled const 15469 B → digest+allowlist
  = 6699 B → the base const must stay ≤ 11301 B against the
  18000 B / 4500-tok persona budget — measured on the compiled
  string; source length counts backtick-concatenation scaffolding and
  overstates the const by ~800 B).

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
| L14 | **Pre-write mega-batch cap + warning timing (RESEARCH P0.3's real gap)** — before the turn's first write-class batch, a batch carrying > `maxPreWriteReadBatch` (10) reads dispatches the head and refuses the tail with the cap's own message (FM-E1a); cap-refused calls never enter the 12/18/24 streak, the hard stop keeps its semantics, the cap's refusal never says "exhausted", and the advisory note still rides a capped batch (FM-E1b); the warning event now fires on *any* crossing of `exploreBudgetWarning`, including a jump that also crosses the hard stop (FM-E2a — today's ladder checks hard first and skips the only user-visible step) | fails-first `TestMegaBatchCappedBeforeFirstWrite` (red: 30-read batch hit the hard stop, delivered 0), `TestWarningFiresWhenBatchJumpsPastHardStop` (red: 0 events), `TestMegaBatchCapExemptLanes` (exemption guard), ladder test moved to post-cap contract (red → green); pre-write, armed turns only | ⬤ |
| L15 | **Slice contract + honest feature marking (RESEARCH P0.2/P1.6)** — `features` becomes `{text, files, acceptance}` with one unmarshal path accepting the legacy string shape (FM-C1a); `AppPlan.Validate()` rejects malformed declared files at both plan doors via the `checkPlanFeasibility` choke point — feature named, nothing saved, never envelope-only acceptance; shaped features seed their files onto the task and take the screens' honest bar: heuristic marking only when every declared file is written on the verified-success path, the audit flips completed features back when a declared file is missing (FM-C1b); file-less features keep model-only marking; the plan prompt teaches the shape, and the frontend normalizes wire features to display text at both ingestion points (a raw object child crashes the plan card) | fails-first `TestPlanFeatureAcceptsLegacyAndShaped` + `TestPlanRejectsMalformedFeatureFiles` (red: type undefined), `TestPlanTaskRowsCarryFeatureFiles` / `TestMarkFeatureTasksFromFiles` / `TestAuditFlipsFeatureWithMissingDeclaredFile` (red), `TestPlanSendDoorRejectsMalformedFeatureFiles` (red: 200 + saved) + stream-door twin (red: plan frame emitted) + `TestPlanGatePassesShapedContractPlan` (over-fire guard), frontend `featureText` vitest (red: helper missing, 3 cases); golden `plan-existing-feature` re-recorded via `TALONSTUDIO_UPDATE_GOLDEN=1` — digest-only diff, criteria untouched | ⬤ |
| L16 | **Prefix-cache order + compact checklist (RESEARCH P1.5)** — stable sections (persona → rules → device → discipline → instructions → skills, weights 450/460) now all sort before the volatile block (map 500 → checklist 900), so a map refresh or checklist flip re-sends only the volatile tail instead of invalidating instructions/skills behind it (FM-C1c); `renderPlanChecklist` collapses completed items into one `Done (N): name, …` line that keeps every name and drops their descriptions, keeps pending/in-progress/failed itemized with markers, and rides declared `files:` + `done when:` on feature lines — `Acceptance` wired plan → row → task for it, nothing invented for undeclared fields (FM-C1d) | fails-first `TestStableSectionsBeforeVolatile` (red: skills assembled after the volatile block), `TestRenderPlanChecklist` moved to compact contract (red: `[x]` present, no Done line), `TestRenderPlanChecklistCarriesEvidence` (red: no files/acceptance plumbing); artifact `.talon/plan.md` render untouched — round-trip test still pins the checkbox format | ⬤ |
| L17 | **First-artifact clock (RESEARCH P1.8)** — a build turn with no successful write gets a first-write advisory line on the turn-state card from +3 min (S1's standard) escalating at +6 min with one latched `first_write` warning event; build mode only, closed by the first successful write at the fsWrite seam, errored writes never count (FM-D1a); the card speaks with one voice — `GateNote` gives the first-write line ownership while nothing is written and hands the card back to the route gate once the first write lands, the route warning keeps today's semantics (FM-D1b) | fails-first `TestFirstWriteLadder` (red: method undefined — silence-only today; asserts in-window silence, S1-named advisory, escalation, one-shot latch, plan-mode silence, closure on write) + `TestGateNoteOneVoice` (red: `GateNote` undefined — route note must not compete while unwritten, must resume after the first write); L3's `TestRouteScaffoldLadder` stays pinned green; round-based budgets rejected with evidence (Run-3's round-1 9.5-min silent generation is invisible to a round count) | ⬤ |
| L18 | **Lone-write payload collapse (RESEARCH P0.1)** — L9's trim only collapsed *superseded* write inputs, so Run-3's 24 distinct paths — every one lone — replayed their full bodies every round for the life of the turn (Run-2: body 67 → 294 KB). Now any payload write past the same 4-round working-memory window collapses on the wire copy: a superseded input keeps its byte-identical FM-L9a placeholder, the path's latest write collapses to a disk pointer (`collapse_note`), and a write whose `tool_result` failed or is missing never collapses — that payload is the conversation's only record (FM-B1a); stored transcript stays full-fidelity, IDs/positions/pairings untouched | fails-first `TestTrimSupersededToolInputsCollapsesLoneOldWrite` (red: 4036 bytes kept) + `TestTrimSupersededToolInputsNeverCollapsesFailedWrite` (red: failed payload destroyed behind a superseded placeholder); pinned `TestTrimSupersededToolInputsCollapsesOldWrite` stays byte-identical green, the recent-window guard renamed and kept; Run-4's `request-ledger` `payloads=` bucket is the before-number | ⬤ |
| L19 | **Ledger truth on the wire (L13 completion)** — `AttributeRequestBody(body)` replaces transcript-based `AttributeRequest`: buckets are disjoint `json.RawMessage` spans of the actual marshaled request (Anthropic's top-level `system` field or OpenAI's system message → system; `tools` array → tools; role-`tool` content / `tool_result` blocks → results; write-class `function.arguments` / `input` → payloads; all other text, blocks, images → history), envelope = remainder ≥ 0 **by construction** — `thinking`/`plan` blocks the serializers drop can no longer be counted because only the body is read (FM-TELe); both provider paths log the identical line format (scorecard contract unchanged) | fails-first `TestRequestLedgerAccountsEveryByte` rewritten to a hand-built body pinning exact bucket spans (red: function undefined), `TestRequestLedgerIgnoresBlocksTheSerializerDrops` (red: builds a real `toOpenAI` body with 10 KB thinking + 4 KB plan blocks — envelope must not go negative and History must exclude them; this is Run-4's `envelope=-26156` as a test), `TestRequestLedgerDoesNotMutateWhatIsSent` adapted to body bytes | ⬤ |
| L20 | **H6 catalog loads at construction (skill discovery + `read_skill` both dead in production)** — `skills.NewCatalog` performs the initial provider load itself (construction is composition start), so the merged map can never be the empty shell `Reload()`'s doc promised at a call site that does not exist; `Reload` stays the refresh seam; the prompt's skills/catalog section and `read_skill`'s library both resolve through the now-populated catalog (FM-H6a) | fails-first `TestNewCatalogLoadsAtConstruction` (red: `Names()` empty and `Manifest()` renders `""` over a provider that loads — Run-4's `skills/catalog=662` failure as a test); live evidence: zero `Reload()` callers repo-wide, `agent knowledge ready (14 skills)` beside a 662-byte catalog; Run-5 `prompt-sec` measured **1,281 B carrying all 15 names** (Run-4: 662 B, zero skills) — but the byte math is name-only (617 + 662 plugins guidance), so the ~5.5 KB this row predicted did **not** materialize: `EmbeddedProvider.Load` discards `Description` upstream of `Manifest()` → FM-H6b, closed separately as a contract-tier one-liner | ⬤ |
| L21 | **System-prompt diet (L5 ≤ 4500 tok, FM-B2a)** — `SystemPromptBuild` sheds the three duplicated/detailed sections: `## Design bar` and `## Navigation semantics` go entirely (mobile-craft / expo-router-nav already carry them; the two unique facts — `@/constants/app-styles`, the vision-gate sentence — merge into mobile-craft first), `## The live device` shrinks to a compact always-on surface law (tool-absence rule, no-fabricate verification honesty, 10/30 lifecycle, `.maestro` scope, `device-session` pointer) with the full protocol moving verbatim into the new `device-session` skill; budget enforced as persona ≤ 4500 tok *including* digest + allowlist (Run-4: persona 22168 − compiled const 15469 = 6699 B for digest+allowlist → base ≤ 11301 B, measured on the compiled string) | fails-first `TestSystemPromptBuildDietBudget` (red: 15469 B > 11301 B), `TestMobileCraftCoversDesignBar` (red: no `app-styles` / vision-gate), knowledge `TestDeviceSessionSkillCarriesProtocol` (red: skill absent — flows dir, `com.talon.app`, lifecycle, tool list); Phase A guard `TestPhaseA_DeviceAwarePrompt` keeps its tool-name + lifecycle assertions in the prompt and relocates `.maestro/flows/` + `com.talon.app` to the skill test with a comment naming the move; S9 scan unaffected (regex ignores hyphens/slashes; `read_skill` is in `coreToolSet`) | ⬤ |
| L20b | **Embedded descriptions cross the bridge (FM-H6b, L20's completion)** — `Registry.Description(name)` exposes the one-line description the bridge had no read path to (`SkillBody`/`SkillNames` only), and `EmbeddedProvider.Load` carries it beside `Name`/`Body`, so `Manifest()` renders `- **name** — <description>` instead of a dash with nothing after it; names, bodies, precedence and the source annotation untouched | fails-first `TestEmbeddedProviderCarriesDescriptionsIntoManifest` (red: **all 15** skills rendered with empty descriptions — compared against `knowledge.Registry.Manifest()`'s own rendering, so the assertion cannot pass through the bridge; L20's `TestNewCatalogLoadsAtConstruction` missed it because its `sliceProvider` already carried a Description); measured: catalog manifest **619 B name-only → 5,456 B** (+4,837 B), so `skills/catalog` 1,281 → ≈**6,118 B**; **no golden re-record** — golden fixtures don't pin this section and `prompt_regression_flips_red` stays green; Run-6 to confirm the section size and the first model-initiated `read_skill` | ⬤ |

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

## Appendix D — Run-4 (project 21 "Momentum", 2026-09-28, after L13–L17)

Same committed prompts (`e2e/prompts/momentum-{plan,build}.txt`), fresh
project, stack recipe `e2e/run-behavior.sh`. Plan → prompt 61
`success` (6 screens / 17 tasks, 0 questions; L15's gate held). Build
→ prompt 62 **`paused` at the 60-round budget**, 65m53s, 60 rounds,
avg 66 s/round (Run-3: 48 s) — same resumable round-exhaustion state
as Run-3, mid-fix-loop with the last check dirty. **First run with
L13's `prompt-sec` + `request-ledger` telemetry**, so it is the
before-evidence for L18/L19/L20/L21: the binary was built before all
four landed.

| Row | Run-3 | Run-4 | Δ |
|---|---|---|---|
| S1 first write | 11m49s ✗ | 8m21s ✗ | still failing, different cause: the 17 reads all landed in the first 25 s (3 batches of ≤10), then round 4 was a **7m56s think** ending in the first write — L17's +3-min note and +6-min warning both missed, because the round loop ticked at +11 s / +16 s / +25 s and next at **+8m22 s, after the write landed** (`first_write` events: 0) |
| S2 first route file | 13m59s ✗ | 18m08s ✗ | regressed; L3's +10-min warning **fired once** (17:14:33, round-5 tick) before the route; the +5-min advisory missed — no tick existed 17:09–17:12 (round 4 spanning it) |
| S3 scaffold | 6/6; last dirty ✗ | **2/6** plan routes (Insights, Settings); last check dirty ✗ | regressed — two thinking rounds (r4 7m56s, r6 5m18s = 13m14s of 65m53s) plus edit churn (edit_file 42, batch_conflict 9) consumed the budget before scaffolding finished |
| S4 reads before write | 19 ✗ | 17 ✗ | flat-fail; all 17 inside the first 25 s in ≤10-read batches, so **L14's cap correctly never fired** (0 `Batch capped` refusals) — the per-batch bound (FM-E1a) is not the cumulative ≤12 standard; batches are the model's choice |
| S5 throughput | 7.5/h ✅ | **1.8/h ✗** | regressed — 2/6 plan screens in 65m53s |
| S6 runnable | last dirty, 3/11 ✗ | last dirty, 4/12 ✗ | paused mid-fix-loop again |
| S7 checklist truth | 0/17 ✗ | 0/16 ✗ | flat — L11/L12 now ship in this binary, but `mark_task_done` was never called: the turn hit the round budget before close-out |
| S8 verify economy | 9% ✅ | **7% ✅** (12 run_checks ≈ 4m39s) | pass |
| S9 tool contract | 41 ✅ | 41 ✅ | pass — tools advertised per call 41 (Run-1: 18 with 8 stripped) |
| S10 input economy | 10,607→11,603 tok, 20 rebuilds ✅ | **10,803→11,773 tok, 31 rebuilds ✅** | pass; L16's attribution live — `system prompt rebuilt in round 58: project-map Δ0 bytes (47095 → 47095)`; body peak 310,816 B |
| S11 cost | ? (L7) | ? (L7) | gateway still exposes no pricing |
| S12 progress | max 183 s ✗ | max 136 s, top5 136/80/77/36/35 ✗ | breach moved from provider to **local and invisible**: round-57 `prep=2m15.649s` = prep-time `CompactWithSummary` (145→128 turns) — an unlogged summarizer LLM call inside prep (18:05:22→18:07:37); stream saw heartbeats only, serverlog only a background reconciler sweep |

- **Ledger before-numbers (this run's pre-L18/L19 binary, the only
  run that carries them)**: `payloads` grew to **70,355 B** across 60
  rounds (body peak 310,816 B) — L18's honest before; `envelope`
  reached **−98,961** (`system+tools+payloads+results+history` can
  exceed the body only when attribution counts blocks the serializer
  drops — thinking/plan), the negative envelope L19's body-spans fix
  makes impossible by construction.
- **H6 confirmed dead in production (FM-H6a)**: `skills/catalog=662`
  on every `prompt-sec` line — the plugins guidance over a zero-skill
  base; all 7 `read_skill` entries in the tool table are auto-attach
  synthetics (`id:""`, `Applied the X skill` — mobile-craft,
  expo-router-nav, mobile-defaults, talon-mobile at 17:04:48 in round
  2; expo-ui, mobile-motion, rn-best-practices later). The model
  **never initiated read_skill itself** — with no skill listed in the
  prompt it had nothing to ask for. Input-side auto-attach was the
  only channel that worked; L20 fixes discovery, L21 depends on it.
- **Persona before-number (L21)**: `persona=22168` B = const 15,469 +
  digest/allowlist 6,699 = 5,542 tok against L5's 4,500 budget.
- **L17's tick-window miss (found live)**: the gate is clock-correct
  but checked only at round-loop ticks (orchestrator.go:1494/1505) —
  a generation spanning the `[6m, write)` window skips every check, so
  the warning's evidence channel reads 0 while S1 fails. L3's route
  warning shares the pattern and fired only because a tick happened to
  exist at +10 m. Candidate L22: run the due-check on the stream/frame
  path (heartbeats already tick there), or accept and document the
  miss as tick-limited.
- **S12's compact hole (found live)**: the prep-time summarizer has
  no log line and emits nothing until it returns — a 135-second model
  call is indistinguishable from a dead server. Candidate: log +
  emit at compact start so the wait is labeled.
- Tool mix: edit_file 42, read_file 24, write_file 22, run_checks 12,
  batch_conflict 9, read_skill 7 (synthetic), grep_codebase 7,
  context_build 1, explore 1, route_scaffold 1 (synthetic — L3
  warning), inspect_package 1, compact 1 (synthetic — the summarizer
  above).
- Verdict: **FAIL** — 8 required rows. Throughput/scaffold rows
  regressed against Run-3's passes (S5, S3), S1/S4/S7 hold the same
  failure modes with sharper causes, and the run's real value is
  telemetry: every before-number for L18–L21, live confirmation of
  FM-H6a, and two new visibility findings (L17's tick window, the
  compact hole).
- Reproduce: `python3 e2e/scorecard.py --db backend/talon.db --project 21
  --prompt 62 --stream /tmp/opencode/run4-build.log --serverlog
  /tmp/opencode/run4-server.log --fresh` (report committed at
  `e2e/reports/behavior-latest.md`).

## Appendix E — Run-5 (project 22 "Momentum", 2026-09-28, after L18–L21)

Same committed prompts (`e2e/prompts/momentum-{plan,build}.txt`), fresh
project, stack recipe `e2e/run-behavior.sh`. Plan → prompt 63 `success`
(6 screens / 14 tasks). Build → prompt 64 **`failed`** at round 8 after
**10m28s** (avg 1m19s/round), killed by an **external** HTTP 429 —
`Cluster RPM rate limit exceeded` from the Atria gateway, not a Talon
defect. Burst one at 19:42:40 recovered (attempt 3/3 succeeded and the
turn ran on); burst two at 19:45:35 exhausted the stream path's
3-retry budget and died at 19:45:47 → `FAILED_NEEDS_USER`. First run
carrying L18–L21, so it is the after-evidence for them.

**Truncated-run caveat — read before comparing any row.** 10m28s
against Run-4's 65m53s. Rows split three ways:

- **Complete and comparable** — S1 (5m40s, the write landed), S2
  (7m20s, the route landed), S4 (13 reads before that write), S9,
  S10, and every telemetry row below (per-call, run-length-independent).
- **Truncated, not comparable** — S3 `1/6` (only 8 rounds to
  scaffold in), S5 `5.7/h` (a rate over one screen), S7 `0/14` (the
  turn never reached close-out). These are lower bounds, not results.
- **Unmeasured, honestly `?`** — S6 (no check after the last write),
  S8 (0 `run_checks` in 8 rounds), S11 (L7 pricing still open).

The report's headline *5 required failures* is therefore a statement
about a 10-minute turn, **not** a verdict on L18–L21. Neither L17's nor
L3's escalation could be re-tested either: both ladders' warnings come
due at +6 min / +10 min, and the artifacts landed at +5m40s / +7m20s —
so 0 warning events this run is correct by timing, and Appendix D's
tick-window finding (candidate L22) stays untested.

| Row | Run-4 | Run-5 | note |
|---|---|---|---|
| S1 first write | 8m21s ✗ | **5m40s ✗** | complete; 2m41s faster, still 2m40s over |
| S2 first route file | 18m08s ✗ | **7m20s ✗** | complete; 10m48s faster, still 2m20s over |
| S3 scaffold | 2/6 ✗ | 1/6 ✗ | **truncated** — lower bound |
| S4 reads before write | 17 ✗ | **13 ✗** | complete; still 1 over the ≤12 bar |
| S5 throughput | 1.8/h ✗ | 5.7/h ✅ | truncated rate (1 screen) |
| S6 runnable | ? ✗ | ? | no check after the last write |
| S7 checklist truth | 0/16 ✗ | 0/14 ✗ | truncated — turn never reached close-out |
| S8 verify economy | 7% ✅ | ? | 0 `run_checks` in 8 rounds |
| S9 tool contract | 41 ✅ | **41 ✅** | pass |
| S10 input economy | 10,803→11,773 tok, 31 rebuilds ✅ | **10,057→10,389 tok, 5 rebuilds ✅** | pass; `rebuild round 8: project-map Δ146 bytes (41412 → 41558)` — attribution live |
| S11 cost | ? (L7) | ? (L7) | still blocked on pricing data |
| S12 progress | max 136 s ✗ | **max 21 s ✅** (top5 21/9/7/5/5) | pass, but weak: 10 minutes with no compaction to hide one |

### L18–L21 after-numbers (vs Appendix D's before)

- **L19 envelope — fixed by construction.** `envelope` is positive on
  **all 8** `request-ledger` lines, min **+221** → max +4,022
  (Run-4: −1,894 → **−98,961**). The exact-sum line also closes on the
  wire: `system=42007 tools=20539 payloads=24229 results=26135
  history=31968 envelope=3617` = body **148,495** ✓.
- **L18 payload collapse — working, window visible.** `payloads`
  0 → 0 → 0 → 10,380 → 17,709 → 19,293 → **24,229** → **19,552**:
  growth through round 7, then round 8 *drops* it as writes age past
  the 4-round working-memory window (Run-4: monotonic to **70,355**
  over 60 rounds, no eviction ever). First run where the collapse
  seam is observable in the ledger.
- **L20 catalog loads — partial.** `skills/catalog=1281` on every
  `prompt-sec` line, carrying **all 15 skill names** (Run-4: 662 with
  zero skills). Byte math says the value is **name-only**: 619 B of
  manifest (254 B preamble + 365 B of rows rendering as `- **name** — `
  with nothing after the dash) + 662 B of unchanged plugins guidance =
  1,281. With descriptions the manifest is **5,456 B**, which is what
  L20's row predicted (~5.5 KB) — **so L20's stated evidence did not
  materialize**; the descriptions are being dropped upstream of the
  manifest. That gap is FM-H6b below, and it is why discovery still
  reads 0.
- **L21 diet — met.** `persona=17922` **constant** across all rounds
  (Run-4: 22,168 → −4,246 B ≈ −1,060 tok) = **4,480 tok against L5's
  4,500 budget**; system prompt 10,057 → 10,389 tok (Run-4:
  10,803 → 11,773 — Run-4 *grew* past the budget, Run-5 does not).

### Findings this run added

- **FM-H6b (L20's missing half) — the bridge drops `Description`.**
  `EmbeddedProvider.Load` (skills.go:58) builds
  `knowledge.Skill{Name, Body}` from `Registry.SkillBody`, discarding
  `Description` that `knowledge.Registry` loaded correctly — so the
  catalog's merged map holds descriptions that `Manifest()` (line 217)
  then renders as `- **name** — ` with nothing after the dash.
  Verified with a throwaway diagnostic (since deleted): the registry
  reports the descriptions, the manifest emits empty ones. Two
  consequences: (i) the manifest's own "match the description"
  instruction is unsatisfiable — nothing to match against; (ii) Run-5
  repeats Run-4's **0 model-initiated `read_skill`** — all 4 calls
  (`mobile-craft`, `expo-router-nav`, `mobile-defaults`,
  `mobile-motion`) are auto-attach synthetics with `id:""`. The
  discovery chain is now *listed but unnamed* instead of *unlisted*;
  one field short of working. Contract-tier: it completes L20.
- **429 backoff is sized for 502s, not for a rate window.**
  `openStream` sleeps `attempt*3s` (client.go:954) → **3s then 6s =
  9 s of sleep**, ~15 s wall (19:45:32 → 19:45:47) against a
  **per-minute** RPM window. Both bursts therefore died in ~10–15 s
  instead of waiting out the 60 s that would have let the turn
  continue; the retry budget was spent before the window reset. A
  429 needs its own backoff (honor the window, not the 502 ladder).

- Tool mix (8 rounds): read_file 10, write_file 9, read_skill 4
  (all synthetic), context_build 1 — **0 `run_checks`**, 0 git
  commits, 0 checklist marks.
- Verdict: **FAIL — 5 required rows**, with the truncation caveat
  above. The run's durable value is the telemetry: L19 proven exact,
  L18's eviction window seen for the first time, L21 inside budget,
  L20 confirmed partially loaded with its remaining defect named.
- Reproduce: `python3 e2e/scorecard.py --db backend/talon.db --project 22
  --prompt 64 --stream /tmp/opencode/run5-build.log --serverlog
  /tmp/opencode/run5-server.log --fresh` (report committed at
  `e2e/reports/behavior-run5.md`, copied to `behavior-latest.md`).
