# Talon — Implementation Notes (W-series)

Running engineering log for `PLAN_WORLDCLASS.md`: per phase, what changed, and how
it connects to the two clients — the **web IDE** (`frontend/`) and the **companion
app** (`talonmobileapp/`). That plan says *what* and *why*; this file says *how it
reaches the screens*.

---

## Transport map (verified 2026-09-22, from source)

| Client | Base URL | Auth | Live updates |
|---|---|---|---|
| Web IDE — `frontend/lib/api.ts` | `BASE` (same-origin) | JWT; a 401 rotates via `POST /v1/auth/refresh` and replays the request once | `EventSource` on `/v1/projects/{id}/metro?token=…`; prompt/build streams over SSE (`Stream` in `internal/api/prompts.go`) |
| Companion — `talonmobileapp/lib/api.ts` | `EXPO_PUBLIC_API_URL` (dev: LAN IP, never localhost-on-device) | OAuth GitHub/Google, `talon://` redirect, single-use exchange code, single-flight refresh; JWT in Keystore-backed storage | Push notifications + reconcile-on-open (PHASE_17 §2.6 — never trusts a held socket) |
| Backend — `internal/api` | `/v1/*` | Bearer JWT middleware | SSE endpoints: prompts stream, metro, usage |

**Design law (PHASE_17 §2.3, permanent):** the companion app never shows code,
file trees, diffs, or terminal — chat, plan approval, status headlines,
notifications, handoff only. Any new endpoint that exposes code content stays
web-only; any new status event must map to a *headline* on mobile, never a
raw trace verb.

## Conventions for every W-series change

1. **New endpoint** → typed wrapper in `frontend/lib/api.ts` (web first); a
   mobile wrapper only if it serves chat / plan / status / notification /
   handoff. Same route + shape for both — one backend surface, two mirrors.
2. **New SSE/event kind** → document it here with the payload shape; the web
   trace renders a chip, mobile folds it into a headline status.
3. **Flag-gated behavior** → note the flag and the *client-visible difference*
   (what changes when it flips).
4. **Auth** → JWT stays in headers; the only URL-token pattern is EventSource's
   `?token=` (SSE cannot set headers) — new streams follow that same pattern
   and nothing else does.
5. **Never** change a response shape without grepping both `lib/api.ts` mirrors
   and `frontend/lib/types.ts`.

---

## Phase log

### W0 — the gate is green (2026-09-22)

- **`backend/internal/ai/client.go`** — `WithModel`/`WithRetryHook` no longer
  struct-copy the `Client` (which copied `sync.Mutex` — the copylocks vet error
  that had `make evals` red since `75c6ef8`). New `clone()` builds the copy
  field-by-field and snapshots `lastErr`/`lastErrAt` *under the lock* (the old
  copy read them with no lock — a latent race the fix also closes). The comment
  above `WithModel` ("never struct-copied") is now true; comment = contract.
  *Client impact: none — same credentials/workspace/pacer/hook sharing, same
  health semantics on `readyz`.*
- **`backend/internal/evals/phases_test.go`** — `TestPhaseW0GateGreen` runs
  `go vet ./internal/ai` from inside the suite, so a reintroduced copylock
  fails a bare `go test ./...` too, not just the Makefile's `vet` target.
- **`.github/workflows/ci.yml`** — new **Tree guard** step
  (`git diff --exit-code` after build/vet/test): the gate itself may never
  modify a tracked file. This is the mechanical answer to "don't break
  anything": drift gets caught in CI, at the gate, not at review time.
- **`PLAN_HARNESS.md`** — errata header: the "all green" status was false at
  audit time; phase laws that landed dark are itemized with pointers here.
- *No API surface change → no frontend/mobile work in this phase.*

### W1.4 — run_gate registered (2026-09-22)

- **The break:** `run_gate` (the verify-chain report tool) was defined, unit-
  tested, and documented since the G4 ladder — but never landed in any
  registry, so the model asking "what did the verify chain report?" always got
  "No tool named run_gate".
- **`tools/verify.go`** — empty accumulator now renders the honest "no chain
  run yet" (the accumulator is non-nil from construction; the old nil check
  would have claimed "chain pass: empty" for gates that never ran).
  **`tools/readonly.go`** — `RunGate` classifies read-only (concurrent with
  other reads).
- **`orchestrator/orchestrator.go`** — exported `LastVerifyReport()`; fixed
  the latent verdict bug wiring exposed: `resetVerifyReport` zeroed
  `OverallPassed` while `New` initializes it true, and `appendVerifyGate`
  multiplies the carried verdict — after the first chain every report would
  have read "fail" even when all gates passed. Nothing read it until now.
- **`api/buildsession.go`** — registered with a provider closure over
  `h.orch`, nil-guarded (the testinfo handler shape has no orchestrator).
- **Tests:** `orchestrator/verify_report_test.go` (lifecycle: fail sticks /
  reset restores the passing baseline / unavailable never flips / late fail
  flips), `api/run_gate_wiring_test.go` (registration, model-visible def,
  read-only class, both handler shapes).
- *Client impact: none — no endpoint or event changed. The tool changes what
  the model can ask mid-build ("what's green?"), surfacing as an ordinary
  tool result in the web trace; mobile's headline status is unchanged.*

### W1.2 — spill read-back (2026-09-22)

- **The break:** H8 files oversized tool outputs to `backend/spill/<hash>.txt`
  and replaces the content with a locator line telling the model to "re-read
  with read_file on that path" — but `read_file` resolved paths only inside
  the workspace root, and the spill dir sits beside the database, outside it.
  Retrieval was one-way: the body was written and unreachable.
