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
- **FM-D1c (L22)** — a wall-clock gate sampled only at round ticks is
  not a wall-clock gate. L17's first-write warning and L3's route
  warning both read `time.Since(sess.StartedAt)` correctly and both
  fire only when the round loop ticks (orchestrator.go:1510/1516) —
  so the *sampling*, not the clock, decides whether the event exists.
  Run-4 measured the miss: round 4 generated for 7m56s straight (ticks
  at +11 s, +16 s, +25 s, next at +8m22 s — after the write had
  already landed), so the +6 min threshold passed inside a window with
  no tick in it, `FirstWriteWarnDue` was never even asked, and the
  run's `first_write` evidence reads 0 while S1 fails at 11m49s. The
  gate cannot make the next round arrive sooner — a generation owns
  the loop — so any check that waits for a round boundary is checking
  *rounds*, not minutes, and the scorecard (which grades minutes)
  grades an event that could not exist. L3's route warning shares the
  pattern and only fired in Run-4 because a tick happened to land
  after +10 min. Two failure modes under one law:
  (i) the evidence channel is silent exactly while the violation is
  active — the loudest moment of the failure produces the least
  signal, and the after-the-fact reading ("why did S1 fail?") has no
  event to read;
  (ii) the one-shot latch makes the miss permanent for the turn — the
  warning that never fired inside the window cannot fire after it
  either (the write lands, the gate closes, the latch is never
  claimed).
  The law: the due-check runs on its own wall-clock ticker for the
  life of the turn — the build heartbeat's 15 s cadence, not the round
  boundary — same due functions, same one-shot latches, same event
  shape and text. Whichever caller (round tick or ticker) observes the
  threshold first claims the event: a turn that ticks inside the
  window behaves exactly as today, a spanning generation gets its
  event within one tick of the threshold. The advisory *note* stays on
  the card — it renders per round by design, FM-D1b's one voice — and
  only the warning event, the evidence channel, leaves the round
  clock.