- **`harness/compaction`** — new `ResolvePath(candidate)`: the locator line
  the model saw *or* the bare path resolves, both through one `inStore` check
  (lexical containment in the store dir + the `.txt` name `Spill` writes).
  `/etc/passwd`, `..` hops, and wrong suffixes never resolve — a locator is a
  pointer at one filed output, not an arbitrary file-read token.
  `LocatorPath` now shares the same check.
- **`tools/read.go`** — `ReadFile` gains an optional `Spill SpillResolver`
  (interface: tools stays dependency-free; `SpillStore` satisfies it).
  Resolved bodies are read with `os.ReadFile` — outside the workspace root
  *and* outside `FS.Read`'s 256 KB cap, which is exactly why the output
  spilled — then rendered through the shared line-window helper, so
  `offset`/`limit` paging works on a spilled body. Same schema (no tool-cache
  churn beyond the one-time description update).
- **Wiring:** `PromptHandler.AttachSpill` (house `Set*`/`Attach*` pattern);
  `main.go` attaches the *same* store the orchestrator spills through;
  `newBuildSession` passes it to `read_file` (child sessions included — they
  share `newBuildSession`).
- **Decision recorded:** H8's spec phrase was `read_spill(locator)`, but every
  repository artifact — the compaction README, the locator replacement text,
  `LocatorPath`'s "the read tools resolve locators through this" — names
  `read_file`. Resolution landed through `read_file` (no new tool name, no
  catalog churn, the replacement text the model holds stays true), paging is
  line-based via `offset`/`limit` (spill bodies are line-oriented tool
  output). A separate `read_spill` tool would have contradicted the instruction
  the model is currently following.
- **Client impact: none** — tool results only. Web trace shows the read-back
  like any file read; mobile turn status unchanged.
- **Tests:** `compaction/TestResolvePath` (locator / wrapped / bare / outside
  / traversal / wrong-suffix), `tools/TestReadFileResolvesSpillLocators`
  (verbatim window, paging, past-end names the filed path, nil resolver
  unchanged), `evals/TestPhaseW1SpillReadBack` (full chain: Spill → locator →
  read_file → line window with true total; outside path does not leak).

### W1.3 — conversation search → FTS5 ∪ LIKE (2026-09-22)

- **The break:** audit row 6 — `SearchConversationBlocks` was `LIKE '%q%'`
  over the raw `blocks` JSON: substring-only, so a search for "running"
  never found "runs"-shaped queries the other way (no stemming), and every
  keystroke pattern scanned full JSON per project. The memory table had had
  FTS5 since P1b (`project_memory_fts`); conversation never got it.
- **`store/store.go` migrate** — `conversation_fts`, an *external-content*
  FTS5 (`content='conversation_messages'`, `content_rowid='id'`,
  `tokenize='porter unicode61'`) mirroring the memory pattern: three triggers
  (ai/ad/au) keep the index in lockstep with append/trim/clear, and a
  `rebuild` backfills rows that predate it. Indexing `blocks` (the same bytes
  the LIKE leg matches) means both legs stay in parity by construction.
- **`store/conversation.go`** — `SearchConversationBlocks` now runs *two*
  legs behind the one signature and unions them deduped by id:
  - **LIKE leg (kept as fallback):** escaped literal substring — "uild" finds
    "build", "100%" stays literal. Runs first; if it errors, the call fails.
  - **FTS leg:** phrase-quoted MATCH (user operators `AND/OR/*` are searched
    for, not parsed); porter stemming means "runs" finds "running". If this
    leg errors — index missing, q tokenizes to nothing — it's simply dropped:
    search degrades to the LIKE result, never fails.
  - Union takes the newest `limit` matches, returns them in turn order
    (same window/order the LIKE-only query had), `nil` on no hits.
- **Handler (`api/history.go`):** comment updated (stem + substring recall,
  both literal-metachar-safe); endpoint, request, and response shape
  unchanged — web History drawer and mobile History screen need no change.
- **Why union, not switch:** the plan (W1 work item 3) says "LIKE path
  retained as fallback behind the same handler" — a pure FTS swap would lose
  mid-token substring hits users already depend on (the wildcard-escape test
  is the contract), while pure LIKE keeps the audit's "not FTS" finding open.
  Union closes the finding and keeps the old contract as a subset.
- **Tests:** `store/TestSearchConversationBlocksFTSUnion` (FTS-only stem hit,
  LIKE-only substring hit, `100%` literal, dedup of a both-legs match, window
  + turn order), `evals/TestPhaseW1SearchFTS` — the plan's eval gate
  ("FTS query matches LIKE results on fixture"): ground truth is the old LIKE
  path replayed in Go over the same rows; union must equal it exactly on
  plain-word queries, plus stem/substring/wildcard class probes.

### W1.1 — trim → shadow (2026-09-22)

- **The break:** audit row 4, both halves. Every turn finalize ran
  `TrimConversation(400)`: INSERT the overflow into `conversation_archive`,
  then **DELETE** it from `conversation_messages` — the oldest turns past the
  newest-400 window were hard-deleted from the only table any reader touches,
  and the archive had *no* reader (search/API/derive never opened it). The web
  History drawer and mobile History screen therefore silently stopped at the
  trim point: truth destroyed, replay hazard on every reload (H1 law,
  `PLAN_HARNESS.md:256-258`).
- **`store/conversation.go`** — `TrimConversation` keeps the name, changes the
  semantics (per H1: "keep the name, change the semantics to shadowing"). New
  signature `(lg *sessionlog.Log, projectID, keep)`, nil-safe, **deletes
  nothing**: it calls `Log.Retain` and, only if a marker actually landed,
  writes a `trim_conversation` audit row (`target_id`=keep,
  `detail`=`keep=%d boundary=%d`).