- **FM-L30a (L30)** — the tools layer gets one law or it gets a hole,
  and this deployment's two wires had three different laws. The layer
  is the input side's quiet giant: 38 tools = 20,125 B of production
  defs (Run-8: 41 tools, 22,216 B ≈ 5,100–5,550 tok) resent every
  round, of which Run-8's model called 14 — and that count is exactly
  why a diet sounds obvious and is mostly not lawful here. Four
  measured facts, four consequences:
  (i) *The availability half of a diet is already complete.* Every
  tool with a hard precondition is registered behind it — device
  tools on `adbTarget`, read_skill on a library, lsp on a resolver,
  codebase_search on an index, delegation on its service, plugins on
  pins — the registry's own law, "offering a tool that can only fail
  wastes rounds." Everything ungated degrades honestly instead of
  failing: git answers "not a git repository — no baseline," the
  install/build pair nil-checks its allowlist. There is no honest
  predicate left to gate, so the byte win from gating is ~0 and a
  gate invented past the evidence would be a fiction.
  (ii) *Relevance-hiding is the amputation this codebase already
  paid for.* The P1-era valve at 6500 tokens fired on every request
  (the system prompt alone is ~8.5K), stripped 8 prompt-promised
  tools, and Run-1 answered "I have no shell" — S9 was born to catch
  it, and coreToolSet now carries every name the prompt promises.
  A one-run unused-set is noise on top of that: Run-8 called
  delete_file 18× and git_commit 8×, tools other runs skip. Hiding
  what the model might want is not a diet; it is the model planning
  around a menu it was never shown — and L20's measured law says it
  will not ask for what it cannot see.
  (iii) *Prose-thinning is the silent degradation this item itself
  warns about.* The descriptions are contracts: run_shell's carries
  the tier-2 approval flow, ask_user's carries the never-ask-for-
  secrets rule (measured by dumping the production defs). No
  behavioral harness measures tool-choice quality, so a trim cannot
  be verified — it can only be discovered in a run.
  (iv) *And yet the OpenAI wire thins them anyway — the uncured half
  of P1.* Commit `7b2f5a4` ("compress tool descriptions and filter
  to core tools when over Groq limit") shipped two halves together.
  The threshold half was cured: 6500 → a 100K valve with a
  contract-guarded core set. The description half never was:
  `chatWithToolsOpenAI` still cuts **every** description to its
  first ≥160-char sentence on **every** request, sized for a context
  limit that the same file's comments declare dead ("a size valve
  became a permanent capability amputation") — run_shell loses its
  approval flow, ask_user loses its safety rule, on the wire, with
  no test and no scorecard row (the runs are anthropic). The
  anthropic wire, meanwhile, carries neither half: descriptions
  whole (correct) but no valve at all — a pathological body (RESEARCH
  measured 294KB; Run-8's max was 234KB) has no safety valve where
  the scorecard actually runs.
  The law (L30), in the shape FM-TELf and FM-ANTa already set — one
  emitter/parser/contract, both protocols: **(a)** descriptions
  travel whole on every wire; the tool description is a contract with
  the model, so the truncation that thins it does not exist (P1's
  context-limit compression applied unconditionally is the same
  disease the threshold cure named, one floor down); **(b)** the size
  valve applies on both wires at the same threshold with the same
  contract-guarded core set — a valve for pathological bodies only,
  100K sitting above every honest body this deployment produces
  (Run-8's largest est ≈ 58K), its failure direction documented by
  Run-1: when the valve bites an honest request the fix is a
  threshold, never a smaller contract. #11's reduce direction is
  thus closed by measurement — (i) shows the lawful gating is
  spent, (ii)+(iii) show what remains is refused with evidence —
  and this row delivers the half of it that was actually broken.
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
- **FM-429a (L23)** — a retry budget measured in attempts
  cannot outlast a rate window measured in minutes: `openStream`
  backs off `attempt*3s` (3s, 6s = 9 s total) because that ladder was
  written for flaky 502s, so Run-5's `Cluster RPM rate limit exceeded`
  bursts burned the whole budget in ~15 s and killed a healthy turn at
  round 8 while the 60 s window that would have cleared it was still
  open. Four sub-defects under one law:
  (i) the streaming path — the only one plan and build turns take —
  never reads `Retry-After` at all, though `chatAnthropic` and
  `chatOpenAI` parse it, so the gateway's own word on when the budget
  refills is thrown away exactly where it matters;
  (ii) on those two paths the *no*-`Retry-After` case falls through to
  the loop-top 3s/6s ladder — the same futility as (i) — and one real
  stall reports twice (the status branch's `server error (HTTP 429)`,
  then the loop top's `transient error`), so a single wait has to be
  owned in exactly one place: sleeping in the status branch as well
  would stack window + 3s on every 429 and double every 502's ladder;
  (iii) lengthening that wait creates the mirror failure: `chatOpenAI`
  has no `isQuotaExhausted` guard where `chatAnthropic` has one, so a
  spent daily budget — not throttling, never clearing on any window —
  would start burning two full windows before failing instead of
  failing at once. The guard ships with the fix or the fix is a
  regression;
  (iv) the fourth gateway call site, `doWithRetry` (the buffered
  agentic path and every vision call), already honours `Retry-After`
  and already quota-fails-fast, but with no header its own ladder is
  2 s/4 s/8 s ≈ 14 s — the same futility a third time — and it sleeps
  `Retry-After` inline *and* the loop-top ladder on the round after,
  so the one wait it does take is longer than the one it announced.
  The law: a 429 waits the gateway's `Retry-After` when sent, else the
  rate window; quota exhaustion is never a wait; every other transient
  keeps its own ladder exactly — 3 s/6 s on the chat paths, 2 s/4 s/8 s
  in `doWithRetry` (a window-sized backoff would stall a 502
  recovery); and a wait long enough to matter must be
  cancellable, because an unstoppable 60 s sleep turns "stop this
  turn" into a 60 s hang.
- **FM-UAa (L24)** — transport identity is part of the
  provider contract, but the backend pins `User-Agent: Talon/1.0` at
  two call sites and sets **none** on the rest, so the streaming path —
  the only one plan and build turns take — inherits Go's
  `Go-http-client/1.1`. agentrouter.org authenticates on exactly that
  header: anything other than `claude-cli/2.0.0 (external, cli)` is
  refused with `401 unauthorized client detected` *before* the key is
  read (a header sweep proved only that exact string passes: `Talon/1.0`,
  the curl default, `claude-cli/1.0.0`, `claude-code/2.0.0` and
  `anthropic-sdk` all 401). The consequence is that the second provider
  the model comparison exists to measure is blocked by a **constant**,
  not by a capability — no key, URL or model setting can reach it.
  Two failure modes under one law:
  (i) an identity a gateway keys on must be configurable (`AI_USER_AGENT`),
  because there is no other way to satisfy it;
  (ii) it must be set where requests are *built*, not where they are
  visible — patching the two hardcoded sites and missing the streaming
  one is a silent 401 on the path that matters, and each new request
  path repeats the mistake, so the header belongs in the one constructor
  all four request builders share.
  The law: a single choke point builds every gateway request and
  attaches the identity; `AI_USER_AGENT` overrides the default
  `Talon/1.0`; and a client cloned for another provider carries it —
  a field dropped at a copy boundary is exactly FM-H6b's shape.
- **FM-URLa (L25)** — the anthropic paths disagree about where the
  Messages API lives, so no single `AI_BASE_URL` can run a provider
  end-to-end. Four call sites build it four ways: `chatAnthropic` and
  `chatWithToolsAnthropic` append `/v1/messages`, while `streamAnthropic`
  and the vision call append `/messages`. Plan mode streams and the
  build loop buffers, so both spellings are exercised by *one* turn —
  pick a base ending in `/v1` and the build loop POSTs `/v1/v1/messages`
  (404 `Invalid URL`); pick a bare root and the plan stream POSTs
  `/messages`. Measured on agentrouter.org, where the wrong path does
  **not** 404: it answers `HTTP 200` with the SPA's `index.html`, so the
  stream reader finds an HTML document where a `data:` frame was
  expected — the mismatch surfaces as a parse error or a silently blank
  plan, never as "wrong URL". Two failure modes under one law:
  (i) a path built in more than one place is a contract that has already
  forked — the openai side never had this because every site appends
  `/chat/completions` the same way, which is why it survives;
  (ii) the fork must not be papered over per-provider in config: any
  scheme where the operator has to know which code path their turn will
  take (stream or buffered) is not a configuration, it is a trap.
  The law: one endpoint builder for the Messages API, called by all
  four sites. It accepts both spellings of the root — `https://host`
  and `https://host/v1` (the convention the openai side and every
  gateway use) — and returns the one URL the API is actually mounted
  at, so a base that ends in `/v1` is never doubled and a bare root is
  never left short. This is L24's lesson one line down: L24 unified
  where the *headers* are set, L25 unifies where the *body* goes.
- **FM-ANTa (L28)** — the anthropic protocol drops the
  `StreamSink` at the dispatch boundary, so every agentic round on an
  anthropic provider is silently buffered. `ChatWithTools` forwards the
  sink to `chatWithToolsOpenAI` but calls
  `chatWithToolsAnthropic(ctx, system, history, tools)` with no sink
  argument at all — the parameter simply does not exist on that
  function — which means the build loop's one streaming capability
  (text and reasoning reaching the wire as the model produces them)
  exists on openai and not on anthropic. The caller cannot see which
  wire it got: the orchestrator passes a sink on both protocols and
  gets live deltas on one and nothing on the other. On anthropic the
  client sees heartbeats and then the finished round, so a model that
  thinks for two minutes reads as a dead connection — exactly the
  failure L8 was written to kill ("no sign the turn was alive"),
  reintroduced by protocol rather than by omission. Run-7's scorecard
  grades S12 (max gap ≤ 90 s between real events, heartbeats
  excluded) against a wire this path never writes to, so the gap it
  measures is the round's duration, not the product's visibility.
  Two failure modes under one law:
  (i) a capability present on one protocol only is a fork, not a
  feature — nothing downstream (progress, S12, cancel-visibility) can
  rely on it, because it cannot know which protocol it is on;
  (ii) dropping the argument at the dispatch site is FM-H6b's shape
  one level up: L22/H6b found a *field* lost at a copy boundary, this
  loses a *parameter* at a call boundary, and both fail silently —
  the call compiles, the round succeeds, the wire stays dark.
  The law: every protocol path receives the sink, and a non-nil sink
  means the round streams — and the SSE parse itself becomes ONE
  reader both anthropic consumers share, because two parsers for one
  protocol's stream is the same fork by another name. The reader
  accumulates what the agentic round needs (`input_json_delta` into
  the tool input, stop_reason, usage) while reporting deltas as they
  arrive, so the plan turn keeps its event stream and the build round
  gets its sink from the identical parse. A nil sink keeps today's
  buffered shape: buffering on request is correct, buffering
  *unconditionally* is the defect. Measured: Run-8's S12 breach — one
  buffered round of 2m12.9s left a 133s hole in the wire (FM-ANTa's
  first scored failure, `phase 00:05:08 → usage 00:07:21`).
- **FM-PLANa (L26)** — a plan turn that lost its plan reports success.
  Run-7's plan stream ended mid-array (`… "app/onboarding.tsx",`) with
  17,081 chars stored, and the turn was still marked `completed` with
  `done {status: success}` on the wire: `json.Unmarshal` failed, the
  handler fell into its `else if raw != ""` branch (written for a model
  that answers in prose), `planAsksQuestion` said no, and the success
  arm ran. Project 24 was left with `plan_json = null` while every
  layer above — drive.py, the scorecard, the UI — read a green turn, so
  the build ran planless: `plan screens: none found`, S3 fell back to
  counting route files, S5 unmeasurable, S7 scored 0/0. Three failure
  modes under one law:
  (i) the reader never learns the stream was cut — `streamAnthropic`
  does not read `stop_reason` at all, so a `max_tokens` end and a clean
  `end_turn` are indistinguishable after the fact;
  (ii) the prose fallback swallows broken JSON — output that *opens*
  with `{` is an envelope attempt, not a message, and reporting it as
  success hides the one fact anyone needs (the plan is gone);
  (iii) the ceiling was below what a thinking model emits: `max_tokens`
  is 16384 while one Run-7 build round alone produced `out=10628`, and
  the truncated plan's JSON was ≈4,300 tokens before it was even
  finished — a replay of the same prompt shape completed at 5,447
  output tokens, so a plan plus its thinking sits over the line.
  The law: the stream surfaces its stop reason and ends as an error
  when the gateway says `max_tokens`; plan output that opens as JSON
  and fails to parse is a **failed plan turn** (terminal `failed` +
  an error frame), never `completed`; prose that does not open with
  `{` keeps the message path exactly as it is; and the output ceiling
  leaves room for thinking plus the envelope.
- **FM-TELf (L27)** — the telemetry line the scorecard parses is
  emitted by protocol, not by call. Run-7's server log carries 60
  `request-ledger` lines but **zero** `est_input_tok=` lines (Run-6:
  59), because that log lives inside `chatWithToolsOpenAI` only —
  `chatWithToolsAnthropic` logs its `stop=` line and nothing else.
  The scorecard therefore returned `? (needs --serverlog)` for S9
  (tools advertised) and S10 (prefix stability), and under §5 an
  unmeasured required row **blocks the run**: the gap did not degrade
  Run-7, it voided two rows outright and made the run unusable as a
  comparison point. Two failure modes under one law:
  (i) an estimate that exists to answer "what do we trim?" must be
  produced by every path that can be trimmed — a diagnostic the
  operator only gets on one protocol is a diagnostic for one provider;
  (ii) the scorecard's contract is a *format*, so the fix is not a new
  line but the same line from a shared emitter — adding an
  anthropic-flavoured variant would fork the parser instead of the
  telemetry.
  The law: one helper emits the estimate for every agentic path, in
  the exact format the scorecard parses, called where the openai path
  calls it today (after marshal, before send).
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
- **FM-CMPa (candidate L29)** — the prep-time context compaction is a
  model call nobody announces. When `EstimateTokens(conversation)`
  crosses `MaxContextTokens` the round loop calls `CompactWithSummary`
  straight: no log line, no frame — the first signal is the
  `compact … completed` tool call emitted *after* it returns. The
  summarizer is one `chat` round over the whole to-be-dropped span
  (found live: round 57's `prep=2m15.649s`, 18:05:22 → 18:07:37), so
  for two minutes the wire carries only heartbeats — which FM-L8a
  already ruled cannot count as progress — and the turn that is
  *working* (writing a checkpoint of its own context) is
  indistinguishable from a dead server. S12 grades the silence: the
  gap lands as a required-row breach. The operator sees nothing
  either: no serverlog line says compaction began, so the
  dashboard's own background sweep is the only motion at +2 m.
  Two failure modes under one law:
  (i) an unannounced long-running call is indistinguishable from a
  hung one — the scorecard cannot tell "compacting" from "dead", and
  it must guess; S12's row counts the guess as a breach;
  (ii) a wait with no log line leaves no after-the-fact evidence —
  "why was round 57 slow" is unanswerable from the log.
  The law: every wait longer than one frame is labeled *before* it
  starts — a `running` frame and a log line at compaction entry,
  paired with the `completed` frame already emitted at exit. The
  frame is not decoration: S12's gap clock resets on any real frame,
  and running/completed is the emitter's own pairing contract, so
  the tool mix still counts exactly one `compact` call — the wait
  becomes measurable instead of invisible.
- **FM-CMPb (candidate L31)** — the checkpoint that survives
  compaction is written in a shape that buries the two things a cold
  reader needs most, and the ceiling it enforces was sized for a
  context window this deployment no longer runs. Two failure modes:
  (i) *Shape.* The instruction's eight sections — Primary Request and
  Intent / Key Technical Concepts / Files and Code / Errors and Fixes /
  Pending Jobs / Current Work / Next Step / Critical Context — mix
  durable state with volatile state in one flat list, and have no
  home at all for two recovery-critical facts: a stated requirement
  that must not be re-litigated, and a blocker with who must act.
  A resume needs a fixed header: what was asked (**Objective**), what
  constrains it (**Requirements**, verbatim — a paraphrased
  requirement is a lost requirement), what was decided
  (**Decisions Taken**, carrying each fix tried and abandoned with
  its reason so it is never retried — today's "Errors and Fixes"
  rule preserved), where work stands (**Completed / Active /
  Blocked / Next Move**), what to open (**Relevant Files**), and what
  must not be missed (**Important Context**). The evidence for the
  shape is this document's own session-checkpoint protocol: a
  session resumed from exactly these sections picks up cold without
  re-asking.
  (ii) *Ceiling.* `MaxContextTokens = 120_000` was sized — the
  comment says it: "leave room for the system prompt, tool
  definitions and a full response" — for a 200K window (120K conv +
  ~29K overhead + response ≈ 181K). The models this deployment now
  serves publish 1M windows: claude-opus-5 is 1M context / 128K
  output per Anthropic's context-window docs and two independent
  catalogs; deepseek-v4-flash is 1M per DeepSeek's release notes and
  the V4 paper. The compactor therefore fires at ~6% of the window,
  pays a full summarizer model round for the privilege (FM-CMPa's
  2m15s), and cuts memory long before the window pressures.
  The law: the checkpoint is written in the guide shape — the ten
  section lines in fixed order, empty sections "(none)", never
  dropped — and the ceiling is `MaxContextTokens = 200_000`
  conversation tokens, sized against measured 1M serving windows
  (peak request ≈ 200K + ~29K overhead + ≤128K output ≪ 1M). Watch:
  Atria-Dawn-Preview publishes no window spec; on a window < 260K the
  first conversation past ~171K 400s *before* the compactor can fire —
  the signal is a provider "prompt too long", the fix is a lower
  const, never a retry. The shape gate accepts the new sections and
  the legacy anchor alike: a legacy checkpoint echoing from kept
  history still beats the static fallback line, and the gate exists
  to reject ramblers, not to enforce fashion. Groq-shaped
  constraints are out of scope by operator decree — no threshold,
  budget, or section in this row considers the 6500-era sizing
  (L30 recorded the same decree against the tools layer).
- **FM-DEVa (candidate L32)** — the APK download is gated behind a
  session that a first-time phone does not have yet. The "Get the
  app" pane exists for the phone that has never seen Talon: install
  the dev client, *then* pair. Two failure modes: (i) *server.*
  `TestInfoHandler.Info` early-returns on "no live session" and on
  "no relay" before `DevClientURL` is ever set, so the only pointer
  to `GET .../dev-client.apk` exists while a session runs — while
  the download itself needs no session (owner-scoped static file).
  The wire hides a door that is actually open. (ii) *web.*
  `TestOnPhone` gates `apkUrl` on `info.available` and renders the
  link only inside the `available` branch, so even a server that
  advertised the URL would not show it; the unavailable branch
  offers only "start a device". The law: `dev_client_url` is
  advertised whenever the APK exists on disk — session or not — and
  both clients render it whenever it is present; with the URL
  honest, an absent URL means exactly "not built", so the fallback
  copy stays true.
- **FM-DEVb (candidate L32)** — the mobile cache outlives the
  session it describes. The Device tab's own law is "every value
  comes from the backend", but `useCreateSession`/`useDestroySession`
  invalidate only `session` keys — never `['test-info']`. Failure
  modes: (i) start a session → the tab keeps `available:false` /
  "Not available yet" with no QR, deep link, or APK until a
  remount; (ii) destroy it → the tab keeps serving a dead deep link
  and a QR pointing at a relay that no longer exists — the exact
  "link that 404s on the phone" the backend refuses to emit,
  reproduced client-side; (iii) `useCreateSession` writes a
  `['session', project_id, 'id']` cache entry no code ever reads —
  a phantom second source of session identity beside the documented
  chain (server live-session → local state → SecureStore). The law:
  session mutations invalidate the queries derived from session
  state, the phantom write is gone, and test-info polls on the same
  5 s rhythm as the session queries so a session started from
  another client converges too.
- **FM-DEVc (candidate L32)** — the Device tab hand-rolls the metro
  SSE subscription that `useMetro` already implements safely.
  `device.tsx` does `openMetroStream(...).then(c => (close = c))`
  with cleanup `close?.()`: unmount (or a project switch) between
  mount and promise resolution finds `close === undefined` and
  closes nothing — a socket stays open to a screen that no longer
  exists, and everything it delivers duplicates the next mount's
  events. The leak-free pattern (`alive` flag; close on resolve if
  already dead) sits in the same file's hook barrel, unused. The
  law: one subscription implementation, the leak-free one; the
  screen slices it, never opens it.
- **FM-DEVd (candidate L33)** — two shipped apps claim one Android
  package, and the installed-check probes a package nothing builds.
  (i) *Conflict.* `backend/template/app.json` (the prebuilt dev client)
  and `talonmobileapp/app.json` (the studio's own app — the surface the
  user builds from) both declare `com.talon.app`. Android allows exactly
  one installed holder of a package name: installing the second evicts
  the first or fails `INSTALL_FAILED_UPDATE_INCOMPATIBLE`, so a phone
  cannot carry the studio app *and* the dev client — and it must carry
  both, because the dev client is what the QR opens. (ii) *Drift.*
  `devclient.IsInstalledOnDevice` probes the literal `com.talon.devclient`,
  which no build has ever produced; `pm list packages <filter>` is a
  substring match, so an installed client answers empty, the check
  returns false, and `InstallDevClient` re-installs the whole APK on
  every session start while `sessions.go`'s `wg.Wait()` — and therefore
  Metro — sits behind that goroutine. The literal was right and the file
  was wrong, and nothing in the tree said so. (iii) *Re-claim.*
  `identity.Generate` derives `com.talon.<slug>` and a scheme from the
  slug with no reserved set: a project named "App" is handed
  `com.talon.app`, one named "Talon" the studio's `talon://` scheme, and
  `run_boot`'s `adb install -r` then evicts whichever studio app held
  them. (iv) *Inheritance.* `renderAppJSON` writes the project's package
  only when the template already has an `expo.android` object to write
  it into — when it has none, the generated app.json silently keeps
  whatever package the template declared, which after the fix would be
  the dev client's own. The law: the dev client owns
  `com.talon.devclient`, that exact name is what the installed-check
  probes and what the shipped APK must declare, generated identity may
  never be assigned either studio app's package or scheme, and a
  rendered project always carries its own package.
- **FM-DEVe (candidate L33)** — both surfaces that *name* a package name
  the wrong one. (i) *Skill.* `device-session` tells the agent to author
  Maestro flows with `appId: com.talon.app`, which is not the package
  any generated project installs — `buildsession.go` boots
  `proj.AndroidPackage` from the workspace's own APK — so for every
  project whose slug is not literally "app" the flow targets a package
  that is not on the device and `run_e2e` fails at launch. (ii) *Panel.*
  `TestOnPhone` says "Scan with your phone camera, or open in the Talon
  dev client", but the shipped APK's merged manifest registers only
  `talonapp`, `exp+talon-app` and `expo-dev-launcher` — never `exp` — so
  a camera tap on the `exp://` QR fires a VIEW intent nothing handles,
  or, if Expo Go happens to be installed, opens a client that lacks this
  project's native modules. The law: the skill names the project's own
  `expo.android.package`, and the panel names an entry path the
  installed APK actually serves.
- **FM-DEVf (candidate L34)** — on one phone there is no second screen to
  scan, so the entire pairing path collapses onto a single button:
  `device.tsx`'s "Open on this phone" → `Linking.openURL(deep_link)`.
  Failure modes: (i) *Unresolvable scheme.* `deep_link` is `exp://…`, and
  the shipped dev client's intent-filters declare exactly `talonapp` and
  `exp+talon-app` (on `MainActivity`) and `expo-dev-launcher` (on
  `AuthActivity`) — never `exp`. An `ACTION_VIEW` carrying scheme `exp`
  matches nothing in the APK, so `Linking.openURL` rejects, the `void`
  discards the rejection, and the tap does nothing at all; if Expo Go
  happens to be installed, the manifest's `<queries><package
  host.exp.exponent>` lets *it* answer instead — a client without this
  project's native modules. (ii) *Wrong target shape.* expo-dev-launcher
  only reads `<scheme>://expo-development-client/?url=…`:
  `DevLauncherURLHelper.kt:7` checks the host, `:9-11` the `url`
  parameter, and `DevLauncherController.kt:245` bails to
  `handleExternalIntent` before `loadApp` ever runs — so the raw `exp://`
  form would not be read as an instruction even where it resolved. That
  raw form stays correct for the QR and the dev client's in-app scanner,
  which parse the string themselves. (iii) *Silent and unexplained.*
  Nothing reports the failure — no error, no fallback — and the only
  guidance in the app (`help.tsx`) describes pairing a *second* phone, so
  a manual end-to-end pass reads as "the button is broken" with no cause.
  The law: the server hands out two links — `deep_link` keeps its `exp://`
  shape untouched for the scanner, and `dev_launcher_link` wraps it as
  `<template scheme>://expo-development-client/?url=…` for the intent,
  with the scheme pinned to `backend/template/app.json` the way
  `DevClientPackage` is — the mobile action opens the launcher link with
  the raw one as fallback, and at least one link the wire advertises must
  be one the shipped APK can resolve.
- **FM-CRDa (candidate L35)** — the paywall must charge for the only thing
  that costs money, and for nothing else. Failure modes: (i) *Wrong
  target.* `gateExport` (`export.go:62`) enforces ownership **and** tier and
  is shared by `Create` (:98), `UploadAsset` (:285) and `ListAssets` (:368).
  Flattening it into the credits check as the plan words it ("one change at
  export.go:73") would stop an owner with no balance from *listing their own
  screenshots* — and `ExportPanel` calls `ListAssets` the moment it opens,
  so the sheet that is meant to sell the credit would 402 before it could
  render. Preparation stays free; only the build costs. (ii) *403 is the
  wrong signal.* A 403 says "forbidden, ever", and today's refusal is a Pro
  upsell sentence with no machine-readable reason, so both clients would
  have to parse prose to learn why. The refusal must be a **402 whose body
  carries the offer** — spare balance, pack size, pack price — so the unlock
  sheet renders without a second round trip. (iii) *Check-then-spend races.*
  Two exports starting together against a balance of 1 must not both proceed:
  a read followed by a separate decrement is exactly how a zero-credit owner
  launches parallel builds, so the spend has to be one conditional UPDATE
  that reports whether it took effect. (iv) *A second copy of the balance.*
  Anything other than `SUM(remaining)` over `export_credits` — a counter on
  `users`, a number the client holds — drifts from the ledger the way
  `quota.go`'s three disagreeing limit copies did. (v) *Paid tiers start
  paying by accident.* `exportAllowed` keeps passing untouched: a Pro owner
  has no credit rows, must never see a 402, and must never have a row
  created for them. The law: **the release build is the only paid surface** —
  ownership is checked on all three routes, the tier-or-credit check and the
  single atomic spend live only on `Create`, and the 402 body *is* the offer.
- **FM-SKRa (candidate L36)** — a signature is not a payment. Failure modes:
  (i) *Trusting the client.* `{signature}` is a claim about the chain, made by
  the party that wants the credit. Granting on receipt lets anyone mint
  credits by typing random base58, so the verdict must come from a
  `getTransaction` the **server** issues against the operator's configured
  endpoint — and the signature must be shape-checked before it goes anywhere
  near that call. (ii) *Verifying the wrong transfer.* A real, successful
  signature is still not enough: the transaction may pay too little, pay an
  address that is not our treasury, pay in a look-alike SPL mint, or have
  failed outright while still returning a transaction. All four are read off
  `meta`: `meta.err == nil`, plus a token-balance entry whose `mint` is the
  configured one and whose `owner` is the configured treasury, whose increase
  equals exactly the pack price in raw units. Filtering on mint **and** owner
  makes a wrong-mint look-alike unbuyable rather than half-priced.
  (iii) *Decimal drift.* The pack is quoted in whole SKR ("100") while the
  chain counts base units; a wrong exponent makes it either unbuyable or
  half-priced. Decimals are therefore read from the chain's own
  `uiTokenAmount.decimals` on the very balances being checked — never from
  `SKR_DECIMALS` — with the startup probe of the mint account kept purely as
  an operator warning, because a typo'd mint otherwise becomes a silent black
  hole where nobody can ever buy. (iv) *Replay.* The same signature verified
  twice must grant once: `tx_signature UNIQUE` already backstops it in the
  ledger, and the handler answers a duplicate with the balance the owner
  already has rather than an error the client will retry. (v) *Not-found is
  not invalid.* A freshly signed transaction is invisible until it lands, so
  "unverified" must split into **pending** (retry) and **wrong or failed**
  (give up) — one blanket refusal teaches the client to abandon a payment
  that is actually confirming. (vi) *The oracle is operator config.*
  `SKR_RPC_URL`, `SKR_MINT` and `SKR_TREASURY` are read from the environment
  at startup and nothing in the request can redirect them, and the call carries
  a timeout so a hung RPC fails closed instead of hanging the request. The law:
  **the server grants only what it has re-derived from the chain it was
  configured to trust** — `/v1/skr/quote` states the price, `/v1/skr/verify`
  re-derives the payment, and the ledger's unique signature makes a second
  attempt a no-op.
- **FM-SKRb (candidate L37)** — the phone must be able to pay, and must never
  claim a payment it did not make. Failure modes: (i) *The refusal is
  invisible.* The backend's 402 now carries `code`, `spare_credits`,
  `skr_amount_per_pack` and `credits_per_pack` so a sheet can render the offer
  from the refusal itself — but `parseResponse` (`lib/api.ts:52`) builds
  `new Error(body.error)` and attaches only `.status`, so **every field that
  names the price is dropped before a screen can read it**. The body must ride
  the error additively, message unchanged, the way `.status` already rides it
  (line 808 is the consumer that proves the pattern). (ii) *Paying a price the
  app invented.* Amount, mint and treasury all come from `GET /v1/skr/quote`.
  Baking `SKRbvo6Gf7…` or "100" into the binary means the offer cannot change
  without an app release and a mint cannot be rotated — the same reason the
  server quotes the price instead of trusting a `skr_amount` in the request.
  (iii) *A destination that is not a token account.* The treasury holds a
  token account only once something has paid it, and a plain transfer to a
  missing account fails on-chain; so the account is created **idempotently**
  first (`CreateIdempotent`, not `Create`, or the second purchase fails), and
  the transfer is **`TransferChecked`** — it carries the mint, so a mis-derived
  destination fails instead of quietly paying a valid account of some other
  token. (iv) *Reading "sent" as "paid".* `sendTransactions` resolves when the
  transaction is **submitted**, not when it lands; verifying immediately asks
  about a signature the chain has not seen, and `pending` gets read as failure
  so the credit is never granted. The client polls verify until the verdict
  leaves `pending`, bounded, and starts the build only on `verified`.
  (v) *Paying twice on a retry.* If verify answers `pending` and the user taps
  again, a second transfer would be built for one purchase; the signature
  survives the send, so retries **re-verify the same signature and never
  re-send** — which is exactly what the ledger's UNIQUE `tx_signature` assumes
  it will do. (vi) *Decimals guessed in the app.* The chain counts base units,
  so the exponent is read from `getTokenSupply` on the quoted mint, never a
  constant: a hardcoded `6` sends ten times the price on a 9-decimal mint and
  a tenth on a 4-decimal one, and the app would have to be re-released to fix
  either. The law: **the phone quotes, pays once, verifies against the chain,
  and only then spends a credit** — it proposes no price, no mint and no
  verdict of its own.

- **FM-SUBa (candidate L38)** — a paid export must hand over *how to submit
  it*, and must not author any part of that answer itself. Failure modes:
  (i) *The bundle is written but never handed over.* `WriteBundle` puts
  `dapp-store.json` and `SUBMIT.md` beside the APK and `ExportJob` carries
  `manifest_path` — a **server-local filesystem path**. The phone and the
  browser are not on that filesystem: no route serves the bundle, so the
  prefilled manifest and the exact submit steps exist on disk and reach the
  user nowhere. Someone who just paid receives an APK and no idea what to do
  with it. (ii) *The portal link guessed at, the package name nowhere.* The
  plan asks for "a deep-link to the Publisher Portal carrying their package
  name"; Solana Mobile documents no such thing — the portal is
  `https://publish.solanamobile.com` with no path and no query (you find the
  app in the sidebar after login), and the only documented package-carrying
  scheme, `solanadappstore://details?id=`, opens the **listing page**, which
  does not exist until the app has been approved. So the honest assist is the
  documented plain URL **plus the package name as visible copy beside it**,
  because `SUBMIT.md` already tells the user the portal matches the app *by
  package name*. Typed into a client, either value drifts: the portal moves,
  or a panel shows a package the artifact was never signed against — the same
  reason the price is quoted and never computed. (iii) *The costs note
  re-authored per surface.* `manifest.go`'s `costsNote` (~0.2 SOL for fees and
  ArDrive storage, KYC/KYB, 2–5 business day review, "Talon never touches your
  publisher wallet") is the disclosure; the web panel ships a shortened
  hand-typed sentence instead and the phone shows nothing at all. Two
  paraphrases of a money disclosure is a disclosure that eventually says
  something the server would not. (iv) *A bundle for a build that is not
  finished.* Serving it for a `running` job either 404s without explaining or
  hands over a manifest pointing at an APK that is not there; like `Download`,
  it answers on `done` only and names the job's actual state otherwise.
  (v) *Someone else's submission.* `manifest_path` and the artifact are
  absolute paths owned by another user's project, so a guessed job id must not
  read another publisher's package, signer and release notes — the endpoint
  takes the same owning-project gate as `Get` and `Download`. The law: **the
  server writes the bundle, the server serves it, and both surfaces show what
  it says** — no client authors a step, a URL or a cost.

- **FM-APKa (candidate L39)** — a phone that has just built a signed APK must
  be able to take delivery of it, and that delivery must survive the auth
  protecting it. Failure modes: (i) *The artifact is reachable only from the
  browser.* The plan's Phase 3 step 6 is "offer the APK download", and
  `GET /v1/exports/{id}/download` exists and is ownership-gated — but the web
  panel is the only surface holding a button. On the phone the done branch
  prints a status line and the submission assist, so someone who just paid a
  credit is told exactly how to submit and cannot get the file off the device.
  (ii) *A link that cannot carry the token.* The endpoint takes a Bearer
  token, and the web panel says so in its own comment — "Downloads ride fetch,
  not an anchor: the endpoint needs the Bearer token and a plain link cannot
  send one". `Linking.openURL` and an anchor both hand the request to the
  browser without that header, so the user gets a 401 page, or an error body
  saved as `release.apk`. (iii) *Bytes written where nobody can open them.*
  Downloading into the app's private storage and stopping is "downloaded" in
  every sense the code can see and unreachable in every sense the user cares
  about; without a hand-off to the system share sheet the file is stranded in
  a sandbox the user never sees. (iv) *The second export fails.* expo-file-
  system's `DownloadOptions.idempotent` defaults to false — its own docs say
  "an error is thrown when the target file already exists" — so the first
  download works and every one after it throws on a name the device still
  holds. The law: **the session's
  token rides the request, the bytes land where the user can reach them, and
  the same download twice still works.**

- **FM-IDENTa (candidate L40)** — an empty email is not an identity: two
  wallets must never become one account. Failure modes: (i) *The absent
  address matches the first absent address.* Wallet sign-in calls
  `UpsertOAuthUser("solana", addr, "", "", "")` — a wallet user has no email
  "until they add one" — and step 2 answers `GetUserByEmail("")` before it
  ever looks at the address. The first email-less row stores `email=''`, so
  every later wallet's `users_oauth_identity` row links to *that* account:
  journey3's stranger wallet signed in as the owner (both rows on
  `user_id=1`), which makes every ownership probe answer as the wrong human —
  an authorization check that cannot fail, and a second user that cannot
  exist. (ii) *Guarding the lookup alone deadlocks creation.* `users.email`
  is `TEXT UNIQUE NOT NULL`, so `''` is a key with room for exactly one
  holder; skip step 2 and the second wallet's INSERT still collides, the
  code reads `UNIQUE constraint failed` as a check-then-insert race and
  recurses into the same upsert — re-colliding, forever. Uniqueness must
  therefore move off the column onto a partial index (`… ON users(email)
  WHERE email != ''`): real addresses stay unique, the empty string admits
  any number of rows. SQLite cannot drop a column-level UNIQUE in place, so
  this is a table rebuild — create `users_new`, copy, drop, rename.
  (iii) *The rebuild fights the foreign keys.* Eleven tables carry
  `REFERENCES users(id)` and the DSN opens every connection
  `_pragma=foreign_keys(ON)`: `DROP TABLE users` with children present fails
  outright (`FOREIGN KEY constraint failed`, observed on this database), and
  the pragma is **per connection** — turning it off on one of the pool's
  eight says nothing about the connection that runs the next statement. The
  rebuild therefore rides one pinned `db.Conn` for its whole life: OFF
  before the transaction, the copy, rename and index inside it,
  `foreign_key_check` before the commit, ON again before the conn goes back
  to the pool. (iv) *The fix half-lands.* The two halves ship together or
  not at all: the Go guard without the schema turns a merge into unbounded
  recursion (step 3 collides on the column's `''`, the race-retry re-enters,
  collides, re-enters); the schema without the guard leaves
  `GetUserByEmail("")` matching the first email-less row, so the merge
  survives the migration looking like success. Detection keys off the legacy
  DDL text, so it must fire exactly when the old constraint is present and
  never again — a rebuild that matches forever rewrites the table on every
  boot — and because a fresh database never matches (its `CREATE TABLE` is
  written without `UNIQUE`), the partial index must be created
  unconditionally or a new deployment ships a users table with no email
  uniqueness at all and `CreateUser`'s `ErrDuplicateEmail` (matched on the
  `UNIQUE constraint failed` substring) never fires again. The law:
  **identity is bound by `(provider, provider_id)`; an email links only when
  one was presented, and an email-less account is its own account** — the
  empty string is never a key, uniqueness covers real addresses only, and
  the schema surgery runs once, on one pinned connection, with foreign keys
  restored before anything else queries.

- **FM-NOTIFa (candidate L41)** — a push may claim only the state the turn
  is actually in; "finished" means finished. Failure modes: (i) *The budget
  pause claims the build finished.* There are two pauses and the terminal
  guard checked one. `outcome.Paused` is ask_user (done status `waiting`),
  and the guard `if res.outcome == nil || !res.outcome.Paused` was written
  for it — but `outcome.Exhausted`, the round budget (done status
  `paused`, prompt row `paused`, Resume continues it), also returns a nil
  error by design, so it fell straight through and dispatched
  `build_success` — "Build finished / verified: false" — for a turn that
  had stopped mid-work with the model still asking for rounds. Live
  evidence: journey3's build hit the 60-round budget and the owner's
  notification history gained exactly that row (id 4, 01:30:56 UTC) while
  the prompt row read `paused`. Every exhausted resume repeats it — a build
  paused four times pushes five "finished" pushes — and the client that
  uses `build_success` to drop its spinner shows *done* over a half-built
  app. (ii) *The count rots with it.* The product's own notification stage
  and the E2E assert exactly one `build_success` at the end; with the false
  rows in the history the honest number is `1 + exhausted legs + 1`, so the
  check can neither pass nor distinguish a real completion from a pause —
  and deleting rows to make it pass hides the bug instead of fixing the
  guard. (iii) *Over-blocking silences the one push that matters.* The
  mirror mistake is a guard so broad it also swallows the genuine finish —
  then the user who waited for a build is never told it is ready, and the
  failure is invisible (an absent push asserts nothing). The terminal side
  must therefore be pinned by the same test: a leg that ends `success`
  still fires exactly one `build_success`. Boundary, decided: `Stalled`
  keeps pushing (it is terminal by contract — not resumable, and the push
  carries `verified: false`), as do `BudgetStop`/`HookStop`; only the two
  resumable stops go quiet. The law: **the push reports the turn's real
  terminal state — `build_success` only when the turn is over and not
  resumable, `build_failed` only on error, nothing pushed for a stop the
  client is expected to continue — and the finished side keeps firing
  exactly once.**
- **FM-TESTa (candidate L42)** — test code may not exercise a destructive
  shared-daemon path it does not need, and "read-only" is a claim to be
  asserted, never commented. Failure modes: (i) *The reconcile test sweeps
  the live fleet.* `TestReconcileDestroysGoneContainers` documents itself
  as "read-only here: the fixture container id matches nothing, so nothing
  is created or removed" — true of the DB→docker half only.
  `Manager.Reconcile`'s second half lists every `talon-redroid-*` container
  on the daemon and force-removes the ones no active session claims; a test
  store claims nothing, so every product container is an orphan to it.
  Witnessed live: the full backend gate, run on the box while the test
  server was up, destroyed the warm pool's prepaid container — docker
  events `kill signal=9 → die exitCode=137 → destroy` at 04:49:03, 46
  seconds after "warm pool: container ready (port 15555, 1/1 warm)". Had a
  device session been live its `talon-redroid-<session>` container would
  have died the same way: a user's device killed mid-use by `go test`, on
  any machine where service and suite share one daemon (the normal dev box
  and the VPS alike). (ii) *The collateral is invisible — the test stays
  green.* Its assertions touch only its own store rows; whether the daemon
  lost containers never enters the result. CI reads `ok`, docker events go
  unread, and the comment keeps promising read-only. A destructive side
  effect with no assertion is a bug the suite can carry forever. (iii) *The
  mirror: skipping hides the test.* Guarding with `t.Skip` whenever
  containers exist would make safety a function of daemon luck (an empty
  daemon at CI time) and silently stop testing reconcile exactly where
  servers run. The sweep must move behind a seam the test does not take,
  and the read-only claim must become an assertion — a before/after
  container census — so a later change that re-arms the sweep in the test
  path turns the test RED instead of killing the fleet quietly. The law:
  **test code drives only the non-destructive half of a shared-daemon
  sweep; "touches nothing" is proven by a census assertion, never by a
  comment; the production sweep (startup and cleanup loop) keeps its full
  behavior.**
- **FM-POOLa (candidate L43)** — the sweeper must not destroy what the pool
  has prepaid, and a dropped slot must refill. Failure modes: (i) *The
  5-minute cleanup loop destroys the warm pool's container.*
  `StartCleanupLoop(s, 5*time.Minute)` runs the full `Reconcile` on the
  live daemon, and its orphan branch removes every `talon-redroid-*`
  container no active session claims — while a warm entry has no session
  by design (it boots under a negative id precisely so it belongs to
  none). Every tick therefore pays the prepaid boot back as an orphan: the
  container dies within five minutes of boot, the in-memory warm map keeps
  pointing at it, and every later claim fails retag → "stale entry dropped"
  → cold path. DV4's whole promise (a booted device waiting when a session
  opens) silently expires after the first tick, with only a claim-time log
  line as evidence. (ii) *A stale drop never refills the slot.* `Claim`
  signals `p.claim` only after a successful retag; the stale path
  `continue`s without signaling, and the worker boots only on that signal —
  so once the pool empties, for any reason (the sweep, a daemon restart, an
  OOM-killed container), it can never re-warm for the life of the process.
  Startup's "1/1 warm" decays to a permanently cold pool nothing is left
  running to notice. (iii) *The mirror: over-sparing leaks containers.*
  Sparing by name prefix or by the sign of the session id would also spare
  the PREVIOUS process's warm leftovers at startup — crash orphans holding
  1GB/1CPU forever, collected by nothing but the next process's sweep. The
  spare must key on this running process's live warm map by container
  identity: present → spare, absent → sweep as before, so the startup sweep
  still collects foreign leftovers. The law: **orphan = no active session
  AND not warm-claimed by this running process — the periodic sweep spares
  only its own prepaid entries, the startup sweep still collects foreign
  ones — and a claim that drops a stale entry signals a refill for the slot
  it emptied.**
- **FM-RELAYa (candidate L44)** — a relay that cannot reach its upstream
  owes the client an HTTP status; a silent socket close is not an answer.
  Failure modes: (i) *The startup window is a trapdoor.* The server wires
  `public :17555 → 127.0.0.1:<metro>` the moment metro's startup wait
  times out — "metro startup timeout for session 2, continuing anyway"
  (05:47:33) — while metro only began listening seven seconds later
  (05:47:40). Any client doing the documented flow (test-info → phone
  relay) inside that window hits a refused dial, and `serveConn` answers
  by returning: the socket closes with **zero bytes** — no status line,
  no body, no retry hint. (ii) *An API consumer cannot classify the
  failure.* The E2E driver's `fetch` threw `SocketError: other side
  closed` on exactly this and the run died at its relay stage
  (journey3b.mjs:446, uncaught) — an unguarded client crashes on what is
  a five-second retry, a guarded one cannot tell "upstream not ready"
  from "relay dead" from a network partition, and the only diagnosis
  lives in a server log no client can read. (iii) *The mirrors.* Answering
  200 empty would lie — there is no upstream; answering before the §2.4
  token gate would leak upstream state to unauthenticated probers; and
  closing with the client's request still unread in the kernel would RST
  the very response a fix writes. The law: **after the token gate, an
  unreachable upstream gets an HTTP 503 with `Connection: close` and
  `Retry-After`, written over drained request bytes so the close is
  graceful — the client's retry decision comes from a status line, never
  from whether the socket happened to die.**
- **FM-KEYSa (candidate L45)** — a keystore file no row owns has no
  password, so generation must move it aside; and "the file exists" may
  never wear keytool's "password was incorrect" disguise. Failure modes:
  (i) *The orphan file wedges every export with a lie.* `Keystore.Ensure`
  generates when `GetKeystore` returns no row; `-genkeypair` then OPENS
  the existing `./keystores/project-1.jks` — a keystore from an earlier
  database whose password is encrypted in a row that no longer exists —
  and answers `java.io.IOException: keystore password was incorrect`. The
  password was a fresh 32-byte random minted seconds earlier and never
  used; there is no wrong password anywhere. LIVE: export job 1 failed
  with exactly this (server log 05:58:31) while the `project_keystores`
  table was empty and `keystores/project-1.jks` dated the day before.
  (ii) *The wedge is permanent and self-hiding.* The row is written only
  AFTER genkeypair succeeds, so the same failure returns on every future
  export — the project can never ship again until a human discovers a
  file nothing in the DB mentions. A DB restore or swap (VPS backups
  restore the database, not `./keystores`) lands in this state by
  default, and the older orphan `project-24.jks` proves it has happened
  before. (iii) *The mirrors.* Overwriting the file outright would discard
  bytes that might be a published app's signing key (stores reject
  updates signed with a different key) — but keeping it is equally wrong:
  with no row the password is GONE, so the file is unreadable by
  construction. The asymmetry that hides the bug is the tell:
  row-without-file regenerates with a log line, file-without-row crashes
  with a misdiagnosis. The law: **the row is the source of truth — a
  keystore file no row owns is unreadable, so generation renames it aside
  with a loud log and writes a fresh key; the error that means "file
  exists" is never allowed to masquerade as "password incorrect".**
- **FM-SIGNa (candidate L46)** — a secret given to two readers through one
  file is consumed by the first, and a path that has never executed in
  production is not a path that works. Failure modes: (i) *Two readers,
  one stream.* `Builder.signAPK` passes ONE `passFile` path as both
  `--ks-pass file:` and `--key-pass file:`. keytool's `-storepass:file` /
  `-keypass:file` reopens the file per flag — keystore GENERATION works,
  which is why every password test so far passed — but apksigner's
  `PasswordRetriever` reads each `file:` path as a single shared stream:
  the keystore-password read consumes it and the key-password read hits
  `end of file reached`. LIVE: export job 3 built for 44 minutes
  (identity → prebuild → identity-patch → assembleRelease →
  verify-identity → sign) and died at 07:26:32 with `apksigner failed …
  Failed to read Key "project-1" password … end of file reached in
  /tmp/release-pass-*` — every export, forever, at the last step. An
  isolated repro pins it exactly: shared file without newline fails,
  shared file WITH newline fails (the stream is shared regardless of
  content), two separate files sign and verify clean. (ii) *Failure
  stacking hid it.* Job 1 died inside `Ensure` (FM-KEYSa) before any
  build ran; job 2 was killed mid-gradle by a harness restart; job 3 was
  the first run in this codebase's life to REACH `phase: sign` — a
  never-executed seam, green behind two earlier blockers, with unit
  coverage that only ever exercised keytool's reopen-per-flag reader.
  (iii) *The mirrors.* Putting the password on argv would pass and
  regress the exact leak the 0600 temp file exists to prevent (`ps`
  reads argv); sharing one file LOOKS right — the same secret twice is
  the same file — which is why review waved it through; and apksigner's
  per-path sharing is its contract, not a bug to work around with
  trimming or buffering. The law: **every password consumer gets its own
  file — a secret shared between two readers is consumed, not shared —
  and a production path that has never run end to end is untested, no
  matter how many of its helpers have tests.**
- **FM-FPRa (candidate L47)** — a fingerprint gate must compare decoded
  bytes, never their spelling. Failure modes: (i) *The equality checks
  presentation.* `sameFingerprint` normalizes with trim + space-removal +
  uppercase but does NOT strip colons — while its only two producers emit
  exactly the two spellings that differ by colons: apksigner's
  `SHA-256 digest: a6cedc…5584` (bare lowercase hex) and keytool's
  `SHA256: A6:CE:DC:…:5584` (colon-delimited). The live strings are the
  SAME 32 bytes. LIVE: export job 4 got through `sign` for the first time
  in this codebase's life (FM-SIGNa fixed) and died at `verify-signature`
  (08:02:31) with `signature check failed: APK signer "a6cedc…5584" !=
  keystore "A6:CE:…:5584"` — a correct signature accused of being wrong.
  (ii) *A correct gate makes the bug more blocking, not less.* The check
  exists to prove the artifact carries the project's own key (app stores
  reject updates signed with a different key), so it is fail-closed by
  design — and because every correct build fails the format comparison,
  no export can ever ship while the message sends the operator hunting a
  wrong key that is not wrong. This is the third never-executed seam in
  a row: job 1 died inside `Ensure`, job 3 at `sign`, job 4 here — each
  fix exposing the next untested comparison, exactly as the stacked
  failure chain predicts. (iii) *The mirrors.* Deleting the check voids
  the store guarantee (a wrong-key APK must not ship); weakening it to
  a prefix match would pass on truncated or wrong-length strings. The
  fix is canonicalization, not deletion: a certificate SHA-256 is 32
  bytes of hex — case, separators, and field labels are presentation.
  The law: **fingerprint equality strips separators and case on both
  sides and compares the full hex value — a format difference must never
  accuse a correct signature, and an empty fingerprint must never pass.**
- **FM-ROUTERa (candidate L48)** — the route tree is not a test directory.
  Failure modes: (i) *A test file in `app/` kills the app.* Expo Router
  imports every file under `app/` through `require.context` at bundle time;
  a `*.test.tsx` there is not a route, but it is imported anyway, and its
  `@testing-library/react-native` import reaches Node built-ins
  (`require("console")`, `require("util")`) that the native runtime does
  not have → `Android Bundling failed`. LIVE: starting the companion app's
  Metro died 153s in with the exact stack — `app/menu/help.test.tsx →
  "@testing-library/react-native" → require("console")` — and two more test
  files (`(tabs)/build-export.test.tsx`, `(tabs)/device.test.tsx`) waited
  behind it to break the same way. (ii) *Jest cannot see the break.* The
  same suite passes under `npm test` because Jest runs in Node, where
  `console`/`util` exist — the two toolchains disagree, so only the
  bundler catches it, and only 153 seconds into a cold start: a green test
  gate and a dead app on the same commit. (iii) *Even when it bundles it is
  still wrong.* The test becomes a navigable bogus route (`/menu/help.test`),
  route generation pollutes, and the next person copies the pattern. The
  law: **`app/` is routes only — tests live outside the route tree (beside
  their sources or under `__tests__/`), Node-only dev libraries must never
  be reachable from a route, and a guard test fails the suite if a
  `*.test.*`/`*.spec.*` file ever appears under `app/` again.**
- **FM-SSEa (candidate L49)** — the phone's stream must settle on headers
  and deliver while the turn is open.
  Failure modes: (i) *The transport can never resolve mid-turn.* `openStream`
  awaits the **global** `fetch`, and React Native's is whatwg-fetch 3.6.20,
  whose promise settles only in `xhr.onload` — the entire response body, i.e.
  the moment the turn ENDS. An SSE body stays open for the whole turn, so
  `fetch()` never resolves, the 15 s connect deadline (which exists to bound
  the wait for *headers*) fires instead, and `controller.abort()` makes the
  app cancel its OWN healthy stream. LIVE, today: both phone turns died at
  request+15 s — `POST /v1/prompts/75/resume … latency_ms=15013`,
  `POST /v1/prompts/stream … latency_ms=14850`, server-side
  `openStream: retry attempt 2/3 … context canceled` over
  `orchestrator: turn model-request-failed after 2 rounds in 15s` — while
  the server had already written 20–33 KB of events that the app could
  never read; the same signature produced the morning's USB failure too, so
  this was never about the transport *link*. (ii) *Nothing renders
  mid-turn.* Zero events reach `reduceEvent`, the screen keeps only its
  fallback `PixelGrid label="Working"`, and when the turn dies the
  conversation refetch fills the transcript in one jump — the user's exact
  report: "while it is streaming, nothing was appearing on the ui … when it
  failed, everything it has done, filled the UI." The TEMP DIAGNOSTIC stage
  markers pinned `events=0` and say to be removed once streaming works.
  (iii) *The failure is reported in two wrong voices.* The user reads the
  app's connect-timeout sentence ("The server never answered this stream")
  plus the server's `friendlyAIError` for context-canceled ("The build was
  cancelled."), with a verify chip ("checks did not pass") beside them —
  three strings, none of which says "the client aborted a healthy stream at
  15 s." The law: **the stream transport settles on response headers and
  streams the body incrementally — a fetch that waits for body-end can
  never carry an SSE turn, the connect deadline bounds headers only, and
  events must reach the reducer while the turn is still running.**
- **FM-KBDa (candidate L50)** — the composer must live above the keyboard,
  and the package that does it is native.
  Failure modes: (i) *The keyboard covers the input.* The app has no
  keyboard avoidance anywhere (zero `KeyboardAvoidingView` before this
  row), so the prompt field at the bottom of `Screen`'s flex column is
  simply under the IME — the user's report: "keyboard blocks the input
  when i am typing my prompt." (ii) *The fixing package is native: the
  binary and the JS must agree.* `react-native-keyboard-controller` (added
  2026-10-02) evaluates `KeyboardControllerNative.getConstants()` at module
  load; in a binary without the module that access throws LINKING_ERROR,
  so the package may only be imported by a dev client that carries it. The
  phone's Sep-24 binary did not (`lastUpdateTime=2026-09-24 18:07:22`,
  **0** hits for KeyboardController in its dex against **108** for
  ExpoFetchModule) — the failure this mode names — and the dev client was
  rebuilt with the package linked on 2026-10-03, which is what makes
  direct wiring lawful now: the import and the binary move together, or
  not at all. (iii) *A rebuild without a test is a hope.* Nothing checks
  that the provider is actually mounted or that the screen it wraps is the
  screen with the composer. The law: **the composer stays visible above
  the keyboard — keyboard-controller's `KeyboardProvider` mounts at the
  app root and `KeyboardStickyView` wraps the composer itself, so the field
  rides the keyboard instead of sitting under it — on a dev client rebuilt
  to carry the native module, and a test pins both wirings so the provider
  can never be quietly unwrapped.**

- **FM-THINKa (candidate L51)** — the live turn shows what the agent *does*,
  not what it *thinks*.
  Failure modes: (i) *The raw reasoning streams onto the screen.*
  `thinking.delta` accumulates into `turn.thinking` and `build.tsx` renders
  the entire string as it grows — the user's report: streaming "in a very
  bad and wrong way… you are not supposed to show the user the full thinking
  text of when the AI is thinking." A build emits hundreds of deltas; the
  reasoning wall buries the tool/decision/command rows that *are* the
  progress and scrolls them off screen. (ii) *Hiding thinking wholesale is
  the same failure inverted.* Every builder's answer is a disclosure, not a
  cut: CopilotKit's "Thinking… / Thought for X seconds", Claude Code's
  collapsed stub, DeepSeek's `ReasoningRow` — collapsed by default, a compact
  duration label, one tap to expand. (iii) *A label that cannot tell running
  from finished lies.* Without a reducer-carried signal the row guesses from
  unrelated state — "Thinking…" frozen over a 60 s tool call, or "Thought
  for…" stamped mid-burst. The law: **decisions, tool calls, skill calls and
  executed commands are the visible layer of a turn; reasoning arrives as one
  collapsed row — `Thinking… <t>` while it streams, `Thought for <t>` once it
  settles — never the raw text by default, one tap away when the user asks,
  and a test pins the collapsed default so the wall can never come back.**

- **FM-TLKa (candidate L52)** — the live turn is one chronological timeline,
  not a stack of fixed slots.
  Failure modes: (i) *Fixed slots destroy chronology.* The reducer buckets
  thinking (one string), tool calls (one array) and narration (one string)
  separately and `build.tsx` renders them in fixed positions — one
  `ThinkingRow` pinned above the whole trace, the plan card pinned below the
  tail. Reasoning that arrives after a tool call still renders at the top;
  the plan reads as "the decisions are at the bottom and everything is one
  big decision" (the user's report), and the interleave the user asked for —
  *thinking → "now let me look at how x is structured" → read file → thinking*
  — is structurally impossible. (ii) *Paired frames duplicate every tool row.*
  The backend emits `running` then `completed` under the same id
  (`looplisteners.go`); the reducer appends both and never keeps the id — a
  phantom spinner per tool that never settles. This is also why stopping the
  build "adjusted everything": the history restore collapsed the duplicates
  into one row each. (iii) *A feed that cannot follow itself.* No auto-follow
  exists — new rows land below the fold while the screen holds still.
  (iv) *The restore drops what it stored.* Thinking blocks are persisted
  (271 rows in the live DB carry `type:"thinking"`) yet the restored
  transcript filters only `text`/`tool_use` and renders narration → plan →
  tools in fixed order — the same chronology loss after every reload, and
  no thoughts at all (the user's stop-observation).
  The law: **every event renders at its arrival position — one ordered
  timeline. Thinking opens a collapsed segment where it started (a one-line
  live preview while it streams, tap to expand) and settles in place; tool
  rows upsert by id, running → settled in place, never twice; narration
  streams where it lands; the plan card sits at the top of its turn with the
  checklist ticking beneath it; the restored transcript walks stored blocks
  in stored order, thinking included; and the feed auto-follows the newest
  row with a `↓ live` pill to rejoin after the user scrolls away.**
- **FM-RSUMa (candidate L53)** — a resumed turn streams where the user can
  see it.
  Failure modes: (i) *Resume is invisible by construction.* `liveVisible`
  retires the tail the instant `historyHasTurn` matches — and a resumed
  prompt id is already in history (the interrupted turn is persisted), so the
  first `phase` frame (which carries `prompt_id`) hides the tail and every
  event after it reduces into an unmounted screen. Server-side proof from
  the user's own session: `POST /v1/prompts/75/resume bytes=69375` — 69 KB
  of real thinking/tool frames streamed while the phone showed nothing;
  the report: "when I press resume it doesn't stream". The same retirement
  fires on any mid-stream history refetch (tab focus), killing a live build
  the same way. (ii) *Naively un-hiding duplicates rows.* The retirement also
  guards the restored transcript against the live tail — removing it without
  excluding the live prompt renders the same rounds from both sides.
  The law: **the tail never retires while the turn is streaming or waiting;
  while it shows, the restored transcript excludes that prompt's rows; both
  release together at done/error, when the refetch absorbs the whole turn —
  and resume/answer seed the tail from the prompt's stored blocks so the
  continuation picks up exactly where it stopped instead of starting blank.**
- **FM-VRFYa (candidate L54)** — the footer claims only what ran.
  Failure modes: (i) *A default verdict reads as an accusation.* Every
  emitter sends `verified:false` whenever no check ran — the no-changes,
  paused and cancelled paths leave the zero value (`orchestrator.go`'s
  `done-no-changes` emits success with `Verified` unset, the `turn_end`
  marker marshals the same zeros) — and the row renders the red chip on any
  false, so "checks did not pass · 1 round" comes out on *every* turn, a
  claim the backend never made (the user's report). The wire contract already
  says absent keys claim nothing; the emitters just never omit the key.
  (ii) *Suppressing everywhere hides the real failure* — `verified:false`
  with files written and a policy is exactly when the red chip is honest.
  The law: **absence is the no-claim state. The backend omits the
  verdict keys (`verified`/`verified_best_effort`) unless checks could
  have run — files touched, a verify policy, or recorded skips — and the
  turn was not paused or round-exhausted out of its checks; both clients
  render the verdict words only from a present key. Files and rounds are
  descriptive, not verdicts: a file count may ride alone (it says what
  changed), a round count rides with a claim or from a legacy
  payload-less marker (the only marker predating the payload, F2) — a
  payload that exists but carries no verdict claims nothing at all, and
  a cancelled or failed turn keeps its rounds in its own status
  sentence, never the footer.**

- **FM-APKi (candidate L55)** — the dev client installs from inside the app.
  Failure modes: (i) *The button hands the URL to the browser.* The device
  tab's "Download the dev client (APK)" is `Linking.openURL(dev_client_url)`
  — the system browser opens an owner-authenticated endpoint carrying none
  of the session the app already holds, so the phone gets bounced to the web
  login ("the web is asking me to authenticate again"), or to a `localhost`
  URL that resolves to the phone itself. (ii) *The token never rides the
  URL* — a query-string token would land in browser history and server logs;
  it belongs in the `Authorization` header the session already holds.
  (iii) *A `file://` install intent dies on modern Android*
  (`FileUriExposedException`, API 24+) — a silent failure is the same as no
  install, so intent refusal falls back to the system share sheet, which the
  repo already ships (L39's `expo-sharing` seam) and which still never
  touches a browser.
  The law: **download in-app over the session token (the exact
  `File.downloadFileAsync` seam L39 proved), fire Android's install intent
  (`ACTION_VIEW` + `application/vnd.android.package-archive`) so the OS asks
  its one-time "install unknown apps" question itself, and on refusal hand
  the file to the share sheet — no browser, no second login, no token
  outside the header.**

- **FM-ANSQa (candidate L56)** — every waiting turn presents the one surface
  that answers it. Failure modes: (i) *A plan-mode question waits with no
  card anywhere.* The plan path classifies a question-shaped reply by
  `planAsksQuestion` and marks the turn `waiting` — but it never emits a
  `question` frame (only `done{status:"waiting"}`), and it stores the
  exchange as plain text (`persistPlanExchange` with `planJSON(nil)` → a
  text-only reply), so history carries no `ask_user` block either. The
  composer gate (`waitingId != null`) blocks every send with "answer the
  question in the transcript first" while both card paths — live
  (`phase === 'waiting' && turn.question`) and restored (`ask_user` block +
  `waitingPromptId`) — miss: the question sits on screen and nowhere can
  answer it (2026-10-03, prompt 84: `mode=plan status=waiting`, blocks =
  `user|text` + `assistant|text`, zero card paths fired; the user's exact
  report: "i cant find anywhere to answer on the ui"). (ii) *A question
  renders as a plan.* The `plan` frame carries `message` with `plan` absent;
  the reducer normalized the absence into an empty-but-truthy `PlanData`,
  so an empty PlanCard with "Approve & build" sat over the question —
  approve would ship an empty PLAN.md into a build (the web guards
  `d.plan ? … : null`). (iii) *A stale ask_user re-opens.* The restored
  card fired on any turn's `ask_user` while any prompt waited — an old,
  long-answered question re-opened as a live answer box keyed to the
  newest waiting prompt (the web pins `isLastTurn`).
  The law: **`waiting` is a render contract — whenever the server says the
  latest turn waits, exactly one answer surface keyed to that prompt is on
  screen: the ask_user card when the block exists, a card over the turn's
  own stored question when it does not; and nothing that is not a plan
  renders as one.**

- **FM-C1e (candidate L57)** — the plan file gate judges paths, not
  punctuation. Failure mode: *A legitimate Expo Router file is rejected as
  a glob.* `AppPlan.Validate`'s glob law rejects `*`, `?` and `[` alike —
  but `app/address/[address].tsx` is an exact filename Expo Router creates
  for every dynamic route, so a plan that declared its screens honestly
  was failed with "must be an exact path — no globs" *after* the user had
  answered its question (2026-10-03, prompt 84: the answer re-ran planning
  for 45 s, the validator killed it, nothing saved — the user then built
  with no plan and hit the unrelated 401s straight after). The law:
  **`*` and `?` are the glob characters a workspace path may never carry;
  brackets are ordinary filename characters — a path stays exact however
  it is spelled.**

- **FM-RAWa (candidate L58)** — a friendly error must not be the only
  record of a failure. Failure mode: *The server forgets what the gateway
  actually said.* `friendlyAIError` rewrites the raw error for the user,
  and every durable record — SSE `error` event, `prompts.response`,
  `turn_end` — carries only the rewrite; stdout logs `model-request-failed`
  with no error attached. When agentrouter began returning 401
  `unauthorized client detected` mid-turn, the server's whole record read
  "check the server's AI_API_KEY" — a wrong instruction about a key probes
  proved valid — and diagnosing it took session-event spelunking plus live
  probe requests instead of one grep (2026-10-03, prompts 84/85). The law:
  **wherever raw becomes friendly, the raw text lands on the server log
  first — the rewrite may soften the message, never destroy its source.**

- **FM-AUTHa (candidate L59)** — an identity rejection gets exactly one
  backoff retry, then the same terminal failure. Failure mode: *One
  gateway-side 401 kills a healthy turn.* All four gateway call sites
  treat 401/403 as "will not succeed on retry" and return on the first
  refusal — but a gateway that authenticates on client identity (FM-UAa)
  can flap: agentrouter 401ed round 15 of a turn whose rounds 1–14 had
  succeeded, and the immediate Continue died the same way 4 s in — two
  turns lost to a key that was never invalid (2026-10-03, prompt 85). The
  law: **`openStream`, `doWithRetry`, `chatAnthropic` and `chatOpenAI`
  each give a 401/403 one retry after `authRetryDelay` (3 s default,
  test-shrunk), reported as `llm/retry` class `auth_rejected` — a dead key
  fails identically twice and dies with at most one extra request; a flap
  is absorbed.**

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
| L23 | **429 waits the rate window; 502 keeps its ladder (FM-429a)** — four gateway call sites, one law: a 429 waits the gateway's `Retry-After` when sent, else `rateLimitWindow` (60 s default, test-shrunk), quota exhaustion is never a wait, every other transient keeps its own ladder exactly (3 s/6 s chat paths, 2 s/4 s/8 s `doWithRetry`), and every lengthened wait is ctx-cancellable. `openStream` (the path plan/build turns actually take) tracks `lastRateLimited`/`lastRetryAfter` per round so a 429 waits the window while 502/503/504 wait `attempt*3s`; `chatAnthropic`/`chatOpenAI` own the 429 wait in the status branch and suppress that round's loop-top ladder with `waitApplied` (the loop top was already where the generic sleep lived — stacking both would have made a 429 window+3 s and a 502 6 s instead of 3 s); `chatOpenAI` gains the `isQuotaExhausted` guard `chatAnthropic` already carried, shipped with the fix because lengthening the wait would otherwise turn an instant quota failure into two full windows; `doWithRetry` (every vision call + the buffered agentic path) swaps its no-header 2 s/4 s/8 s ladder for the window, reports the wait so a minute-long stall is never silent, and drops its `secs<=60` clamp that ignored longer header values and fell back to the futile ladder | fails-first, red states first: `TestOpenStream429WaitsOutRateWindow` (red: `c.rateLimitWindow undefined` — compile failure, the 60 s knob did not exist), `TestOpenStreamHonorsRetryAfterHeader`, `TestChatOpenAI429ReportsTheWaitItTakes` (red: retry hook fired **2×** for one stall — status branch and loop top both reported), `TestChatOpenAIQuotaExhaustionFailsFast` (red: 3 calls, generic "Rate limit exceeded" instead of `quotaError`), `TestDoWithRetry429WaitsRateWindow` (red: **2.68 s** wall on a 25 ms window + zero waits reported); keep-tests `TestStreamKeepsFastLadderFor502` + `TestChatOpenAI502KeepsLoopLadderNotWindow` (a 502 must never report the window: 3 s ladder, one wait); Run-5 evidence: `Cluster RPM rate limit exceeded` bursts died in ~15 s (3 s+6 s) against a per-minute window — Run-6 to show the turn surviving one; `chatWithToolsOpenAI` log format and the scorecard-parsed `request-ledger` lines untouched (`openStream` retry line amended, safe); `Retry-After` is honoured as sent — the chat paths already slept it uncapped, so no new hang class, and the wait is cancellable | ⬤ |
| L24 | **Transport identity is configuration, and it is attached where requests are built (FM-UAa)** — `newRequest(ctx, url, body)` becomes the only way this package builds a gateway request: all four request paths (openStream, doWithRetry, chatAnthropic, chatOpenAI) funnel through it, and it sets `User-Agent` from the new `userAgent` field (empty ⇒ `defaultUserAgent` `Talon/1.0`). `AI_USER_AGENT` populates the field in `New()`; `clone()` carries it so `ClientForProvider` clients keep it. The hardcoded `Talon/1.0` in `chatWithToolsAnthropic`'s `setHeaders` is deleted rather than left to overwrite the constructor's value — one place sets the header, not four | fails-first `TestUserAgentEnvKnobConfiguresTheClient` + `TestEveryRequestPathSendsTheConfiguredUserAgent` (red: **compile failure** — `c.ua undefined`, `unknown field userAgent`, the same shape L17/L23 shipped their red as; each of the four paths then asserted green individually, so a partial answer — the streaming path left on Go's `Go-http-client/1.1` — cannot pass); keep-test `TestUserAgentDefaultsToTalon` (no knob ⇒ the same `Talon/1.0` gateways see today); external evidence: header sweep on agentrouter.org — only `claude-cli/2.0.0 (external, cli)` returns 200, `Talon/1.0`, the curl default, `claude-cli/1.0.0`, `claude-code/2.0.0` and `anthropic-sdk` all 401 `unauthorized client detected`; `tools/research.go`'s UA is the search engine, a different contract, untouched | ⬤ |
| L25 | **One endpoint builder for the Messages API (FM-URLa)** — `c.messagesURL()` is the single place the anthropic endpoint is built, called by all four sites that previously disagreed: `chatAnthropic`, `chatWithToolsAnthropic` (were `/v1/messages`), `streamAnthropic`, and the vision call (were `/messages`). It trims a trailing `/` and appends `/messages` when the base already ends in `/v1`, else `/v1/messages`, so both spellings of the root resolve to the mounted API — the openai side's `/chat/completions` sites are untouched, keeping the diff inside `protocol == anthropic` code paths | fails-first `TestAnthropicMessagePathsAgree` (red: **each base spelling fails half the paths** — with a base ending `/v1`, `Chat` and `ChatWithTools` request `/v1/v1/messages`; with a bare root, `ChatStream` and `VisionCheck` request `/messages`; the test drives all four real call sites against one recording server and asserts every path, so fixing two sites and leaving two cannot pass); keep evidence: agentrouter.org probe — `/v1/messages` 200 JSON, `/v1/v1/messages` 404 `Invalid URL`, `/messages` 200 **HTML** (the silent-failure shape FM-URLa names) | ⬤ |
| L26 | **A truncated plan is a failed plan, never a success (FM-PLANa)** — three changes on the plan stream's truth path: (1) `streamAnthropic` reads `message_delta`'s `stop_reason` and a `max_tokens` end emits an error event (the existing `sawError` arm then marks the turn `failed`); (2) the plan handler's prose fallback refuses envelopes — output that opens with `{` and does not unmarshal is a terminal `failed` + error frame, while prose that does not open with `{` keeps today's message path unchanged; (3) `maxOutputTokens` 16384 → 32768, the ceiling below which Run-7's plan (≈4,300 tok of JSON plus a model that emitted `out=10628` on a single build round) was cut mid-array | fails-first `TestPlanStreamTruncatedByMaxTokensIsAFailure` (red: SSE fixture ends `stop_reason: max_tokens` mid-JSON → turn reports `done success`, prompt `completed`, plan_json null — Run-7's exact state, project 24) + `TestBrokenPlanEnvelopeIsNotReportedAsSuccess` (red: handler's prose arm swallows it) + keep-test `TestPlanStreamProseAnswerStillSucceeds` (a question-only reply must keep returning success/waiting — the new rule must not eat the message path) | ⬤ |
| L27 | **The estimate line is emitted by call, not by protocol (FM-TELf)** — one `logEstInput` helper, in the exact `est_input_tok=%d (system=%d hist_msgs=%d tools=%d body_bytes=%d)` format the scorecard parses, called by `chatWithToolsOpenAI` (moved, unchanged output) and by `chatWithToolsAnthropic` (new) right after the body is marshalled — so S9/S10 are measurable on every provider | fails-first `TestAnthropicPathEmitsTheEstimateLineTheScorecardParses` (red: run the anthropic round against a stub and capture `log` output — zero `est_input_tok=` lines, the Run-7 state) + keep-test `TestOpenAIEstimateLineFormatUnchanged` (red: format drift would break the scorecard parser silently) | ⬤ |
| L28 | **The agentic round streams on every protocol (FM-ANTa)** — `ChatWithTools` passes the `StreamSink` to `chatWithToolsAnthropic` instead of dropping it at the dispatch boundary, and a non-nil sink switches that round to `openStream` + one shared `readAnthropicStream` reader: text deltas forward as `sink(d, false)`, thinking as `sink(d, true)` (the openai branch's exact contract), while the reader accumulates the same fields the buffered path returns — text/thinking/tool blocks, `input_json_delta` concatenated into `ToolUseBlock.Input`, stop_reason, usage (input+cache from `message_start`, output from `message_delta`; agentrouter confirmed to send both). `streamAnthropic` is refactored onto the same reader with an events adapter, so the parse exists once; its observable behaviour is preserved (error event → error frame, `max_tokens` → error frame, close-without-`message_stop` → clean `done`). The agentic side is stricter where it must be: a stream that ends with no stop_reason is an error (the orchestrator's client ladder retries) rather than a silently toolless round, and `max_tokens` still lands in `StopReason` so Run-8's round-3 pattern — cut at the ceiling, 6 usable tool calls executed — is unchanged. Nil sink (planloop's five callers) keeps the buffered path byte-identical, and both branches in the function share one header closure | fails-first `TestAnthropicRoundStreamsToTheSinkWhileTheRoundRuns` (red on two independent places: the request body carries no `"stream":true` — the Run-6/7/8 state; the sink is called **zero** times, Run-8's dark wire — plus per-delta count and first-call-before-stream-end assertions that keep a one-shot end-of-round call from ever passing as streaming) + contract keep-test asserting the streamed and buffered results are deep-equal from the same server (latency may change, the result contract may not) + `TestAnthropicMessagePathsAgree` extended to a 5th, sunk call so both base spellings cover the new branch; the contract change also reached 16 scripted fake providers across api/composer/orchestrator that ignored the stream flag — a JSON answer parses as an *empty* stream, turning a scripted tool_use round into a scriptless one that failed with the client's no-stop_reason error — so `internal/aitest` now reframes each scripted response as the SSE carrying it, when and only when the request asked to stream (a fake that answers a stream request with JSON hides the failure inside the client, exactly the shape FM-URLa names); scope line: openai's M2 restart loop is deliberately out of scope — a dropped anthropic stream surfaces as an error to the existing ladder, not a new retry machine | ⬤ |
| L29 | **Compaction announces itself before it waits (FM-CMPa)** — the round loop logs the compaction (round, turns, token estimate vs budget) and emits `compact` **`running`** before `CompactWithSummary`/`Compact` runs; the existing `completed` frame at exit pairs with it, so the scorecard still counts exactly one `compact` call in the tool mix while S12's gap clock resets at wait *start*. The emit sits above the `o.ai` branch, so the static-fallback path announces too — both are waits from the wire's point of view only in length, and neither may start silently | fails-first `TestCompactionCheckpointIsOnTheWireBeforeTheModelCall` (red: the fake model's *summarizer* handler waits for a `compact` `running` frame the moment its request arrives — today the first compact frame is emitted only after the call returns, so the wait times out with the 133k-token model call already invisible; a fixture guard asserts compaction was actually reached, so "never compacted" cannot pass as "compacted visibly"); evidence: Run-4 round 57 `prep=2m15.649s` with no log line until it finished (Appendix D, found-live) | ⬤ |
| L22 | **Gates ride wall time, not round ticks (FM-D1c)** — `fireGateWarnings(sess, mode)` becomes the single implementation of both due-checks (L3 route + L17 first-write: due → latch → one warning event, text byte-identical to today's), called from the round-loop sites *and* from a turn-scoped ticker goroutine started before the round loop and stopped when it returns. The ticker is `gateTickInterval` — 15 s, the build heartbeat's cadence, a var so tests compress it (the heartbeat's own pattern). The round-loop call stays: at a boundary it claims the event with zero delay; the ticker covers every window the round loop cannot see (a generation spanning the threshold — Run-4's 7m56s — long tool runs, verification, compaction). Mode-gating and latches untouched: plan turns stay silent, one event per gate ever, FM-D1b's one-voice card unchanged | fails-first `TestFirstWriteWarnFiresDuringASpanningGeneration` + `TestRouteWarnFiresDuringASpanningGeneration` (red: compile — `undefined: gateTickInterval`, the same shape L17/L23/L24 shipped theirs as; the behavior it locks in once it compiles: threshold set to cross ~2 s into a single model call that blocks until the event lands or a 5 s cap — today the round loop is inside `ChatWithTools` the whole time, no tick exists, and the turn then answers `end_turn` with no round 2, so the event never fires at all; a fixture guard fatals if the warning landed *before* the generation started, so a round-1 claim cannot pass as a spanning one) + keep-tests `TestFirstWriteLadder`/`TestRouteScaffoldLadder`/`TestGateNoteOneVoice` pinned green (session semantics untouched); Run-4 evidence: 7m56s generation, ticks +11s/+16s/+25s → +8m22s, `first_write` events 0 while S1 failed | ⬤ |
| L30 | **One tools-layer law, both wires (FM-L30a; closes P2 #11)** — (a) descriptions travel whole: the OpenAI path's unconditional 160-char sentence compression (P1 `7b2f5a4`'s uncured half — sized for a Groq context limit the same file declares dead) is deleted, so `run_shell`'s approval flow and `ask_user`'s secret rule reach the model on every protocol; (b) the 100K size valve (`toolStripInputTokens` → `filterToCoreTools`, contract-guarded by `coreToolSet` + the existing `TestPromptPromisedToolsSurviveCoreFilter`) applies on the anthropic wire too — today it exists only in `chatWithToolsOpenAI`, so the wire the scorecard runs has no pathological-body valve at all; both halves share one threshold, one core set, one <8-element keep-original guard. The reduce direction itself is dispositioned in FM-L30a (i)–(iii): availability gating measured complete, relevance-hiding = Run-1 amputation, prose-thinning unverifiable — no other cut is lawful | fails-first `TestOpenAIToolDescriptionsTravelWhole` (red observed: contract cut to 285 of 375 bytes — P1's ≥160-sentence compression live on every openai request; green: byte-identical to `ToolDefinition.Description`) + `TestAnthropicStripValveFiresOnOversizedBodies` (red observed: est>100K with 14 tools sends all 14 — no valve exists on this wire; green: core-only, non-core stripped) + pins `TestAnthropicToolsUnchangedOnHonestBodies` (est far below 100K → tools wire byte-identical to today: the recipe cannot move) and `TestAnthropicValveKeepsAllWhenCoreWouldGoUnder8` (the shared <8 keep-original guard mirrored); Run-8: tools=22,216 B/round = 4th-largest layer, 14 of 41 called; Run-1: 8 promised tools stripped at 6500 (S9's origin); golden suite re-recorded via `TALONSTUDIO_UPDATE_GOLDEN=1` — digest-only diff, 0 non-sha256 lines changed | ⬤ |
| L31 | **The checkpoint speaks the guide shape; the ceiling matches the window (FM-CMPb)** — (a) `compactionInstruction`'s section list becomes `Objective / Requirements / Decisions Taken / Current Work State (Completed, Active, Blocked, Next Move) / Relevant Files / Important Context` — ten lines, markdown headings, empty sections "(none)", Requirements pinned verbatim, Decisions carrying fixes tried-and-abandoned so they are not retried, the "consolidate any prior checkpoint" rule extended to older-format ones; (b) `looksLikeCheckpoint` accepts the guide shape *and* the legacy anchor (a legacy checkpoint from kept history beats the static fallback; the gate rejects ramblers, not fashion); (c) `MaxContextTokens` 120_000 → **200_000** — conversation ceiling sized against the verified 1M windows of both run models (claude-opus-5, deepseek-v4-flash), Atria-Dawn-Preview's unpublished window recorded as the watch item in the const's own comment | fails-first `TestCheckpointCarriesTheGuideShape` (red: the instruction lists the legacy eight sections — `Objective` absent from the section list — and `looksLikeCheckpoint` rejects a guide-shaped reply) + `TestMaxContextTokensIs200K` (red: **120_000**) + `TestConversationUnderTheCeilingDoesNotCompact` (red: a ≈167K-token conversation — above the old 120K, below the new ceiling — emits the `compact` frame and reaches the summarizer today); keep-fixture `TestCompactionCheckpointIsOnTheWireBeforeTheModelCall` re-derived const-tracking (`MaxContextTokens*3 + 60_000` chars ≈ 220K tokens) with `checkpointReply` moved to the guide shape, its "never compacted cannot pass" guard intact; `TestLooksLikeCheckpoint`'s legacy `good` untouched — the acceptance it pins must survive; docs `DESIGN_AGENT_CONTEXT.md` / `PLAN_HARNESS.md` 120k numbers move with the const; no golden touch (fixtures never approach the ceiling) | ⬤ |
| L32 | **The phone surface is reachable before the phone is paired; the Device tab's truth stays on the server (FM-DEVa/b/c)** — (a) `TestInfoHandler.Info` derives `DevClientURL` through one helper (devClient configured + `DevClientAPKPath` on disk) *before* any early return, so the no-session and no-relay answers carry it too; (b) web `TestOnPhone` drops the `available` gate from `apkUrl` and renders the shared APK block in the unavailable branch as well — with (a) landed, an absent URL means exactly "not built" and the fallback copy stays true; (c) mobile `useCreateSession` invalidates `['test-info', project_id]` + `['live-session', project_id]` and no longer writes the phantom `['session', project_id, 'id']` key, `useDestroySession` invalidates the `['test-info']`/`['live-session']` prefixes (its vars are a session id — prefix is the only honest scope), and `useTestInfo` polls at the session queries' 5 s rhythm so sessions started from another client converge; (d) `device.tsx` replaces its hand-rolled SSE effect with `useMetro` — one subscription implementation, the race-free one | fails-first `TestTestInfoAdvertisesTheAPKBuiltOnDiskWithoutSession` (red: `DevClientURL == ""` with no session — Info's early return predates the field) + coverage pins `TestTestInfoOmitsTheAPKWhenNotBuilt`, `TestDevClientAPKServesTheBuild`, `TestDevClientAPKNotBuiltIs404`, `TestDevClientAPKRequiresOwnership`, `TestDevClientAPKRealBuildRangeProbe` (skip-if-absent; real 148 MB build served over Range, `PK` magic), `TestMetroSSEReportsNoSessionHonestly`, `TestMetroSSERejectsNonOwner`, `TestMetroSSESaysSoWhenTheBundlerNeverStarted` + vitest `TestOnPhone` first-time-phone case (red: available:false + dev_client_url → link absent today) + jest `device.test.tsx` (red: no test-info refetch after start/destroy; red: unmount-before-resolve leaves the metro socket open) | ⬤ |
| L33 | **One package per app — and every surface that names one names the package it actually gets (FM-DEVd/e)** — (a) the prebuilt dev client claims `com.talon.devclient` (`backend/template/app.json`) instead of the studio app's `com.talon.app`, so one phone can hold both; (b) `devclient.DevClientPackage` is the named, exported single source `IsInstalledOnDevice` probes — pinned to the template's `expo.android.package` by test instead of living as a second, unchecked literal; (c) `identity.EnsureUnique` refuses reserved packages (`com.talon.app`, `com.talon.devclient`) and reserved schemes (`talon`, `talonapp`) before it ever reaches the store's own checkers, and `renderAppJSON` always writes the project's package — creating the `android` block when the template has none — so a rendered project can never inherit a studio package; (d) the `device-session` skill's Maestro `appId` becomes the project's own `expo.android.package` (L21's `com.talon.app` marker retargeted with it); (e) `TestOnPhone`'s pairing instruction names the dev client's own Scan QR Code entry instead of a camera tap no registered scheme can serve | fails-first `TestDevClientPackageIsItsOwnNotTheStudioApps` (red: `backend/template/app.json` and `talonmobileapp/app.json` both read `com.talon.app`) + `TestInstalledCheckProbesTheTemplateBuild` (red: `DevClientPackage` = `com.talon.devclient`, template = `com.talon.app` — the drift that made every session start reinstall) + `TestShippedDevClientAPKDeclaresItsOwnPackage` (red: the real APK's AXML manifest carries `com.talon.app` 8× as UTF-16LE and `com.talon.devclient` 0×; self-validating — a broken extractor would show 0 for both; skip-if-absent. **Stays red until `build-dev-client.sh` is re-run**, which is the point: the artifact, not just the source, must declare the new package) + `TestProjectIdentityRefusesStudioIdentity` (red: `Generate("App")` → `com.talon.app`, `Generate("Talon")` → scheme `talon` — neither is refused today) + `TestRenderedAppJSONCarriesTheProjectPackageNotTheTemplates` (red: a template with no `expo.android` block leaves the project's package unwritten) + knowledge `TestDeviceSessionSkillCarriesProtocol` retargeted (red: skill carries no `expo.android.package`) + vitest `TestOnPhone` pairing-instruction case (red: copy promises a phone-camera tap). **GREEN (2026-10-04):** the artifact red was root-caused and cleared — a stale gitignored `backend/template/android/` (generated Sep-12, still `applicationId 'com.talon.app'`, predating the Sep-29 package rename in `7f2c208`) was what `npx expo run:android` rebuilt, because Expo reuses an existing native dir instead of regenerating it; that is also why a template build *replaced the studio app on-device* (one applicationId = one app slot, same debug keystore = accepted update). After `rm -rf backend/template/android` + `build-dev-client.sh`: `aapt2 dump badging` on the shipped APK reads `name='com.talon.devclient'`, `go test ./internal/devclient/` 3/3 ok, refreshed `native-modules.json` (`builtAt` + newly-linked `expo-font`) committed; **live (user, 2026-10-04): both apps installed side by side on one device** | ⬤ |
| L34 | **One phone hands itself the project (FM-DEVf)** — (a) `TestInfoHandler.Info` gains an additive `dev_launcher_link`: `<scheme>://expo-development-client/?url=` + URL-encoded `deep_link`, where `<scheme>` is `devclient.DevClientScheme`, pinned to `backend/template/app.json`'s `expo.scheme` by test so the dev client's identity keeps one authority beside `DevClientPackage`; `deep_link` keeps its `exp://` shape byte-for-byte for the QR and the in-app scanner; (b) mobile `device.tsx`'s "Open on this phone" opens `dev_launcher_link ?? deep_link` instead of the raw `exp://`, so the VIEW intent lands on `MainActivity`'s `talonapp` filter, passes `isDevLauncherUrl` + `hasUrlQueryParam`, and `DevLauncherURLHelper.kt:24` rewrites the inner `exp://` → `http://` for the fetch; (c) `help.tsx` gains the "test on this phone" entry the FAQ never had — the existing answer covers only a second phone; (d) `devclient.ManifestDeclares` lets any package assert a link against the artifact actually shipped instead of against a source file | fails-first `TestInfoHandsThePhoneALinkTheShippedClientCanResolve` (red: the wire advertises only `deep_link`, scheme `exp`, and the shipped APK's AXML declares no `exp` scheme — 0 matches; read off the handler's JSON *and* the artifact, not off source) + `TestDevLauncherLinkWrapsTheDeepLinkForTheLauncher` (red: no `dev_launcher_link` key on the wire at all) + jest `device.test.tsx` "Open on this phone" case (red: the button still passes `deep_link`) + jest `help.test.tsx` case (red: the FAQ has no answer for testing on this phone); `internal/devclient`'s manifest helper moves onto `ManifestDeclares` with its L33 red unchanged (APK still declares `com.talon.app`) | ⬤ |
| L35 | **The build is the only thing that costs (FM-CRDa)** — (a) new `export_credits` ledger (`store/export_credits.go`, created in `migrate()` beside `export_jobs`): one row per grant holding `credits` / `remaining` / `source` / `tx_signature`, with `tx_signature` **UNIQUE** so a replayed payment cannot grant twice — the idempotency law `billing_events.provider_event_id` already sets; `GrantCredits` returns false for an already-seen signature instead of raising an error, `SpareCredits` is `SUM(remaining)` over the ledger (the balance *is* the ledger — no second copy), and `ConsumeCredit` is a single conditional `UPDATE … WHERE remaining >= 1` over the oldest funded row, so a balance of 1 admits exactly one of two concurrent exports; (b) `gateExport` becomes ownership-only and its doc says so — `ListAssets` / `UploadAsset` are store-checklist preparation and stay free for every owner, which is also what lets the paywall sheet render at a balance of zero (the pre-existing `TestListAssetsEndpoint` keeps passing untouched — its "free user" is a *non-owner*, so it has always asserted ownership at 404, never tier); (c) `Create` gains the two-stage paywall: `exportAllowed(tier)` passes untouched (paid tiers are never charged and never get a row), otherwise `Create` runs the paywall in two stages: `mayExport` reads `SpareCredits` and refuses a zero balance **without spending**, before any verification or approval work (so a build that policy turns down costs nobody a credit), and `takeExportCredit` performs the single conditional `ConsumeCredit` immediately before `CreateExportJob` — the early read is never a reservation, so a balance of 1 still admits exactly one of two exports that reach it together. Refusal is **402 Payment Required** with `code`, `spare_credits`, `credits_per_export`, `skr_amount_per_pack` and `credits_per_pack` read from `SKR_PRICE_AMOUNT` / `SKR_PRICE_CREDITS` (default 100 / 5) so a client renders the offer from the refusal itself | fails-first `TestExportWithoutSpareCreditsIsPaymentRequired` (red: a free owner at a balance of 0 is refused **403** with a Pro-upsell sentence — no 402, no parseable reason) + `TestStoreChecklistIsNotPaywalled` (red: `ListAssets` refuses the owner at 404/403 while the balance is 0 — the paywall blocks the very screen that must sell it) → round B `TestFreeOwnerWithSpareCreditsStartsTheExportAndSpendsOne` (red: `GrantCredits` undefined, so a funded owner cannot be shown to reach 202 and drop 1→0) + `TestConsumeCreditAdmitsExactlyOneOfConcurrentExports` (red: `ConsumeCredit` undefined — a balance of 1 cannot be shown to admit one of 8 racers) + `TestPaidTierIsNeverCharged` (coverage pin: Pro reaches 202 at a balance of 0 with no ledger row, so a future refactor that charges everyone trips it) | ⬤ |
| L36 | **A signature is not a payment (FM-SKRa)** — (a) new `internal/skr` verifier: `Config{RPCURL, Mint, Treasury, Price, Credits}` comes from `SKR_RPC_URL` / `SKR_MINT` / `SKR_TREASURY` / `SKR_PRICE_AMOUNT` / `SKR_PRICE_CREDITS` at startup and is unreachable from any request; `Verify` shape-checks the base58 signature **before** the RPC call, issues its own `getTransaction` (`encoding: jsonParsed`, `maxSupportedTransactionVersion: 0`) with a timeout, and derives the verdict entirely from `meta` — `meta.err == nil`, a `pre/postTokenBalances` pair whose `mint` equals the configured mint **and** whose `owner` equals the configured treasury, whose raw increase equals exactly `Price * 10^decimals` where `decimals` is the chain's own `uiTokenAmount.decimals` on those same balances; (b) `internal/api/skr.go` `SkrHandler` mounts `POST /v1/skr/quote` (returns `skr_amount`, `credits`, `mint`, `treasury`, `spare_credits` — the price is server-stated, never client-suggested) and `POST /v1/skr/verify` (returns `verified`, `reason`, `credits_added`, `spare_credits`); verification is `pending` vs `wrong-or-failed`, and a duplicate signature answers 200 with the balance the owner already holds instead of an error the client would retry; (c) credits land through the ledger's own `GrantCredits(source: "skr")`, so `tx_signature UNIQUE` is what makes a replay a no-op; (d) `ProbeMint` reads decimals off the mint account and warns when a configured `SKR_DECIMALS` disagrees — an operator signal only, never the value used | fails-first, all observed **before** any source existed — `SkrHandler`, `NewSkrHandler` and package `skr` are all absent, so the package does not build and each case is a compile red naming what is missing (compile-red is an accepted form of fails-first here exactly as it was for L33's `DevClientPackage`; the substance each guards is behavioral, and each round-trips real JSON-RPC over HTTP against a stub chain, through the real handler and the real ledger): `TestSkrQuoteStatesThePriceThePackCosts` (a price the server never stated would be whatever the client claims) + `TestSkrVerifyGrantsOnlyWhatTheChainShows` (no server-issued `getTransaction` proves anything) + `TestSkrVerifyRefusesAPaymentItCannotProve` — table over wrong amount / wrong mint / failed `meta.err` (a real signature of the wrong transfer would grant) + `TestSkrVerifyDistinguishesPendingFromUnpayable` (an unlanded transaction and a bad one are indistinguishable, so the client abandons a payment that is still confirming) + `TestSkrVerifyIsIdempotentOnReplay` (one signature would grant every time it was sent) + `TestSkrVerifyRejectsAMalformedSignatureBeforeCallingTheChain` (asserts the RPC receives **zero** calls) + `TestSkrVerificationUsesTheChainsOwnDecimals` (a configured `SKR_DECIMALS=9` against a chain mint of 6 must still price the pack correctly) | ⬤ |
| L37 | **The phone can pay, and says so (FM-SKRb)** — (a) `lib/api.ts` gains an additive `detail` carrying the whole refusal body, so the 402's `code` / `spare_credits` / `skr_amount_per_pack` / `credits_per_pack` reach the sheet that must render them; `.message` and `.status` are untouched, extending the exact mechanism line 808 already reads; (b) `skrApi` (`quote`, `verify`) sits beside `exportApi`, and the export start keeps its 402 offer instead of flattening it to a sentence; (c) `lib/skr-transfer.ts` builds the payment the way Solana Mobile's own `skr-staking` sample builds theirs — **no dependency added**: `findAssociatedTokenAddress` from kit's `getProgramDerivedAddress` over seeds `[owner, Tokenkeg…, mint]` under `ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL`; a `CreateIdempotent` ATA instruction (`data: [1]`, six accounts, payer `WRITABLE_SIGNER`) because a treasury that has never been paid has nowhere to receive; and a `TransferChecked` (discriminator **12**, accounts source → mint → owner → dest) so the mint rides along and a wrong destination fails rather than paying a different token's account; the raw amount is `quote.skr_amount × 10^decimals` with decimals from `getTokenSupply` on the **quoted** mint, so no exponent is constant in the binary; **no dependency added** — the only `package.json` edit is inside the `jest` block (see evidence); (d) `components/build/ExportPanel.tsx`, mounted as the build tab's third sub-view: quote first, `spare_credits ≥ 1` skips the sheet entirely, otherwise pay → keep the signature → verify (retrying `pending`, never re-sending) → start → poll, and a non-`verified` verdict never reaches `exportApi.start` | fails-first, two rounds, 12 tests. **Round A** behavioral, against today's shipped code — `lib/api.test.ts` 2 red / 1 green: `'keeps the paywall offer on the thrown error'` (**Received: undefined** — `parseResponse` attaches only `.status`, so `spare_credits` / `skr_amount_per_pack` / `credits_per_pack` are gone before any sheet could read them) and `'still hands a non-402 refusal its own body'` (**TypeError: Cannot read properties of undefined (reading 'error')**), while `'leaves a successful start alone'` stayed green. **Round B**, written before any source existed and observed red: `lib/skr-transfer.test.ts` → `Could not locate module @/lib/skr-transfer`, covering `'transfer carries the mint and the amount, not just the pair'` (discriminator 12 and the mint in account[1] are the whole point of choosing it over `Transfer`), `'the treasury account is created idempotently, so a second purchase cannot fail'` (a plain `Create` fails the second time), `'the pack amount comes from the mint decimals, never a constant'` (run at 6, 9 and 4 — a hardcoded exponent fails two of the three) and `'the associated token address answers to both owner and mint'`; `components/build/ExportPanel` absent → `Could not locate module @/components/build/ExportPanel`, covering `'prices the pack from the quote the server sent, never from a constant'` (quotes **37** SKR against an unusual mint and asserts both reach the instructions while the backend's own mint does not), `'skips the paywall when the account already holds a credit'`, `'a rejected payment never starts the build'` and `'a pending payment re-verifies the same signature instead of resending'`; `app/(tabs)/build-export.test.tsx` → `Unable to find an element with text: Export` (the build tab offered Chat and Files only). **Harness corrections recorded rather than skipped**, each re-verified by the full suite going green: (1) jest rejects `.mjs` *before* transforming it (`unstable_shouldLoadAsEsm`), so `transformIgnorePatterns` cannot reach `@solana/kit` — resolution had to change instead: `testEnvironmentOptions.customExportConditions: ['node','require','react-native']`, which selects kit's `dist/index.node.cjs` because `node` precedes `react-native` in its export map; no dependency was added, and the five pre-existing suites were run immediately and stayed green. (2) TanStack's default 5-minute `gcTime` timer held the event loop open after every render-only suite, so jest never exited — `gcTime: 0`, the setting `device` and `verify-evidence` already use. (3) jest's worker cannot JSON-serialize a BigInt, so a *failing* BigInt assertion reports `Do not know how to serialize a BigInt` instead of the diff; read those with `--runInBand`, noted in the suite header. Gates: `npm test` **9/9 suites, 43/43 tests** (31 pre-existing + 12 new), `tsc --noEmit` exit 0. | ⬤ |
| L38 | **The export hands over how to submit it (FM-SUBa)** — (a) `ExportHandler.Bundle` at `GET /v1/exports/{id}/bundle`, registered beside `Get` and `Download` in `cmd/server/main.go`: the same owning-project gate, `done`-only with a **409** naming the job's actual status, answering `{"submit_md": …, "manifest": …}` where `submit_md` is `SUBMIT.md` read verbatim and `manifest` is `dapp-store.json` passed through as `json.RawMessage` — **the files are the contract**, so a surface renders what the server wrote instead of re-authoring the steps, and `android_package` / `costs_note` / `publish_command` all arrive from one source; (b) `release.PortalURL` becomes the single constant behind the README's portal line **and** a new `SubmissionBundle.PortalURL`, so the link is server-stated once and lands inside `dapp-store.json`; (c) mobile `ExportPanel`'s done branch fetches the bundle, renders `submit_md` verbatim through the existing `MarkdownView`, and offers **Open Publisher Portal** (`Linking.openURL(manifest.portal_url)`) with the package name printed beside it; (d) web `ExportPanel`'s done branch does the same and replaces its hand-typed costs sentence with `manifest.costs_note`, plus `SUBMIT.md` and `dapp-store.json` offered as file downloads. Auto-submission stays out: Talon still never touches a publisher keypair | fails-first, three surfaces, all red before any source existed. **Backend** — compile-red naming exactly what is missing (the accepted form L33 and L36 shipped theirs in): `h.Bundle undefined (type *ExportHandler has no field or method Bundle)` at all three call sites, plus `undefined: release.PortalURL` and `written.PortalURL undefined (type release.SubmissionBundle has no field or method PortalURL)` → `FAIL internal/api [build failed]`. The two cases: `TestExportBundleHandsOverTheReadmeAndManifest` — a done job whose files the real `WriteBundle` wrote, with `CostsNote` left empty on purpose so the disclosure that comes back must be the shipped default: 200 with `submit_md` byte-identical to the file on disk and carrying `Option A — Publisher Portal`, `manifest.portal_url` = `https://publish.solanamobile.com`, `manifest.android_package` = `com.talon.l38demo` named by both file and README, `costs_note` naming `0.2 SOL`; the same test owns the gates — a stranger's job → **404**, a `queued` job → **409** whose body names `queued` — plus `TestPortalURLIsOneConstantNotAClientLiteral` (the README line and `dapp-store.json`'s `portal_url` must be one value). **Mobile** jest: `'a finished export hands over the submission bundle, not just a status'` (**Unable to find an element with text: Open Publisher Portal**) and `'the package name sits beside the portal button, and the link is the one the server sent'` (**Unable to find an element with text: com.talon.l38demo**), with the four pre-existing FM-SKRb cases green throughout (2 failed / 4 passed at red, 6/6 at green). **Web** vitest: `Failed to resolve import "./SubmissionAssist" from "components/ide/ExportPanel.test.tsx". Does the file exist?` → `no tests`, covering `'a finished export shows the served package, portal and costs note'` and `'the bundle download hands over SUBMIT.md and dapp-store.json'`. **Harness notes, recorded rather than skipped:** (1) the web done branch sits behind a 10 s poll and `@testing-library/dom` 10.4.1 only auto-advances fake timers when a `jest` global exists (`jestFakeTimersAreEnabled()` tests `typeof jest !== 'undefined'`, absent under vitest), so that suite drives `SubmissionAssist` directly — the full job → done → assist path is covered on mobile, whose mocked job query resolves immediately; (2) a single-suite jest run of `export-panel` prints its results and then hangs (`Jest did not exit one second after the test run has completed`, `--detectOpenHandles` reports nothing) — reproduced with **only the pre-existing FM-SKRb tests**, so it is a property of running that file alone and not of these tests, and the multi-suite run exits cleanly, so iteration read the printed results under `timeout`; (3) the first full frontend run reported 2 × 5000 ms timeouts (`PhonePreview`, `TestOnPhone`), both green in isolation and on re-run — parallel-load flake, not a regression. Gates: backend `go build ./...` clean and `go test ./...` **51 ok** plus the intentional `TestShippedDevClientAPKDeclaresItsOwnPackage`; mobile `npm test` **9/9 suites, 45/45 tests** (43 + 2) with `tsc --noEmit` exit 0; frontend `npx vitest run` **18/18 files, 99/99 tests** (97 + 2). | ⬤ |
| L39 | **The phone takes delivery of the APK it paid for (FM-APKa)** — (a) mobile `lib/api.ts` gains `exportApi.downloadUrl(jobId)` and `exportApi.downloadApk(jobId, filename)`: the download rides the **session token** (the same `getToken()` the `request` helper already uses) into `File.downloadFileAsync(url, new File(Paths.cache, filename), { headers, idempotent: true })`, and the result goes to `shareAsync(file.uri, …)` — expo-sharing's **named** export; the package declares no default at all, so `.default` would be `undefined` against the real package while a hand-written mock would happily supply one — so the bytes leave the app's sandbox through the system share sheet instead of being written where nobody can open them; `idempotent: true` is what makes the *second* export work against a filename the device already holds (FM-APKa(iv)); `expo-file-system` (already in the tree), `expo-sharing` (added) and `./auth` are all reached through a **lazy `require(…)` inside the function**, not `await import(…)`: they are native, or must stay out of `lib/api`'s static graph, and jest's ESM dynamic-import path does not consult CJS mock factories — an `await import('./auth')` under a registered `@/lib/auth` mock loads auth.ts (whose line 1 is `import * as SecureStore …`) as CJS and dies on *Unexpected import statement in CJS module*, which is exactly what the run before the seam was written in CJS form reported. Loading `lib/api.ts` anywhere else still does not drag a native module into the module graph; (b) `ExportPanel`'s done branch gains **Download signed APK** with busy and failure states, beside the submission assist, and the two share one job id so the button can never offer a different artifact than the assist describes | fails-first, two reds that each name what is missing, observed before any source existed: jest `'a finished export offers the signed APK it was paid for'` → **Unable to find an element with text: Download signed APK** (the done branch renders a status line and the assist — nothing hands over a file), and `'the download carries the session token and shares the result'` → **TypeError: _api.exportApi.downloadApk is not a function** (the seam does not exist); the second asserts the `Authorization` header and `idempotent: true` reach the download and that the APK mime reaches `shareAsync`, so a token-less request (FM-APKa(ii)) or bytes left in the sandbox (FM-APKa(iii)) cannot pass; harness note recorded: the single-suite jest hang in L38's evidence was root-caused to this suite's QueryClient — `gcTime: 0` was set on **queries** but the pay path settles `startExport.mutate`, and a *mutation's* 5-minute `gcTime` lives under `defaultOptions.mutations`, so any run that paid left a `timeout:300000` from `@tanstack/query-core/timeoutManager` behind (render-only suites exited, which is why it looked file-specific); `mutations: { gcTime: 0 }` added — `npm test -- export-panel` now exits **0** with no `Jest did not exit`; the frontend twin is `testTimeout: 15000` in `vitest.config.ts` (18 files on 8 CPUs starved two trivial renders past vitest's 5 s default) | ⬤ |
| L40 | **An absent email is not an identity (FM-IDENTa)** — (a) `UpsertOAuthUser` step 2 is guarded `email != ""`: the email path runs only when an address was presented, so a wallet sign-in falls through to "create fresh" unless `(provider, provider_id)` is already bound — step 1 (bound path) is untouched, and a real email still links two providers to one human; (b) `migrate()` gains the rebuild, keyed off the legacy DDL (`sqlite_master.sql` for `users` containing `email TEXT UNIQUE NOT NULL`): a legacy table is rebuilt on a **single pinned `db.Conn`** — `PRAGMA foreign_keys=OFF` before `BeginTx`, `CREATE TABLE users_new` in the final nine-column shape *without* the column-level `UNIQUE`, `INSERT INTO users_new (…) SELECT … FROM users` with explicit ids so AUTOINCREMENT continues past the copied max, `DROP TABLE users`, `ALTER TABLE users_new RENAME TO users`, `PRAGMA foreign_key_check` (any row rolls the transaction back), `COMMIT`, `PRAGMA foreign_keys=ON` before the conn returns to the pool — then `CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users(email) WHERE email != ''` runs unconditionally so fresh databases get the same guarantee, and `queries`' `CREATE TABLE users` drops `UNIQUE` so new files are born with the new shape and the detection text stays a pure legacy marker; (c) `walletauth.go`'s comment states the law it actually follows — bind by address, link by email only when there is one, else create fresh | fails-first store tests, both red observed before any fix existed: `TestUpsertOAuthUserSeparateAccountsForEmailLessWallets` → **second wallet returned user 1 — both identity rows on `user_id=1`** (the merge), `TestMigrateRebuildsLegacyUsersEmail` → **"users still carries the column-level UNIQUE, so the rebuild never ran"** — the same test then requires the partial `idx_users_email` to exist on the file; the same file carries the keep-tests that must not move: a duplicate *real* address still yields `ErrDuplicateEmail` (the partial index's message must keep containing `UNIQUE constraint failed`), the same wallet signing in twice stays one account (bound path), one real address from two providers stays one account (the linking law), and a second `New()` on the rebuilt file is a no-op with `pragma_foreign_key_check` clean and the child row still pointing at its parent | ⬤ |
| L41 | **The push tells the truth about where the turn stopped (FM-NOTIFa)** — the terminal guard in `PromptHandler.Stream` becomes `if res.outcome == nil \|\| (!res.outcome.Paused && !res.outcome.Exhausted)`: both resumable stops — ask_user (`Paused`, done `waiting`) and the round budget (`Exhausted`, done `paused`) — go quiet, `Exhausted` was the one that fell through because it also returns a nil error, while `Stalled`, `BudgetStop` and `HookStop` stay terminal and still push, and an error still pushes `build_failed`; the guard's comment now names both pauses so the next stop added to `BuildOutcome` has to be classified here | fails-first api test with the scripted stand-in provider, `TestBudgetPauseIsNotBuildSuccess` (AI_MAX_ROUNDS=2): leg 1 spends the budget → done frame `status=paused` **and zero `build_success` rows** (pre-fix: one row — `Build finished / verified: false`, the same row journey3 recorded live as notification id 4), leg 2 resumes → done `status=success` **and exactly one `build_success`** (pre-fix: two) — the second half pins FM-NOTIFa(iii), the real finish must keep firing once; the live false row in the journey DB is deleted with the fix, and stage 8 of the end-to-end journey then asserts `build_success == 1` against a clean history | ⬤ |
| L42 | **A test sweeps only what it owes (FM-TESTa)** — `Manager.Reconcile` splits: the public `Reconcile(s)` keeps the full startup sweep (DB→docker repair + orphan destruction + legacy removal, behavior unchanged) and delegates to an internal `reconcile(s, sweepOrphans bool)`; `TestReconcileDestroysGoneContainers` calls the non-sweeping half so its "nothing is created or removed" comment becomes true by construction, and it gains the census: `ListContainers()` snapshotted before and after the call, asserted identical — the comment becomes an assertion | fails-first: with a probe container `talon-redroid-7777` running, the pre-fix test (full sweep) fails the census — RED, the exact destruction FM-TESTa(i) witnessed live (docker events kill→destroy, the 04:49:03 gate run that took the warm container); after the split the same run passes with the probe still alive; the swept half stays covered by L43's pure orphan-plan test so the production sweep keeps a test | ⬤ |
| L43 | **The sweeper spares what the pool prepaid, and a dropped slot refills (FM-POOLa)** — (a) the orphan loop's decision extracts into a pure `orphanPlan(docker []ContainerInfo, claimed map[string]*store.Session, isWarm func(string) bool) []ContainerInfo` used by the sweep; (b) `Manager` gains `ProtectWarm(isWarm func(containerID string) bool)` consulted only by the orphan branch (`nil` = today's behavior), `Pool` gains `IsWarm(id)` under its mutex, and `main.go` wires them together by moving `StartCleanupLoop` to after `NewPool` — the startup `Reconcile` still runs before the pool exists, so foreign crash leftovers are still collected, and the loop starts only after the spare is wired (happens-before, no race); (c) `Claim`'s stale-entry drop signals `p.claim` before `continue`, exactly as a successful claim does | fails-first: (1) pool test — a warm map seeded with a dead container id, `Claim` → retag 404 → drop: RED pre-fix (no refill signal queued — select on `p.claim` finds nothing), GREEN: signal present; (2) pure `orphanPlan` test — unclaimed+not-warm → planned (pins the production sweep), unclaimed+warm → spared, claimed → never touched; (3) live, with the probe running against the current binary: the very next 5-minute cleanup tick destroys it (`orphaned=1` in the reconciler log, docker events at the tick) — and after the fix the server's real warm container survives a full tick — witnessed live on the fixed binary at 06:11:40 (`recovered=0, destroyed=0, orphaned=0`, `talon-redroid--3` still Up) | ⬤ |
| L44 | **The relay answers "upstream not ready" with a status (FM-RELAYa)** — `internal/relay/relay.go`'s dial-failure path gains `replyUnavailable`: drain the request bytes already arriving (short deadline, so the following close carries no unread data and cannot RST the answer), then write `HTTP/1.1 503 Service Unavailable` + `Retry-After: 5` + `Connection: close` + a body naming the upstream — instead of returning into a silent close; the §2.4 token gate still runs first, so an unauthenticated prober learns nothing, and the pass-through path is untouched | fails-first relay test: `Start(freePort, closedPort)` (a metro address nothing listens on), the "phone" writes `GET /index.bundle…` → RED pre-fix (read fails with zero bytes — EOF/reset, exactly the `SocketError: other side closed` that killed journey3b at :446), GREEN: status line `503`, `Retry-After: 5`, body names the refused upstream; live failure evidence on record: server log `05:47:33 upstream dial … connection refused` (relay wired at startup-timeout) vs metro listening `05:47:40` | ⬤ |
| L45 | **An orphan keystore file is moved aside, not misdiagnosed (FM-KEYSa)** — `Keystore.Ensure`'s no-row path stats the target before `-genkeypair`: an existing file is renamed `<name>.orphaned-<unix>` with a log line naming the project and the reason (no row owns it → its password died with the row → unreadable by construction), so genkeypair always starts on a clean path; the write order stays row-after-success (the row remains the source of truth) and the row-without-file branch keeps its regenerate-and-log behavior — the two branches now degrade symmetrically | fails-first release test (real keytool): temp keystore dir seeded with a valid JKS written under a password no row has ever seen, no row → RED pre-fix (`keytool failed: … keystore password was incorrect`, no row saved — the exact live failure, export job 1 / 05:58:31), GREEN: `Ensure` returns a working record (stored password opens it via `-list`), the orphan sits renamed `.orphaned-*` beside it, and a second `Ensure` reuses the same file — LIVE on the fixed server: the real orphan was moved aside at 06:07:55 (`moved aside to keystores/project-1.jks.orphaned-1791004075 and regenerating`) and export jobs 4/5 sailed past `Ensure` | ⬤ |
| L46 | **Each password reader gets its own file (FM-SIGNa)** — `Builder.signAPK` writes TWO 0600 temp files (one for `--ks-pass file:`, one for `--key-pass file:`) instead of passing a single path twice; both are cleaned up, argv stays password-free, and the rest of the invocation is unchanged | fails-first sign test (real keytool + apksigner + a `aapt package`-built 611-byte fixture, skipping exactly where production's own SDK gate would): `signAPK` over a keystore from `Ensure` → RED pre-fix (`apksigner failed … Failed to read Key … end of file reached` — the byte-for-byte live failure, export job 3 / 07:26:32; isolated repro A), GREEN: the signed artifact passes `apksigner verify --print-certs` printing the keystore's DN; shared-file-with-newline ruled out as a fix (repro B), two-files proven (repro C) — LIVE: export job 5 ran `sign` → `verify-signature` clean on the fixed binary (08:36:13), the first signing in this codebase's life | ⬤ |
| L47 | **Fingerprint equality compares bytes, not spelling (FM-FPRa)** — `sameFingerprint`'s normalizer strips every non-hex separator (`:` and whitespace) and uppercases BOTH sides before comparing, so apksigner's bare-hex digest and keytool's colon-delimited digest of the same certificate equate; the full hex value must match (no prefix/substring shortcuts) and the `a != ""` empty guard stays | fails-first unit test with the LIVE strings from export job 4: `sameFingerprint("a6cedc…5584", "A6:CE:DC:DA:…:5584")` → RED pre-fix (`false` — the export's exact `verify-signature` failure, 08:02:31), GREEN: `true`; a genuinely different fingerprint, a truncated one, and two empties all stay `false` — LIVE: export job 5 logged `release verified (package com.talon.neighborhoodrewards, signer a6cedc…5584)` at 08:36:17, the two spellings equated in production | ⬤ |
| L48 | **`app/` is routes only — tests never sit in the route tree (FM-ROUTERa)** — the three test files under `app/` (`menu/help`, `(tabs)/build-export`, `(tabs)/device`) move to `__tests__/` mirroring the structure, the two relative screen imports rewritten through the `@/` mapper; a guard test walks `app/` recursively and fails the suite if any `*.test.*`/`*.spec.*` file appears there | fails-first guard test → RED while the three files sit in `app/` (listing them, quoting the live `Android Bundling failed … require("console")` stack from `expo start`), GREEN after the move: guard passes and the full mobile suite is green via `npm test` from `talonmobileapp`; bundle check: Metro serves `index.bundle?platform=android` without the `require.context` crash | ⬤ |
| L49 | **The phone's stream settles on headers and delivers while the turn is open (FM-SSEa)** — (a) `openStream` gets its fetch from `expo/fetch` through a **lazy `require(…)` inside the function** (L39's pattern — native, so it stays out of `lib/api`'s static graph; the phone's exact APK carries `ExpoFetchModule`, 108 hits in `classes2.dex`): its promise resolves at `RESPONSE_RECEIVED` — headers — and body chunks arrive via `didReceiveResponseData` → `response.body.getReader()`, so the 15 s connect deadline bounds headers, its documented job, instead of outliving a turn it cannot see; the initial call **and** the 401-retry path both go through it; (b) the TEMP DIAGNOSTIC scaffolding leaves with the fix — `api.ts`'s stage `marks`/`diag` events, the hook's `diag` counter and `applyEvents` arm, `reduceEvent`'s `diag` case, `build.tsx`'s `diagBox`, the build-export mock's `diag` key — its own comments say *remove once streaming works*, and a `stream debug (dev)` box must not be on camera; (c) nothing else moves: the reducer and the screen already render thinking/text/output/tasks live (`PixelGrid` was only the fallback with zero events) — the events were the missing half | fails-first `lib/api-stream.test.ts`: `'delivers events while the stream is still open'` + `'never talks to the browser fetch for a stream'` — global `fetch` stubbed to whatwg-fetch semantics (settles only at body end), `expo/fetch` mocked to settle at headers with a caller-fed reader; RED/GREEN: RED = `'delivers events while the stream is still open'` received only the diag stage marker (no events) + `'never talks to the browser fetch'` — browser fetch called **once** with the real URL; GREEN = both green in the full 13-suite run. **Live (2026-10-03, user's phone): new project streamed live mid-turn — "i just created a new project and saw that it streams"**; backend log same session: `POST /v1/prompts/stream bytes=17460 latency_ms=5289 status=200` with events rendered as they arrived instead of the pre-fix 15 s death | ⬤ |
| L50 | **The composer lives above the keyboard (FM-KBDa)** — (a) `components/app-providers.tsx` mounts keyboard-controller's `KeyboardProvider` at the app root — static import, lawful because the dev client was rebuilt with the package linked on 2026-10-03, so the import and the binary move together (FM-KBDa(ii)); (b) `app/(tabs)/build.tsx` wraps **the composer block** in the package's `KeyboardStickyView` — the one element that must ride the keyboard, offset `{closed:0, opened:0}` so it sticks exactly at the IME edge — instead of a whole-screen avoider, which the shipped `android:windowSoftInputMode="adjustResize"` already resizes (a second whole-screen pad would double-offset); (c) nothing else moves: the composer's flex column, `Screen`'s layout and the manifest stay as they are, and no link guard exists to drift | fails-first `components/app-providers.test.tsx` (RED: the provider marker is absent — the root never mounted it) + `__tests__/(tabs)/build-keyboard.test.tsx` (RED: the composer is not wrapped by the package's sticky view); keep-gates `npm test` / `tsc --noEmit` / `lint:check`; live keyboard evidence on the rebuilt phone recorded in the evidence cell after the run | ⬤ |
| L51 | **Reasoning collapses; activity is the interface (FM-THINKa)** — (a) reducer: `thinking.delta` sets `thinkingActive: true`, and one seam around the frame switch clears it on every content frame except another thinking delta (`phase` frames name the round, not output) — the honest "is the model thinking right now" signal; (b) new `components/build/ThinkingRow.tsx`: collapsed-by-default disclosure row — `Thinking… <t>` ticking while `thinkingActive`, frozen `Thought for <t>` after, tap toggles the full reasoning text (the researched pattern: CopilotKit's header+toggle, Claude Code's collapsed stub, DeepSeek's `ReasoningRow`); (c) `build.tsx` swaps the raw `thinkingBox` dump for `ThinkingRow`, placed above the trace so the decision reads before the act — the tool/skill/verify/decision/command `TraceRow`s and the phase `PixelGrid` stay the visible progress layer untouched | RED: `L51a/b/c` — `thinkingActive` `Expected: true / Received: undefined` (field absent) + `thinking-row` suite `Could not locate module @/components/build/ThinkingRow` (accepted module-red form); GREEN = `thinking-row` 3/3 + `reduce-event` L51 block green, full run **58/58 (14 suites), tsc 0, lint:check 0 errors**; **Live (2026-10-03, user's phone): the collapsed row rendered mid-stream — the report that opened L52 ("only showing thinking at the top") is this row working, positioned in a fixed slot; L52 inherits it per-segment** | ⬤ |
| L52 | **One chronological timeline: interleaved thinking, in-place tools, plan on top (FM-TLKa)** — (a) `TurnState` gains `timeline: TimelineItem[]` (`thinking` \| `narration` \| `tool` \| `decision` \| `skill` \| `output`) appended/upserted in arrival order by `reduceFrame`: thinking deltas fold into a trailing streaming segment or open a new one; narration folds the same way; `tool_call` frames upsert by `data.id` — the paired `running`/`completed` frames settle one row in place instead of appending twice — id-less frames append; `verify` upserts by stage; `decision` appends its row (the suppressed `write_adr`/`write_memory` rule unchanged); `command_output` extends a trailing output block or opens one behind the running tool; the legacy `thinking`/`toolCalls`/`text` fields keep updating exactly as they do today, so L51's signal and every pinned contract hold; (b) `build.tsx` renders the timeline in order — `PlanCard` + checklist move **above** it (top of the turn; the card's outlive rule and the approve action untouched), question/answers/evidence/`PixelGrid` keep their roles below; (c) `ThinkingRow` gains `preview` — the segment's first line, clamped, rendered only while it runs: one honest line of "now it's looking at x" under the ticking label, full text still one tap away; (d) auto-follow through a pure `lib/live-follow.ts` helper (at the bottom → follow; a manual scroll-up releases; the `↓ live` pill re-attaches) wired to the tail's `ScrollView`; (e) `Transcript.tsx` walks each turn's stored blocks **in stored order** — `thinking` → collapsed `Thought for …` row, `text` → markdown where it sits, `tool_use` → `TraceRow` (`write_adr`/`write_memory` → `Recorded …` with the input's decision head, the web's reconstruction), `plan` → card — and only the newest `turn_end` per prompt renders its marker, so a stale "Turn cancelled" can no longer outlive its replacement | fails-first, five reds: `reduce-event` `L52a` (thinking-after-tool folds into a new segment at the end — today `timeline` is absent, `Received: undefined`), `L52b` (running+completed same id → **one** row settled — today two), `L52c` (narration split, decision in arrival order), `lib/live-follow` (module absent — accepted module-red), `thinking-row` `L52d` (preview absent while running); plus `transcript.test.tsx` reds (thinking block renders `Thought for …`; newest marker wins) and a `build` resume-visibility red (tail hidden while streaming with history holding the prompt — L53's gate, pinned here because it is the same render); GREEN (2026-10-03): `npm test` 21 suites / 90 tests — `reduce-event` 23 (L52a–e + L54b + every legacy pin), `live-follow`/`turn-visibility`/`timeline` 8, `thinking-row` 5 (L52d1 preview red → green), `transcript` 4 (L52i/j/k reds → green), `build-resume` 2; `tsc --noEmit` clean; `lint:check` 0 errors; live interleaved observation on the phone recorded after the run | ⬤ |
| L53 | **Resume streams in view (FM-RSUMa)** — (a) tail visibility becomes one pure rule: visible while `streaming`/`waiting` **regardless of** `historyHasTurn`, and on `done`/`error` only until history carries the prompt; (b) while the tail shows a prompt, `RestoredTranscript` receives history with that prompt's rows filtered out — one filter point released together with (a) at settle, so a mid-stream refetch (tab focus, stream-dead poll) can neither duplicate the rounds nor kill the view; (c) `resume()` and `answerPrompt()` accept the prompt's stored blocks from the screen's already-loaded history and seed the timeline before streaming (settled thinking segments, settled tool rows through the shared label map, decisions, narration) — the continuation picks up where it stopped instead of a blank tail | fails-first: `lib/turn-visibility.test.ts` (`isTailVisible('streaming', true)` must be true — today's inline rule answers false, the 69 KB-invisible-resume state as a pure function) + `build` component red (history seeded with the live prompt's rows: the tail's content must render while streaming — absent today) + seed red (`seedFromBlocks` module absent); GREEN (2026-10-03): full run 21 suites / 90 tests — `turn-visibility` pins (`isTailVisible('streaming', true)` true, `excludeLivePrompt` drops the prompt's rows only while the tail shows them, released at settle) + `build-resume` component reds → green (tail renders while streaming with history holding the prompt, restored user bubble kept, no double rows, seeded resume continues onto the interrupted rounds); **live proof = press Resume on the phone and watch frames arrive**, backend line `POST /v1/prompts/{id}/resume` with the tail visible, recorded here after the run | ⬤ |
| L54 | **The footer claims only what ran (FM-VRFYa)** — (a) backend omits `verified`/`verified_best_effort` from the `done` frame (`finishBuildStream`), the `turn_end` marker payload and the non-stream build response unless a check could have run — `FilesTouched`, `VerifyPolicy` or `SkippedGates` present — and the outcome is neither `Paused` nor `Exhausted` (`verdictClaim`, the single rule all three emitters read); no check could have run means no verdict; genuine failures carry files + policy and stay claimable; (b) mobile: the red chip already keys off `verified`'s presence — the missing half is the claim gate: verdict words only from a present key; files render alone (descriptive — F6b); rounds render alongside a claim or from a legacy payload-less marker (F2) — a payload present but empty, and a live rounds-only done, render no row at all (a cancelled turn's rounds stay in its status sentence); (c) web `TurnFooter` gets the same two gates — live `done` mapping and restore `turn_end` mapping — one vocabulary, both clients | fails-first, three reds: Go `TestDoneFrameOmitsVerifiedWhenNothingRan` (today's payload ships `"verified":false` for a no-changes outcome — the exact `done-no-changes after 1 rounds` shape from the live log) + marker twin; jest `verify-evidence` `L54b` (rounds-only evidence renders a row today — `Received: "1 round"`); web vitest `TurnFooter` (false + zero files still claims); GREEN (2026-10-03): `go test ./internal/api/` — `verdict_wire_test.go` red → green (done frame omits `verified` when nothing ran, keeps it when checks ran; `verdictClaim`/`turnEndEvidence` pinned) + e2e F1–F11 with F2 rewritten to the absence pin (`verified`/`verified_best_effort` absent, files empty, policy empty, payload still present); mobile `verify-evidence` 14 (L54b4–6 reds → green); web vitest 18 files / 101 tests (L54c/L54c2 reds → green, F1–F11 hold); full `go test ./...` carries only the pre-existing MEANT-TO-STAY-RED FM-DEVd APK-artifact test (user rebuild via `build-dev-client.sh`); **live = a no-changes/plan turn on the phone shows no "checks did not pass"**, recorded here after the run | ⬤ |
| L55 | **The dev client installs from inside the app (FM-APKi)** — (a) new `lib/devclient.ts`: `downloadAndInstallDevClient(url)` — `expo-file-system`'s `File.downloadFileAsync` over the `getToken()` header with `idempotent: true` (L39's proven seam, lazy `require` so the native module stays out of the static graph), then `expo-intent-launcher`'s `ACTION_VIEW` + `application/vnd.android.package-archive` on the file URI, `shareAsync` (expo-sharing, already in the tree) as the fallback when the intent refuses; (b) `device.tsx` swaps `Linking.openURL` for the helper with busy → installing → error states — the browser path is deleted, not demoted; (c) `expo-intent-launcher` joins the manifest (stageable: `package.json` + `yarn.lock`) | fails-first `__tests__/(tabs)/device-install.test.tsx`, three reds: pressing the APK button must **not** call `Linking.openURL` (today it does — the browser/auth wall itself), the download must carry `Authorization: Bearer` (today no download exists — `Received: undefined`), the resolved file must reach `startActivityAsync('android.intent.action.VIEW', …archive MIME)` with `shareAsync` only on refusal (intent never called today); GREEN (2026-10-03): full run 21 suites / 90 tests — `lib/devclient.test.ts` 4 (session token rides the Authorization header never the URL, ACTION_VIEW + archive MIME reaches `startActivityAsync`, intent refusal falls back to `shareAsync`, a failed download surfaces) + `device-install.test.tsx` (button drives the helper, `Linking.openURL` never called); `expo-intent-launcher@~55.0.16` in package.json + yarn.lock + install-state; **live install on the phone after the template rebuild**, recorded here after the run | ⬤ |
| L56 | **A waiting turn always has its answer surface (FM-ANSQa)** — (a) `components/build/Transcript.tsx`: the restored question card pins to the turn that owns the wait — the last turn, matching `waitingPromptId` (legacy rows without a prompt id keep passing), web's `isLastTurn` rule — and a **fallback answer card** fires when that waiting turn has NO `ask_user` block (the plan-question shape): it shows the turn's own last stored text (the question prose `planAsksQuestion` classified; `persistPlanExchange` guarantees a non-empty message) and answers through the same `onAnswer(waitingPromptId, text)` → `answerPrompt` → `POST /v1/prompts/{id}/answer` path the ask_user card uses — the backend already continues plan planning from that route; (b) reducer `plan` case adopts `data.plan` only when present — a plan-less question reply leaves `plan` undefined (web parity), so no empty PlanCard / "Approve & build" renders over a question; the message still lands as narration; (c) the composer gate is untouched — with (a) landed its "answer the question in the transcript first" is finally true | fails-first, three reds: `components/build/transcript.test.tsx` `L56a` (waiting plan-question turn — user text + assistant text, no `ask_user`, `waitingPromptId=84` — must render the answer card; today **no placeholder exists**, the exact stranded state) + `L56b` (an old turn's `ask_user` must not re-open while a newer prompt waits — today its card renders) + `hooks/reduce-event.test.ts` `L56c` (`plan` frame without a `plan` key must leave `state.plan` undefined — today `Received: {}`); root cause read live off prompt 84: sqlite `mode=plan status=waiting`, blocks `user\|text + assistant\|text`, wire = `done{waiting}` with no `question` frame; **GREEN (2026-10-03): all three reds pass and the mobile gates hold — `npm test` 21 suites / 93 tests (90 + L56a/b/c), `npx tsc --noEmit` clean, `npm run lint:check` 0 errors (7 pre-existing warnings)**; live proof — reload on prompt 84's project → card renders over the question → answer continues planning — recorded here after the run | ⬤ |

| L57 | **Expo `[param]` routes pass the plan door (FM-C1e)** — `AppPlan.Validate`'s glob case narrows `strings.ContainsAny(file, "*?[")` → `ContainsAny(file, "*?")`: brackets are ordinary filename characters, so Expo Router dynamic routes (`app/address/[address].tsx`, `app/post/[id]/[slug].tsx`) validate as the exact paths they are, while `*` and `?` keep failing with the offending feature named | fails-first `TestPlanAllowsExpoDynamicRouteFiles` (red ×3: `app/address/[address].tsx` rejected "must be an exact path — no globs" — prompt 84's exact death) + keep-guard `TestPlanRejectsMalformedFeatureFiles` extended with `app/?.tsx` (the star row already pins `app/*.tsx`); **GREEN (2026-10-04):** all three dynamic routes pass, the 5-row malformed table still rejects `*`/`?`/traversal/absolute/node_modules with the feature named, full `go test ./internal/ai/` ok | ⬤ |
| L58 | **The raw error reaches the server log before the rewrite (FM-RAWa)** — `friendlyAIError`, the single choke every friendly form funnels through (7 call sites: both stream error events, both non-stream responses, the prompt response row, the build-failure dialog, the answer path), logs `api: raw error behind friendly message: %v` before switching — one line covers every emitter, so the gateway's own words (`unauthorized client detected`) survive the rewrite that turned them into a wrong instruction about AI_API_KEY | fails-first `TestFriendlyErrorLeavesRawErrorInServerLog` (red: log empty — the rewrite logged nothing); **GREEN (2026-10-04):** the agentrouter 401 body (`unauthorized client detected`) now lands on stdout as `api: raw error behind friendly message: …` while the user-facing rewrite stays the friendly mapping, full `go test ./internal/api/` ok; **live (2026-10-04 02:40):** the phone's failing turn logged the raw 403 body three times before the rewrite — `api: raw error behind friendly message: … pre-consume quota failed, user quota: ＄0.250056, need quota: ＄0.413152` — the real cause (an empty balance, not `AI_API_KEY`) read off the server log in one grep | ⬤ |
| L59 | **A 401/403 gets one backoff retry, then the same death (FM-AUTHa)** — (a) new `Client.authBackoff` field + `authRetryDelay()` (0 ⇒ `defaultAuthBackoff` 3 s, test-shrunk — the `rateLimitWindow` pattern), carried by `clone()`; (b) all four gateway call sites gain the same branch: a first 401/403 waits `authRetryDelay` (ctx-cancellable), reports `llm/retry` reason `unauthorized (HTTP 40x)`, retries once — a second refusal returns the original status-bearing error, so `friendlyAIError`'s mapping and everything keying on `401`/`403` stays byte-identical; (c) `retryClass` maps `unauthorized`/`HTTP 401`/`HTTP 403` → `auth_rejected` so the audit groups the incident instead of burying it in `other` | fails-first `TestOpenStream401GetsExactlyOneRetryThenFails` (`openStream` = the path plan/build turns take) + `TestDoWithRetry401GetsExactlyOneRetryThenFails` + `TestChatAnthropic401GetsExactlyOneRetryThenFails` + `TestChatOpenAI401GetsExactlyOneRetryThenFails` (each red: **1** request reaches the gateway — the first refusal returns) + `TestRetryClassGroupsAuthRejections` (red: `other`); **GREEN (2026-10-04):** all five reds → green — every site sends exactly 2 requests against an always-401 gateway (10 ms shrunk backoff) and still returns the status-bearing error `friendlyAIError` maps, `retryClass("unauthorized (HTTP 401)")` → `auth_rejected`, `go test ./internal/ai/ ./internal/orchestrator/` ok; full-run note: api suite 295 s under load-30 (the user's concurrent `expo run:android` — `npx eslint` ~42 s per lint gate, baseline-identical); live = the next gateway flap logs `api: raw error …`, records `llm/retry` class `auth_rejected`, and survives one Continue; **live (2026-10-04 02:40):** gateway 403 → `openStream: HTTP 403 — identity rejected, one retry in 3s` (02:40:33) — one wait, one retry, second refusal returned the original status-bearing error → `turn model-request-failed after 1 rounds` (02:40:39); cause was a hard quota wall (`pre-consume quota failed`), so the turn correctly died after its single retry instead of hammering — the survives-one-transient-flap half still awaits the post-top-up window | ⬤ |

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

## Appendix F — provider comparison: Atria vs agentrouter (2026-09-29)

**Question.** Run the same behavioral test against the current provider
(Atria, openai protocol, `Atria-Dawn-Preview`) and the new one
(agentrouter.org, anthropic protocol, `deepseek-v4-flash`), score both,
say which one the product should prioritise. Same prompts
(`e2e/prompts/momentum-*.txt`), same recipe (`e2e/run-behavior.sh`), same
60-round budget; the only `.env` difference is the provider block plus
`AI_USER_AGENT` (that gateway authenticates on it — FM-UAa).

**Verdict.** agentrouter / `deepseek-v4-flash` wins **6 rows, loses 2,
ties 4** — and one of its two losses is an infrastructure artefact, not a
model difference (see S12 below). It reaches first write in 2m42s vs
8m35s, ships 5/6 planned routes vs 4/6, marks **15/15** checklist items
honestly vs 4/13, and finishes the turn in 18m23s at 58 rounds where
Atria needed the full 60-round budget over 42m32s — 2.9× the throughput
(16.3 vs 5.6 screens/h) in 57% less wall time. Its one behavioural
weakness is verification
economy: 13 `run_checks` ≈ 6m37s = 36% of wall vs Atria's 8%.

### The three runs

| | Run-6 | Run-7 | Run-8 |
|---|---|---|---|
| provider / model | Atria · `Atria-Dawn-Preview` | agentrouter · `deepseek-v4-flash` | agentrouter · `deepseek-v4-flash` |
| protocol / transport | openai, streamed tool_calls | anthropic, SSE | anthropic, SSE |
| binary | `7fdf542` (L23/L24) | `1507594` (L25) | `dd4a0c5` (L26/L27) |
| outcome | completed · 42m32s · 60/60 rounds | paused at budget · 14m37s · 60/60 | completed · 18m23s · 58/60 rounds |
| usable as a measurement? | yes | **no** — two scorecard-voiding defects, fixed as L26/L27 | yes |

Run-7 is recorded, not discarded: it is the measurement that proved both
defects exist (plan stored nothing while reporting success; zero
`est_input_tok=` lines ⇒ S9/S10 `?` ⇒ §5 blocks the run), and both fixes
are fails-first tested against its exact state. Its numbers are excluded
from the comparison because S3/S5/S7 had no plan to score against.

### Rows — Run-6 (Atria) vs Run-8 (agentrouter)

| Row | Standard | Run-6 · Atria | Run-8 · agentrouter | Winner |
|---|---|---|---|---|
| S1 first write | ≤ 3 min | 8m35s ❌ | 2m42s ✅ | agentrouter |
| S2 first route file | ≤ 5 min | 14m54s ❌ | 4m17s ✅ | agentrouter |
| S3 scaffold | all 6 plan routes | 4/6 ❌ | 5/6 ❌ | agentrouter (closer; both fail) |
| S4 reads before first write | ≤ 12 | 28 ❌ | 17 ❌ | agentrouter (closer; both fail) |
| S5 throughput | ≥ 4 screens/h | 5.6/h ✅ | 16.3/h ✅ | agentrouter (2.9×) |
| S6 runnable while building | last check green | ✅ | ✅ | tie |
| S7 checklist truth | all shipped marked | 4/13 ❌ | **15/15 ✅** | agentrouter |
| S8 verify economy | ≤ 15% of wall | 8% ✅ | 36% ❌ | Atria |
| S9 tool contract | ≥ 26 advertised, 0 missing | 41/call ✅ | 41/call ✅ | tie |
| S10 input economy | prefix stable; tail bounded | 10987→11900 tok, 29 rebuilds, body 252,751 B ✅ | 12070→12641 tok, 28 rebuilds, body 234,748 B ✅ | tie |
| S11 cost accounting | every run priced | ? (L7 open) ❓ | ? (L7 open) ❓ | tie — blocks a full PASS on both |
| S12 progress visibility | gap ≤ 90 s | 31s ✅ | **133s ❌** | Atria — **but infrastructure, not the model** |
| required-row failures | 0 to pass | 5 | 4 | — |

**S12's 133s is FM-ANTa, measured.** The gap is `phase 00:05:08 → usage
00:07:21` = build round 3, a single buffered round of 2m12.9s that wrote
nothing to the wire because `ChatWithTools` drops the `StreamSink` on the
anthropic path; the same round on openai would have streamed its text and
reasoning deltas throughout. The other four largest gaps (36–40s) are
`tool_call → command_output` runs, all under the bar. Scoring this row as
"a model visibility difference" would be wrong; scoring it as "fine"
would also be wrong — the transport really did go dark for 133 seconds,
which is exactly what L8 was built to prevent. Fix is FM-ANTa (candidate
L28), not a provider setting.

**Same round, second finding:** round 3 also hit `stop=max_tokens` with
`out=32768` — the *raised* ceiling — while returning6 usable tool calls
(the loop executed them and continued). One round in 57 exhausted a
32,768-token budget on thinking alone; a thinking model's budget has to
be sized for reasoning + payload, not payload.

### Tool-call shape (behaviour, not transport)

| | Run-6 · Atria | Run-8 · agentrouter |
|---|---|---|
| write / edit / delete | write 29 · edit 14 · delete 13 | write 37 · multi_edit 10 · edit 4 · delete 18 |
| reads | read_file 24 | read_file 29 |
| verification | run_checks 10 | run_checks 13 (+ prettier1) |
| skills | read_skill 8 | read_skill 7 |
| VCS | — | git_commit 8, git_status 1 |
| distinct tools | 14 | 18 |

Run-8 commits as it goes (8 commits vs none) and leans on `multi_edit`
instead of single `edit_file`; both stayed inside the tool contract
(S9: 41 advertised, 0 promised-missing).

### Telemetry (both runs measured, L27 verified in production)

| | Run-6 | Run-8 |
|---|---|---|
| `est_input_tok=` / `request-ledger` lines | 59 / 59 | **57 / 57** (Run-7: 0 / 60 — the defect) |
| stop mix | 58 tool_use, 1 end_turn | 55 tool_use, 1 end_turn, 1 max_tokens |
| system prompt rebuilt | 29× | 28× |
| max-body attribution | system=42007 tools=20539 payloads=24229 results=26135 history=31968 envelope=3617 | system=47479 **tools=22216** payloads=20608 results=89165 history=36909 envelope=18371 |

The `tools` layer — 20,539–22,216 B ≈ **5,100–5,550 tokens on every
single request** — is unchanged between providers and is the largest
attributable layer in both runs: P2 #11 (tool-def diet) remains the
biggest input lever either model is paying for.

### Confounds, stated plainly

1. **Protocol differs by necessity** — agentrouter speaks anthropic only,
   Atria openai only. S1–S11 measure what was produced and are
   unaffected; S12 measures the wire, and the anthropic wire is buffered
   (FM-ANTa). Comparable as "over/under the 90s bar", not as equal
   figures.
2. **Output ceiling moved** — Run-6 ran at `max_tokens` 16384, Run-8 at
   32768 (the fix for Run-7's truncation, FM-PLANa). A ceiling is a limit
   not a target: no request is shaped by it unless the model would
   otherwise be cut, which is precisely the case it was raised for.
3. **S11 unmeasured on both** (no published pricing — L7 open), so
   neither run can be a full PASS under §5; the comparison is row-by-row.
4. **Run-5 excluded** — killed by an external 429 at round 8; Run-6 is
   the Atria measurement.
5. **Blocked follow-up** — `claude-opus-5` on agentrouter returns HTTP
   402 `Budget pool quota has been exhausted`; the three-way comparison
   (Atria / deepseek / opus) needs an administrator quota increase, then
   is a straight Run-9 on this same recipe.

### Reproduce

- Run-6: `e2e/run-behavior.sh run6 …` → `e2e/reports/behavior-run6.md`
- Run-8: `e2e/run-behavior.sh run8 …` → `e2e/reports/behavior-run8.md`
  (`e2e/reports/behavior-latest.md` always holds the newest; re-score any
  run with `e2e/scorecard.py --db backend/talon.db --project <id>
  --prompt <id> --stream … --serverlog … --fresh`)