- **`sessionlog/log.go`** — new `Retain(keep, summary, reason) (boundary,
  err)`, the retention counterpart to `Shadow` (compaction already wrote
  shadows): scans `Replay()` backward counting User/AssistantMessage rows,
  boundary = seq of the row just before the oldest kept message, then
  `Shadow(boundary, …)`. Returns `0, nil` — no marker — when nothing falls out
  of the window or the existing shadow already covers the boundary, so a
  repeated trim is idempotent (no duplicate markers, no second audit row).
  `Replay()` is untouched by shadowing; only `DeriveMessages` skips shadowed
  rows.
- **Flag:** `TALONSTUDIO_TRIM_SHADOW` (documented in `.env.example` under
  "Harness flags — all ship dark") gates **only the shadow-marker write**.
  Delete-removal and archive recovery ship live, so row 4 closes even with the
  flag dark. Flag dark → trim is a complete no-op: no marker, no audit, full
  transcript stays model-visible (the pre-W1.1 visibility, minus the deletes).
- **`store/store.go` migrate** — P2a retirement: the `conversation_archive`
  CREATE/index block is replaced by a boot-time recovery, run after the audit
  indexes (the purge needs `audit_log`): rows whose project has a newer
  `clear_conversation` audit are **purged** (a user wipe wins — recovery never
  resurrects destroyed history), the rest are `INSERT OR IGNORE … WHERE NOT
  EXISTS (project_id, seq)` back into `conversation_messages` — the write-only
  archive finally gains a reader — and the table is `DROP`ped only if both
  steps succeeded. Any error logs "will retry next boot" and boot continues:
  crash-idempotent (`OR IGNORE` + `NOT EXISTS`), and the next boot re-runs it.
- **Call sites (`api/prompts.go`):** `finalizeTurn` (`:1257`) and
  `persistPlanExchange` (`:1366`) pass the project's log from the existing
  `h.slog` cache; stale "trim deletes" comments replaced with the truth.
  `historyTurns = 60` untouched — rendering window only (plan: *"`historyTurns`
  window applies to rendering, not to truth"*).
- **Client impact:** the **History endpoints for web and mobile now always
  return the complete transcript** — turns older than the newest 400, deleted
  from storage before this change, are back (recovered from legacy archives,
  never deleted again). No endpoint, request, or response shape changed → no
  `lib/api.ts` / mobile wrapper work. When the flag flips on, the only
  client-visible difference is model-side: derivation mid-thread starts from a
  retention summary ("Older turns retained…") instead of full history — an
  ordinary message in the web trace; mobile headline status unchanged.
- **Tests:** `store/TestTrimConversationShadowsInsteadOfDeleting` (5 rows
  survive a keep=2 trim, Replay=7 = 5+shadow+summary, Derive=3 = summary+2,
  re-trim idempotent, exactly one audit row), `TestTrimConversationFlagDarkNoop`
  (no marker / no audit / derivation unbounded), `TestMigrateRecoversArchive`
  (seq order restored, table dropped, second boot a no-op),
  `TestMigratePurgesClearedArchive` (wipe outranks recovery; table still
  retires), `evals/TestPhaseW1TrimShadow` — the plan's eval gate
  (*"re-derive after trim reproduces full history"*): every original log event
  survives byte-identical (+ exactly 2 additive markers), storage keeps every
  row, derivation = summary + keep.

### W1.5 — `delegate_fork` + `sessionlog.Fork` wiring (2026-09-22)

- **The break (register row 8, W1.5 half):** `sessionlog.Log.Fork` and
  `Store.SessionLogForked` were fully implemented and unit-tested but had
  **zero production callers**; H9's second model-facing tool `delegate_fork`
  ("child seeded with parent history prefix — continue this analysis") didn't
  exist at all; no lineage event kind existed. The child-side plumbing
  (`Spec.Fork`, `childRunner.forkHistory`) was dead code nothing could set.
- **`harness/events`** — new audit-only kind `SessionForked`
  (`session/forked`, non-surface by default: lineage never becomes a
  model-visible message) + `ForkPayload{ParentKey, ChildKey, FromSeq, Label}`,
  appended to **both** logs — the parent records the child it seeded, the
  child records the parent it came from, so either side reconstructs the
  lineage.
- **`tools/delegate.go`** — new `DelegateFork` (`delegate_fork`): same
  report-framed contract as `delegate`; `task` is **optional** and defaults
  to `subagent.DefaultForkTask` (one const shared by tool + runner, so a
  fork's prompt is never empty); spawns with `Spec.Fork=true` and
  `ParentID=LabelPrefix` for lineage. Registered beside `delegate` in
  `buildsession.go` → inherits the existing `TALONSTUDIO_SUBAGENTS` opt-in
  (plan flag policy: *pure additions ship live* — no new flag; now documented
  in `.env.example`).
- **`api/subagent.go`** — `forkHistory` changed from a static slice to
  `func() []ai.Message` resolved **per spawn** = `h.conversation(projectID)`:
  exactly the history the parent's next turn replays (derive-preferring with
  legacy fallback → child prefix == parent prefix under either flag). A fork
  now also calls `st.SessionLogForked(parentLog, key, fromSeq)` (child key =
  `<parentKey>-fork-<unixnano>`; `fromSeq` = parent's last event seq via
  `EventAt(Seq()-1)`): the child log = **byte-identical parent prefix** +
  lineage + task; the final report is appended on success → the child log is
  a complete, replayable delegation record (prefix, lineage, task, report) —
  not a write-only artifact. Every log failure logs and continues (never
  fails the run — same law as `deriveFromLog`). `runLoop` became a field, so
  its "seam tests override" comment is finally true; `Log.Key()` accessor
  added.
- **Decisions recorded:** (1) children keep the read-only registry
  (`childRunnerFor`'s established design — "inspect and report; never
  mutate"), which satisfies H9's gate *"cites no tools the parent lacks"*;
  H9's looser "shares parent composition" phrase is noted for the W12 /
  disjoint-write review. (2) The fork prefix comes from `h.conversation()`,
  not child-log derive: `SESSIONLOG=derive` ships dark (row 11 / W2), so the
  parent's own read path is the only source guaranteed identical on both
  sides until W2's parity harness flips it. (3) `session_events.seq` is a
  global `AUTOINCREMENT` across log keys → a forked child's new rows always
  land **above** the inherited shadow boundary (proven: the task is
  derivation-visible even under an inherited compaction shadow).
- **Client impact:** none — model-facing tool only; no endpoints, no SSE
  kinds, no `lib/api.ts` / mobile wrapper work. The child's report arrives in
  the web trace exactly like `delegate`'s (framed block); mobile headline
  status unchanged (PHASE_17 §2.3 — no raw trace verbs).
- **Tests:** `api/TestDelegateForkSeedsChildFromParentPrefix` — the H9 gate:
  first request = parent prefix (DeepEqual) + task; tool set ⊆ parent's
  (`shell` filtered); parent log gains exactly one lineage event with the
  right `FromSeq`; child log = byte-identical prefix + lineage + task +
  report; task derivation-visible under inherited shadow; non-fork spawns
  seed nothing and log nothing. `api/TestDelegateForkRegistered` — present in
  reg/defs with `SUBAGENTS=1`, absent when dark. `evals/TestPhaseW1DelegateFork`
  — tool contract: `Fork` set, `DefaultForkTask` on omitted task, lineage
  `ParentID`, report framed as data, nil-service structured refusal,
  `SurfaceKind(SessionForked)==false`.

---

## W1.6 — Approval Seam wired live (H4 tier-2 Escalate + sensitive HTTP)

**What shipped.** The H4 approval seam and `sandbox.Capability.Escalate` were
complete but dead (register row 9): zero production callers, and `run_shell`
tier-2 was ask-only — the refusal told the model to "ask the user and run it
only after they approve", but no approval could ever unlock the tool (the
approval spin's cousin: an ask that led nowhere). W1.6 wires it:

- **Tier-2 shell → Escalate.** `RunShell` gained `Sandbox`/`Approval`/
  `PromptID`; the `tierApproval` case now calls `escalateTier2`, which asks
  `Capability.Escalate(workspace → danger, Operation{shell, command},
  justification)` against the turn's seam. Outcomes: `AllowedOnce` → the
  command runs (exactly this command, once); `Rejected` → a final `REFUSED`
  (denial is final per the escalation contract — no retry-with-new-reason
  ladders); `Unavailable` (nil seam, nobody asked, nobody answered yet) →
  the pre-existing `APPROVAL REQUIRED … ask_user` fallback, verbatim — the
  model-visible question stays the delivery mechanism. `Escalate` refuses
  unjustified requests, so an empty classifier reason falls back to
  "outside the always-allowed shell set".
- **Consent flow closes the loop.** `WantsDeviceBuild` only matches build
  words, so a user answering *"yes, run git push"* recorded no
  `turn_approvals` row and the seam could only ever answer "not yet". Two
  scanners now sit in `orchestrator`: `WantsApproval` (affirmative consent,
  negation-outranks) and `AnswerNegated`; the answer flow grants the new
  `store.ScopeTier2Shell` on an affirmative answer, and the seam's answerer
  (`turnAnswerer`, renamed from `deviceBuildAnswerer`) reads **scope by
  request family**: `sandbox-escalation:*` → `tier2_shell`, everything else
  → `device_build`. A build yes stays a build unlock; a command yes unlocks
  exactly the surfaced command's escalation for this turn. The device-build
  grant also gained `&& !AnswerNegated(...)` — *"don't install it"* contains
  the word install and previously recorded a grant (adjacent hole, same
  binding-record contract, fixed while in the file).
- **Sensitive HTTP actions audit through the same seam.** `GitHandler.Push`
  (between repo-name resolution and `EnsureRepo`, so a refusal never creates
  a repo) and `ExportHandler.Create` (which mints/bundles signing
  keystores) each make one `approveUserAction` request — scopes
  `git-push`/`release-export`, one `ApprovalDecided` event to the project's
  session log, `403` if the seam cannot answer (fail closed, never
  unseamed). The authenticated POST is the consent (`userInitiatedAnswerer`),
  matching the plan's framing: these were "sensitive by convention only" —
  now they are sensitive by seam + audit. Keystores have no separate
  endpoint; the release export path *is* the keystore-touching action.
- **Audit text names the command.** `escalation.go`'s action formatter used
  `Operation.Path` (empty for shell) → records read *"shell  — reason"*; it
  now renders the command: *"shell git push origin main — outside the
  always-allowed set"* — what an operator needs to judge a grant.
- **Root cause of "seam dead" found and fixed.** `newBuildSession`
  accumulated `sandbox`, `promptID`, `seam`, `children` on a local
  `buildSession` but returned a **fresh struct literal** with only
  `{reg, fs, defs, projectID, store}` — so the executor's H4 outer ring
  (`b.sandbox != nil` at `buildsession.go:477`) never ran, build receipts
  were attributed to prompt 0 (guard `b.promptID != 0` always false — the
  "every build_apk completion is filed durably" feature was dark), and the
  session carried no seam. Tool registrations copied their own field values
  before the return, which is why delegation and (now) tier-2 worked while
  the *session* stayed nil. Fixed by returning `bs` with `defs` set; the
  diff comment records the find.

**How it connects.**
- *Web app:* no endpoint or SSE-shape changes. The user-visible flow is the
  ask_user card they already answer (the answer endpoint they already call
  now also records `tier2_shell` consent — same POST, one more
  `GrantApproval`); `run_shell` results stream through the existing tool
  result events. Git push / release export requests keep their contract —
  403 only when the seam is unavailable (never under `PolicyAsk`, the
  hardcoded default). Audit trail = `ApprovalDecided` events already in the
  project session log (the same stream `GET …/sessions` replays).
- *Companion mobile app:* no wrapper work (`lib/api.ts` untouched). Mobile
  answers the approval prompt through the same answer endpoint; headline
  statuses unchanged (PHASE_17 §2.3 — no new raw trace verbs; the refusal/
  grant text rides ordinary tool-result content).
- *Harness:* `tools → harness/{approval,sandbox}` imports follow the
  existing `tools → harness/subagent` precedent; the seam is per-turn
  (built in `newBuildSession`), audited via `approvalAudit` → session log.

**Decisions recorded.** (1) **Ships live, no new flag** — consistent with
W1.2–W1.5 (behavior fixes, not opt-in surfaces); the plan names no flag for
W1.6 and `PolicyAsk` is the established default (W9 owns modes/policy
composition). (2) Turn-wide consent is by plan design ("`turn_approvals`
stays the binding record"): a yes is not consumed per action — each
sensitive act still *asks* and audits individually; the grant scopes which
*class* of act it can answer. (3) `PolicyNever` → deterministic refusal
without prompting is already seam-native (eval-gated below); no policy env
yet — W9. (4) No success-path handler test for `Create`/`Push`: one would
spawn the async export goroutine; the shared helper is gate-tested
(`TestApproveUserActionAuditsOnce`) and the handlers are 4-line
pass-throughs — noted residual. (5) Observed pre-existing flake, unrelated
to this diff: `TestClaimAttachmentsSpendsOnce` TempDir cleanup race under
full-package parallel load (6/6 isolated, full package green).

**Tests / gates (all green, local = CI-equivalent):** package tests →
`make evals` → `go test -race ./...` (41 pkts). New:
`evals/TestPhaseW1SeamWiring` — the plan gate: granted escalation →
exactly **one** audit record/event + `AskedCount==1` + action names the
command; unanswered → one `Unavailable` event; `PolicyNever` → refuses
without prompting (asked=false), one event. `api/TestNewBuildSessionWiresTier2Seam`
(registration wiring + nil-slog bare-handler regression),
`api/TestTier2ShellGrantFlowAuditsOnce` (unanswered → ask_user fallback +
1 event; grant → retry runs + exactly 1 new event, escalation-scoped,
prompt-attributed),
`api/TestApproveUserActionAuditsOnce` (push + export → 1 event each),
`tools/TestRunShellTier2SeamOutcomes` (granted executes / denied final /
unavailable falls back — one audit record each, command named),
`orchestrator/TestWantsApproval` + `TestAnswerNegated`, existing
`TestRunShellRequiresApproval` (bare tool = fallback) still green.

**For W9 (audit-register follow-through):** W9:434 claims
`GET /v1/projects/{projectID}/audit` "already serves it" — **no such route
exists** in `internal/api` today (grep `/audit` finds only `approvalAudit`
callers). W9's UI-trail work must build that endpoint (session log already
carries every `ApprovalDecided` event it needs). Also noted for W9: the
device answer-flow's negation guard is now in place, and scopes are the
seam's matching axis (`device_build` | `tier2_shell` | `sandbox-escalation:*`
| `git-push` | `release-export`).

---

## W1.8 — Spill retention audit (document-only, no finding)

**What was audited.** `PLAN_WORLDCLASS:251-252` / H8 risk: workspace
cleanup + export paths must exclude `backend/spill` (oversized tool
outputs can be hundreds of KB and outlive the content that produced them).
Read the four relevant trees in code; **no code change — audit passes.**

**Findings (all verified, cited):**
- *Spill placement:* `cmd/server/main.go:163` roots the store at
  `filepath.Join(filepath.Dir(dbPath), "spill")` — a **sibling of the
  database**, outside `workspacesDir`. (H8's prose says "under the
  project"; the implementation's datadir-sibling placement is the safer
  choice for this audit — see below.)
- *Cleanup paths* never touch it: `workspace.Manager.Create`/`Destroy`
  (`workspace.go:95,213`) `RemoveAll` only `<workspacesDir>/<projectID>`;
  the project file-delete endpoint (`projects.go:343`) resolves through
  `resolveInside` (`projects.go:407+`), which rejects absolute paths, NUL,
  and any escape from the project dir; project delete
  (`projects.go:389-395`) calls `store.DeleteProject` + `workspace.Destroy`
  only. Release-build scratch (`release/build.go`) and the devclient build
  dir have their own roots.
- *Export paths* never walk into it: `writeBundle` (`export.go:166`)
  assembles an explicit `SubmissionBundle{APK, shots/icon/banner,
  whats-new}` — no directory walk; `GetFiles` (`projects.go:185`) walks
  only `projectDir`; `release/build.go:448` walks only the release
  scratch source.
- *Structural guarantee:* zero `spill` references in `workspace/`,
  `release/`, or `export.go` — cleanup/export do not know spill exists;
  the trees are disjoint by construction (`<workspacesDir>/<id>` and
  `<datadir>/<db>` vs `<datadir>/spill`).

**Retention note (not a finding, for a future sweep):** `SpillStore` is a
global content-hash dedupe (`compaction.go:64` — no per-project key), so
project deletion *cannot* selectively purge spill files and today does not
attempt to; there is no size/age GC either. Orphaned spill files therefore
accumulate for the life of the datadir. Harmless for correctness (the
audit's requirement is exclusion, verified above) but a size-capped or
age-based GC over `<datadir>/spill` would complete the retention story —
candidate for a later H8/W14 follow-through, not scheduled work.

**Status:** register row 39 ✅. No tests/gates — nothing changed; the W1.6
gates (package tests, `make evals`, `-race`) remain the last green run on
this tree.

---

## W2 — Read-path parity harness (W2.1 + W2.4); flip/pagination deferred

**What shipped.** `PLAN_WORLDCLASS:264-266` asks for a parity harness proving
`deriveFromLog()` ≡ legacy `GetConversation` message-for-message across
normal / interrupted / answered / compacted / trimmed transcripts, plus
H1's replay-determinism gate promoted permanently (`make evals` runs it —
it lives in the api package's standard test set). `api/TestPhaseW2ReadParity`
is that gate: seven subtests, all green.

**How the fixtures stay honest.** Every message is written through the
production dual-write pair (one `conversation_messages` row via
`store.AppendMessage` + the same `ai.Message` through `appendLoggedUser`/
`appendLoggedAssistant` — the exact `runLoop Persist` sequence at
`prompts.go:1020-1035`), interrupted/plan rows go through the real
`persistTurnEnd` / `persistPlanExchange`, and **both reads run the production
read function** (`storedConversation`) in its two flag states
(`TALONSTUDIO_SESSIONLOG` legacy vs derive) — the literal pre/post-flip
model histories. Shadow shapes compare the post-boundary window under the
summary head (the projection's honest form of the same history); a pairing
walk asserts every `tool_result` references an earlier `tool_use`.

**The harness found 4 real projection gaps — all closed:**
1. **`turn_end` marker fields** (status/error/rounds): `events.BlockPayload`
   had no fields for them, so the terminal row projected as a bare
   `{"type":"turn_end"}` — after the flip a cancelled/failed turn would read
   as alive. Fixed by extending `BlockPayload` + `DerivedBlock` + both
   mapping sites (omitempty keeps every existing log byte identical).
2. **Plan block dropped on read**: `deriveToAIMessages` never copied
   `Plan` (the bridge test's "plan blocks preserved" claim asserted only the
   text block). Plan cards would silently rebuild empty post-flip.
3. **`tool_result` name dropped in projection**: `ToolResultPayload` stored
   `Name` all along; `DeriveMessages`' ToolResult case discarded it —
   projection-only fix, retroactive for all existing rows.
4. **Doc attachment refs dropped on write**: `appendLoggedUser` copied
   attachment refs only for `Type == "image"`; doc blocks lost id/name/mime
   in the log. Now any block carrying an attachment id rides by reference.

**Honest caveat (for the W2.2 flip review):** fixes 1, 2, 3 are
retroactive (fields were always in the legacy row or payload); fix 4 and the
turn_end fields are **write-side schema evolution** — log rows written
*before* this commit never carried them, so those specific historical turns
project without them until they age out of the 60-turn window or a shadow.
Bounded, UI restore still reads the legacy table until W2.3, and the flip
itself is deferred.

**W2.4:** `repairOnLoad` (`log.go:74`, comment at the field) is now named
the derivation integrity gate — load-time `Repair()` closes an interrupted
tail *before* anyone derives, so neither read path can replay an unanswered
`tool_use` after a crash. Already correct; documented per plan.

**Not done (deferred, recorded in row 11):** **W2.2** default flip — the
plan itself gates it on "two green CI weeks" (`:273`); it is a deployment
act with `TALONSTUDIO_SESSIONLOG=legacy` as the documented rollback, not
something to do silently mid-session. **W2.3** derived pagination
(log-backed `before_seq`, jump-to-turn, search reading the same projection)
is the next slice — it is what makes the flip safe for the *UI* read path
(HTTP `GetConversation` still reads the legacy table today).

**Connection:** no endpoint/SSE changes; production behavior today is
unchanged (flag dark → both the old and the fixed projection are reachable
only under `SESSIONLOG=derive`); web/mobile need no client work — restore
and search keep reading legacy until W2.3 flips them with the same gate.

**Gates (all green, local = CI):** package tests (21 pkts) → `make evals`
→ `go test -race ./...` (41 pkts).

---

## W3.A — plan-with-eyes: bounded read-only tool loop (committed `TALONSTUDIO_PLAN_TOOLS=v1`)

**Plan:** `PLAN_WORLDCLASS.md` W3.1 (project map + `Plan` reads code) + W3.3 (round cap); register row 12 (partially).

**Design (new file `backend/internal/api/planloop.go`):**
- **Flag:** `planToolsEnabled()` — same truthy-switch as `subagentsEnabled`; `.env.example` documents `TALONSTUDIO_PLAN_TOOLS=v1`. Dark in production → both plan doors byte-identical (existing `TestPlanModeSendsNoToolsToModel` keeps the wire assertion).
- **Registry:** `buildPlanToolRegistry()` — a *filtered copy* of `tools.All()` (never mutates the shared registry) keeping exactly `explore, find_files, grep_codebase, list_files, project_map, read_file`, asserted read-only via `tools.IsReadOnly` (gate: `TestPhaseW3PlanRegistryReadOnly`). This is the "Plan is not allowed to edit code" boundary.
- **Loop (`runPlanToolLoop`):** messages start from `planMessages`; each round = one `client.Send` with `client.Tools(registry)`. `stop_reason == tool_use` → execute each tool block through `registry.Execute` (failures become `is_error` tool results, the conversation stays valid) → append assistant + tool-result blocks → next round. Rounds capped at `maxPlanRounds = 8`; after the cap the next Send is forced tool-less, so the model must return `text` (gate: `TestPhaseW3PlanRoundCap` — 9 calls total, finalize has `tools == nil`).
- **Answer/doors:** first tool-less `text` becomes the answer. The streaming door passes it through the *new* `ai.ParsePlan` (thin exported wrapper over the private `parsePlan`), so plan JSON extraction/normalization stays single-sourced; the non-stream door calls it for its envelope check. Loop returns `(raw, env, err)`; callers branch on `err == nil && env != nil`.
- **Fallback (`planFallback`):** any non-cancellation failure (e.g. provider 400s tools) or a missing workspace path reverts to the *original single-shot `ai.Plan`* (gate: `TestPhaseW3PlanToollessFallback` — provider-rejects-tools → 2 calls; no-workspace → 1 tool-less call). The tool-less path is byte-equivalent to the old wire (`TestPlanModeSendsNoToolsToModel`).
- **Both doors:** flag-on `sawError`-style terminals mirror the existing streamPlan error handling (error SSE + `UpdatePromptResponse` + `cancelled/failed` + done error); `persistPlanExchange`/`savePlan`/`dispatchNotification` tail unchanged.

**Gates (all green, uncommitted at time of writing → commit `W3.1/W3.3: …`):** `gofmt`/`go vet` clean; package tests (api, ai); `TestPhaseW3PlanTurnSeesFixtureRepo` (real loop over a fixture workspace, marker `W3_FIXTURE_MARKER` only reachable via `read_file` content); registry/cap/fallback/stream-doors gates; `make evals`; `go test -race ./...` (41 ok).

**Caveat:** same class as W2 historical rows — this only affects *new* plan turns behind the flag; old plan rows are untouched (no projection change).

**Web/mobile connection:** none required — plan turns are server-computed; the flag is a harness/env concern. When W3.2 (map/AGENTS injection) lands, the web/mobile plan view may surface richer `readiness` text, but no client change is required for W3.A itself.

**Remaining in W3:** W3.2 (inject project map + fingerprint + AGENTS.md chain into `planPrompt`), W3.4 (plan artifact `.talon/plan.md` + checklist sync, register row 13), W3.5 (stale-fingerprint Explore survey via `ClassifyTurn`).

---

## W3.2 + W3.5 — per-project plan prompt + stale-fingerprint Explore survey (committed, same `TALONSTUDIO_PLAN_TOOLS=v1`)

**Plan:** `PLAN_WORLDCLASS.md` W3.2 (inject project map + fingerprint + AGENTS.md chain into `planPrompt`) and W3.5 (cheap-model survey when the map fingerprint is stale); register row 12 now fully closed (artifact stays row 13).

**W3.2 — prompt assembly (`planSystemPrompt` in `planloop.go`):**
- Both plan doors (Send + stream, incl. the Answer door's inherited path) now call `h.planSystemPrompt(ctx, projectID)` instead of the static `h.planPrompt`. Flag off → first branch returns `h.planPrompt` untouched: no workspace walk, no cache, no model → **byte-identical wire** (pinned by `TestPlanModeSendsNoToolsToModel`).
- Flag on → the prompt is a `promptsec` registry (H7, same package Build mode uses) with four sections in weighted order: `plan/head` (the static `PlanSystemPrompt`, persona weight), `project/map` (`tools.ProjectMapText(workspace)`), `project/survey` (W3.5 digest, custom weight 550 — between map 500 and memory 600), `instructions/chain` (AGENTS.md). Each dynamic section renders **once** and the closure captures the text, so `Assemble()` and `Fingerprints()` agree byte-for-byte and neither walks the tree twice. Empty renders are omitted by promptsec (absence = never registered).
- **AGENTS chain reuse:** `main` attaches `promptHandler.AttachInstructionsChain(...)` — the *same closure* it hands `orchestratorSvc.AttachInstructions` (workspace root + `$TALON_HOME/AGENTS.md` via `instructions.Loader`), so plan and build cannot drift on conventions. Unattached (tests/embedded) → workspace-only loader, no global file.
- **Fingerprint discipline:** `notePlanSections` stores each project's `promptsec.Fingerprints()` set and logs `promptsec.Diff(prev, next)` when a section moves — the observability half of the cache law (a late change leaves the head/map hashes intact, so a cached prefix stays provably valid). Gate: `TestPhaseW3PlanPromptSectionLocality` (survey change moves only `project/survey`; an AGENTS.md edit moves only `instructions/chain`).

**W3.5 — Explore survey (`planSurveyDigest` + `exploreSurvey`):**
- Staleness unit = `tools.ProjectFingerprint(workspace)` (the cheap mtime/size hash that "walks the same file set ProjectMapText renders"). Cache per project: `{fingerprint, digest}` of the last **successful** survey. Matching fingerprint → digest reused with zero model calls; changed/never-taken → survey runs *before drafting* (it happens inside prompt assembly, so the loop's first round already carries it).
- The survey = one bounded loop (`maxSurveyRounds = 3` + tool-less finalize) over a single-tool `{explore}` registry, on the **cheap tier**: `ai.LoadRoutingPolicy().Models[TierMedium]` (compiled default maps it to `AI_MODEL_CHEAP` — the same cheap path `ClassifyTurn`'s small-edit branch serves) via `h.ai.WithModel(model)`; equal-model deployments skip the copy. Digest is plain text by prompt instruction (it lands verbatim in the system prompt).
- **Failure semantics:** provider error → log + return "" (no digest) → **not cached**, next plan turn retries; the turn itself never waits on survey success beyond the one attempt. Gate: `TestPhaseW3PlanSurveyFailureDegrades` (400 fast-fail → head+map only, attempt count 1 then 2 proves no negative-cache). Gate: `TestPhaseW3PlanSurveyWhenStale` (1st call surveys once, 2nd call byte-identical prompt + no re-survey, file-size change → 2nd survey).
- `.env.example` gained the previously-undocumented `AI_MODEL_CHEAP` line (W3.5 consumes it; also partial W8.3 surface).

**Fixture mechanics (for future edits):** `newPlanFixture` now sets `AI_MODEL_CHEAP=survey-model` and the stub answers `model == "survey-model"` requests *apart* from plan traffic (`surveyBodies` / `f.surveyCount()`), so every existing plan call-count assertion still counts plan rounds only. `f.surveyRespond` is overridable per test (used by the failure gate). Default survey reply marker: `W3_SURVEY_DIGEST`.

**Caveat (honest):** the W3.A-era assertion `sys == planPrompt` / `strings.Contains(sys, "src/app.ts")` was *silently satisfied by the wrong section* once injection landed (the AGENTS rule and survey digest both name `src/app.ts`; the map itself renders as a `src/` header + `app.ts` line). All three assertions were re-aimed at the map's fixed header `## Current project structure`, and the wire gate now additionally pins the digest (`W3_SURVEY_DIGEST`) in a real door turn.

**Gates:** `gofmt`/`go vet` clean (api, cmd/server); 10 W3 tests + flag-off regression green fast; `make evals` EXIT=0; `go test -race ./...` EXIT=0 (41 ok).

**Web/mobile connection:** none required — plan turns remain server-computed; injection enriches the system prompt only. The web/mobile plan view *may* surface richer text (map-grounded plans cite real files — already visible in the plan JSON `features`), but no client work is implied by W3.2/W3.5. Flag stays dark in production → zero behavior change.

---

## W3.4 — `.talon/plan.md` artifact + two-way checklist sync (committed, behind `TALONSTUDIO_PLAN_TOOLS=v1`)

**Plan:** `PLAN_WORLDCLASS.md` W3.4 — the plan becomes a durable, user-editable file in the workspace; register row 13 closed, **W3 fully landed**.

**New package `internal/planfile`** (imported by api + orchestrator; imports only `ai` — no cycles):
- `RelPath = ".talon/plan.md"` beside skills/plugin pins. `Enabled()` reads the same env var with the *same* truthy set as `api.planToolsEnabled` (`1|true|on|v1` — deliberately not `"yes"`: two definitions that disagree would mean write-under-one, read-under-the-other).
- `Render(plan, rows, statuses)` = deterministic body (`# Plan` / `## Checklist` rows / Navigation / Packages / Structure) under a line-1 marker `<!-- talon-plan:v1 sha256:<hex> -->` hashing the body. Same inputs → byte-identical bytes (the eval gate's "written and re-read stable").
- Direction signal = the marker: hash matches → the bytes are ours (safe to overwrite); mismatch *or missing marker* (hand-written file) → the user's. `ParseStatuses` returns desc→status for the four talon markers (`[ ] [~] [x] [!]`); `Sync(content, statuses)` rewrites **only** the marker char of lines whose description names a stored row, keeps prose/own-items byte-for-byte, and re-seals (adopting their text into the canonical body).
- **Join key = description, not ID** — IDs are internal; a reworded line imports nothing (stale-but-honest) rather than matching the wrong row.

**Single derivation:** `orchestrator.PlanTaskRows(plan) []planfile.Row` now backs everything — `seedPlanTasks` (refactored off its two inline loops), `savePlan`'s artifact render, and file↔store imports — so store rows, prompt checklist, and checkbox lines cannot drift.

**Write points:**
- `savePlan(projectID, promptID, resp)` (both doors pass the prompt ID): after `UpdateProjectPlan` → full `Render` + `planfile.Write`; **changed bytes** → `h.checkpoint("plan saved", pre-image snapshot)` (`Existed` from prior `Read`), so `RestoreCheckpoint` undoes creation and re-saves like any turned file. No-op re-save → no write, no checkpoint (byte-stable, zero churn).
- `reconcilePlanArtifact` at the top of `seedPlanTasks`: missing → backfill render; ours → re-render (repairs a restore-stale marker; byte no-op when nothing moved); **theirs** → `ParseStatuses` → `store.ImportPlanTaskStatus` per matching row *and* into the `persisted` map the restore pass reads — before seeding, so the import survives `UpsertPlanTask`'s conflict path and the session restores it. No write on this branch: their text stands until the next flip.
- `syncPlanArtifact` through the persistence hook (`taskPersistence` extracted from `CreateSession` so tests wire the real path): every `MarkPlanTask` flip → `UpsertPlanTask` → sync. Edited file → import every row *except* `justFlipped` (a mid-turn user check survives; the flip's own stale line must not revert it) → `Sync` → write iff bytes changed. Hook skips sync if the store write failed.
- `store.ImportPlanTaskStatus` is an unconditional upsert (clears `completed_by_prompt` — a human, not a prompt, settled this) with a status-vocabulary guard; bypasses the terminal-status protection **by design** (that clause stops *seeding* from clobbering completions; this is the person reopening work). Import iteration always walks **rows**, never the file map — the user's own `- [ ]` items parse but can never become phantom tasks.

**Gates:** `gofmt`/`go vet` clean on all touched files; `planfile` pkg tests (byte-stability + marker round-trip, edit detection incl. missing marker, sync-preserves-user-text + re-seal, parse vocabulary, write no-churn, flag truthy set); `api/TestPhaseW3PlanArtifactWrittenAndStable` (plan turn → file exists, marker-valid, rows+sections present; second save byte-identical; checkpoint holds the absent pre-image; flag-off → no file); `orchestrator/TestPlanArtifact{SeedFlipRoundTrip,UserCheckImportsAtSeed,MidTurnImportOnFlip,FlagOff}`; 10 W3 tests + `TestPlanModeSendsNoToolsToModel` green; `make evals` EXIT=0; `go test -race ./...` EXIT=0 (41 ok).

**Web/mobile connection:** the artifact is server-written into the workspace — no client change. The web/mobile plan view already reads plan JSON; checking a box in the app *or* in the file converges at the next boundary (seed or flip). A future nicety: a "edit `.talon/plan.md` in any editor" affordance, and the web file viewer shows the artifact for free.

**Caveat (honest):** description-join means a user who *rewords* a checklist line breaks that row's import (by design — no safe mapping exists). Their edits are not checkpointed (only `savePlan`'s structural writes create checkpoints; status lives in `plan_task_status`, which checkpoint restore deliberately does not touch). Mid-turn user checks are honored at the next flip's sync or the next seed, whichever comes first.
