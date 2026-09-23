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

---

## W4.1 — codebase index: lexical + graph layers, reconcile-on-touch (committed, dark behind `TALONSTUDIO_INDEX`)

**Plan:** `PLAN_WORLDCLASS.md` W4 items 1–2 (indexer service; Merkle-style incremental updates), led by the plan's own risk mitigation: **pure-Go lexical/graph layers ship first** — no cgo, no watcher, no hard dependency. Register row 14 partially closed (embedding/vector, the two tools, memory fusion, map-as-view remain).

**`internal/index` (new package):**
- `Enabled()` reads `TALONSTUDIO_INDEX` (same truthy set as every W-flag; documented in `.env.example`). Flag off → `Reconcile` is a no-op, nothing writes the `code_*` tables, and the retrieval tools (W4.4) simply won't register — grep-only fallback.
- **File set = the law, by construction:** the walk uses `tools.IsMappableSource` (new export of the map's own predicate) + `tools.ShouldSkipDir` — exactly the set `ProjectMapText` renders and `ProjectFingerprint` hashes. Widening to `.go` means flipping that one switch and updating map/fingerprint tests together.
- **`Analyze(rel, src)` is the parser seam** — one function a future tree-sitter build replaces wholesale: a line-aware TS/JSX scanner for column-0 declarations (indentation excludes body statements), import specifiers (from/bare/require — same patterns the map's extractor uses), and name-matched calls attributed to the enclosing declaration (method-definition lines and keywords filtered; a heuristic, documented as one).
- **Chunking** = one chunk per declaration (the syntactic unit available without a parser), the preamble under the file's base name, hard line-boundary splits past `maxChunkBytes=4096`; bodies re-join the file **byte-for-byte** (pinned by test — trailing-newline handling is the subtle part). `hash = sha256(body)`: content-addressed, and the future embed-cache key (scope's "hash by content").
- **`Reconcile(root, projectID, store)`** is W4.2's mechanism without a watcher: content-hash every walked file, diff against `code_files`, touch only what moved — changed/new → `Analyze` + `ReplaceFileIndex`; vanished → `DeleteFileIndex` + `ClearEdgeTargets` (an unchanged importer's stale `ToPath` clears, so state still equals a fresh build); unchanged → skipped entirely. Relative imports resolve against the full file set (`./x` → ts/tsx/js/jsx/index candidates); packages stay unresolved (`ToPath=""`).

**Store (`codeindex.go`, schema in `migrate()`):** `code_files` (per-file hash = the Merkle state), `code_chunks` + `code_chunks_fts` (external-content FTS5, porter unicode61, trigger trio + rebuild — the `project_memory_fts` pattern verbatim), `code_symbols`, `code_edges`. Methods: `ReplaceFileIndex` (tx: one file's whole slice swapped + hash upsert), `DeleteFileIndex`, `CodeFileHashes`, `SearchCodeChunks` (bm25-ranked; every token individually quoted via `ftsTokens`, so user text can never be FTS5 syntax), `CodeDefinitions`, `CodeEdgesTo`/`CodeEdgesFrom`, `ClearEdgeTargets`, and `CodeIndexDigest` (deterministic sha256 over sorted state — the eval's hash-equal witness).

**Gates:** `gofmt`/`go vet` clean; `index/TestReconcileIncrementalEqualsFull` (**the** gate: one-file write → digest equals a fresh full build; deletion+edge-clear covered; no-op pass re-indexes 0), `TestReconcileTemplateTimeBudget` (cold build of `backend/template` under 10s with files indexed; warm pass re-indexes 0), `TestReconcileResolvesLocalImports`, `TestReconcileFlagOff`, extractor/chunk/resolve unit tests (column-0 rule, byte-exact re-join, oversize split, hash moves on edit), `store/TestCodeIndexRoundTrip` (search hit shape, injection/quoting guard, digest idempotence, FTS-trigger delete) + `TestFtsTokens`; `make evals` EXIT=0; `go test -race ./...` EXIT=0.

**Honest deviations from scope:** (a) tree-sitter (cgo/WASM) deferred — the build stays pure-Go (`modernc.org/sqlite`) and the plan's mitigation explicitly ships lexical+graph first; `Analyze` is the seam. (b) sqlite-vec not used — the vector slice will do in-process cosine over `embedding` BLOBs at workspace scale (provider interface first, brute force where the schema says blob). (c) Go parsing not wired: the file-set parity law currently includes ts/js only — extractor growth and `.go` in `IsMappableSource` land together with map/promptsec tests. (d) `FS.Touched()` turn-end feeding is an optimization, not the mechanism — the hash diff is correctness (and `Touched()` had no caller to hook; the plan itself says "reconciliation-on-touch, no watcher").

**Web/mobile connection:** none yet — no tool reads the index in this slice; W4.4 lands `codebase_search`/`codebase_graph` in the build registry, after which the web timeline renders their labels/targets like any other search tool. Tables are inert until the flag is set.

**Remaining in W4:** W4.3 embedding provider (interface + API default + populate `project_memory.embedding`), W4.5 `read_memory` FTS ∪ vector fusion, W4.6 project map as a view over the index.

## W4.4 — `codebase_search` + `codebase_graph` tools + retrieval benchmark (committed, behind `TALONSTUDIO_INDEX`)

**Plan:** `PLAN_WORLDCLASS.md` W4 items 4–5 — the two index-backed tools in the build registry plus `evals/retrieval/` (gate: indexed ≥ grep-only hit@5). Register row 14 partially closed (embedding, memory fusion, map-as-view remain).

**Tools (`internal/tools/codebase.go`, fn-field pattern):**
- Both follow the `StoreAdapter` law: **the tools package never imports the store.** `Reconcile`/`Search`/`Definitions`/`Callers`/`Callees` are function fields wired in `api/buildsession.go`; `IndexHit`/`IndexSymbol`/`IndexEdge` are boundary types converted at that seam (`convertIndexEdges`). The `FS` field is kept for sibling symmetry — the index walk is root-closure-based, so `Execute` never touches it directly.
- **`codebase_search`** — bm25 over chunks (today's whole retrieval layer; the vector half joins at the same `Search` seam when W4.3 lands, fused before formatting). Reconciles before every query: a `Reconcile` failure yields a stale-but-honest answer with an inline note, never a failed call. Output shares grep's prefix — `path:line (name): excerpt` vs grep's `path:line: content` — so one parser reads both tools, which is the benchmark's formatting contract. Limit 1–25 (default 10); empty results steer back to `grep_codebase`.
- **`codebase_graph`** — `direction` ∈ {definitions, callers, callees}; `distance` 1–2 (callers/callees only, definitions always direct). Callers dedupe on `(FromPath, FromName)` across hops, callees on `ToName`; hop-2 walks the opposite direction from each level-1 edge's far endpoint, skipping self-references and file-level edges. New store method **`CodeEdgesFromSymbol`** (callees-by-name: call sites join on name, not path — `CodeEdgesFrom` stays file-scoped).

**Wiring + classification (flag-gated everywhere):**
- `buildsession.go`: one `if index.Enabled()` block registers both with a shared `reconcile` closure (`fs.Root()`). Flag off → registry byte-identical to today's; grep-only fallback, no hard dependency.
- Name-based lists updated or the tools silently vanish/misbehave: `ai/agentic.go coreToolSet` (the ≥8-core truncation path would filter them out), `harness/tooltimeout/policy.go` readBudget (fast).
- Orchestrator: probe list + `probeTarget` cases — both tools' `query`/`symbol` text joins the same `require()`/`node_modules` package-unit extraction as `grep_codebase`/`read_file` (pinned by new `enforce_test` rows in both directions); `toolLabel` → "Searching the index for …" / "Tracing …"; `toolTarget` → `query`/`symbol` fields truncated at 60. Plan registry untouched (byte-pinned).

**`evals/retrieval/` (new benchmark package):** checked-in fixture (`fixture/src/` — 8 small ts/tsx files) + `fixture/queries.json` (8 natural-language queries → known gold paths). **`TestHitAt5IndexedAtLeastGrep`** runs both systems end to end through the real tools over one shared `path:line` parser: `codebase_search` (limit 5) vs a *generous* grep-only baseline (stopwords stripped, remaining tokens as a `(?i)` alternation — the indexed layer must beat a competent agent's grep, not a strawman). Gate = indexed hit@5 ≥ grep-only hit@5 **and** ≥ 6/8, so `0 ≥ 0` can never pass. **`TestGraphEndToEnd`** pins definitions/callers/callees through real edge rows and asserts no spurious hop-2 in a fixture where nothing calls `authorizeTransaction`.

**Gates:** `gofmt`/`go vet` clean; targeted: tools + index + store + **evals/retrieval** + orchestrator (probe rows + 4 switches) + tooltimeout + ai all pass; `make evals` EXIT=0 (first run hit the known `TestRefreshRotates` TempDir-cleanup flake — same class as `TestClaimAttachmentsSpendsOnce`, test body green, clean re-run); `go test -race ./...` EXIT=0 (44 packages).

**Honest deviations:** (a) "hybrid" is lexical-only today — bm25 stands in for the fusion; the fixture and gate are deliberately built so W4.3's vector half strengthens (never weakens) the same measurement. (b) The index reconciles **at query time**, not via turn-end `FS.Touched()` feeding — freshness without a watcher (W4.1's note; the feeding hook stays an optimization). (c) Distance capped at 2 hops: the scope says "direction+distance" without a bound, and beyond 2 the result set outgrows a tool result.

**Web/mobile connection:** the web timeline already renders generic tool labels/targets — `toolLabel`/`toolTarget` give it "Searching the index for …"/`query` rows exactly like `grep_codebase`, over the same SSE events the mobile app consumes. Both tools are `ReadOnly()` → run concurrently within a turn, no approval, sandbox-op classification unchanged. Every shipped environment keeps `TALONSTUDIO_INDEX` unset until W4.3 flips the deployment default.

**Remaining in W4:** W4.3 embedding provider (interface + API default + populate `project_memory.embedding`), W4.5 `read_memory` FTS ∪ vector fusion, W4.6 project map as a view over the index.

## W4.3 — embedding provider: interface + API default + embed caches (committed, dormant without env)

**Plan:** `PLAN_WORLDCLASS.md` W4 item 3 — provider behind an interface (API-based default; local ONNX the opt-in slot), activate `project_memory.embedding`, embed cache keyed by chunk hash. Register row 14 partially closed (memory fusion, map-as-view remain).

**`internal/embed` (new package):**
- `Provider` = `Name()` + `Embed(ctx, texts) ([][]float32, error)` — the only contract the store sees (store declares a structurally-identical `Embedder`, so neither package imports the other: the StoreAdapter law, one layer deeper).
- **API default (`*API`)**: POST `{model, input:[…]}` to any OpenAI-compatible `/v1/embeddings` endpoint — OpenAI-shaped hosts and local servers (Ollama, llama-server) alike. Batched (64, overridable), ctx-respecting, `Authorization` only when a key exists, and strict response validation: wrong item count, untrustworthy `index` ordering, mixed dimensions, empty vectors → hard errors, never best-effort (a misaligned vector poisons content-addressed cache rows and keeps being served).
- **`FromEnv()`**: `TALONSTUDIO_EMBED_URL` (the only required var) / `_KEY` (optional — keyless local endpoints) / `_MODEL` (default `text-embedding-3-small`), whitespace-trimmed; **unconfigured → nil**, and every caller treats nil as "vector layer does not exist", not an error. Documented in `.env.example`.
- ONNX/local: a deliberate *slot*, not a stub — the pure-Go build law (W4.1) stands, and a fake deterministic local embedder would lie about retrieval quality. Whoever owns the native dependency implements `Provider`; nothing else changes.

**Embed caches (store):**
- **Chunk cache keyed by content hash** (Cursor's move): `ReplaceFileIndex` reads the file's `hash→vector` pairs *before* its delete-and-reinsert swap and re-applies them to surviving hashes — an unchanged chunk never re-embeds; new hashes land NULL for the sweep. `CodeIndexDigest` deliberately excludes vectors (pinned by test), so swept incremental builds keep hash-equal to fresh full builds.
- **Sweeps**: `EmbedCodeChunks(ctx, projectID, emb, batch)` + `EmbedMemories(ctx, …)` share `embedRows` — select `(id, text)` where `embedding IS NULL`, batch-embed, update by PK. Idempotent (embedded rows never selected → zero provider calls when nothing to do), failure-retryable (rows stay NULL; earlier batches' progress is safe because each row's vector depends only on that row's own text). `EmbedMemories` activates the `project_memory.embedding` column the schema has carried since P1c.
- **On-disk format**: little-endian IEEE-754 float32 (`vecBytes`/`VecFromBytes` via `math.Float32bits`) — the sqlite driver refuses `[]float32` outright (the test caught it: "unsupported type []float32"), which is how the format earned its name. Truncated blob → `nil`, never a garbage vector. This is the pair W4.5's cosine scan reads: change one, change both.
- **Schema**: `code_chunks.embedding BLOB` in `CREATE` + guarded `ALTER` (house pattern — duplicate-column error on fresh DBs is the benign no-op).

**Wiring (`api/buildsession.go`)**: one paragraph after the index tools — `embed.FromEnv()` nil → nothing runs (byte-identical, zero goroutines: the whole layer is dormant in every shipped environment and every test). Configured → a single background goroutine with a 60s ctx sweeps chunks (only when `index.Enabled()`) then memories, logging only on n>0 or error. Builds never block on the network.

**Gates:** `gofmt`/`go vet` clean; `embed` units (request shape + auth header + out-of-order reordering; batching 5@2 → 2/2/1 with position-aligned concatenation; rejection table: 500 / wrong count / duplicate index / mixed dims / empty vectors; empty input makes no request; missing endpoint errors) + `FromEnv` dormancy/defaults/override; `store/TestReplaceFileIndexCarriesEmbeddings` (carry / new-NULL / vanished-row), `TestEmbeddingExcludedFromDigest`, `TestEmbedCodeChunksSweepIdempotent` (first fill → no-op → edit embeds ONLY the fresh body), `TestEmbedSweepFailureLeavesRowsNull` (+ recovery), `TestEmbedMemoriesSweepIdempotent` (FK-seeded the way `memory_recall_test` does; round-trip through `VecFromBytes`; incremental fill); `make evals` EXIT=0 (the `TestRefreshRotates` TempDir-cleanup flake again — same `testing.go:1464` signature as W4.4's run, body green both times, clean re-run); `go test -race ./...` EXIT=0 (45 pkgs).

**Honest deviations:** (a) sqlite-vec stays rejected (pure-Go build — W4.1's documented decision); "activate the column" means the column + byte format + population, and W4.5 does in-process cosine over `VecFromBytes`. (b) Local ONNX not implemented — interface slot only, per the risk mitigation's own wording ("interface + flag + pure-Go … ship first"). (c) Embed-on-*write* for new memories is not wired — the build-time sweep covers legacy and new rows on the next session build; W4.5 (the fusion consumer) decides whether read-time embed-on-miss earns its complexity. (d) Cross-batch dimension consistency isn't sweep-enforced (a model change mid-cache would mix dims in the DB) — W4.5's read path must validate dims before fusing.

**Web/mobile connection:** none visible — no UI surface for vectors; sweeps are silent background work (logging only when vectors land). The gain arrives with W4.5's fused `read_memory`, which both clients consume through the existing events. Config ships as empty env keys only.

**Remaining in W4:** W4.5 `read_memory` FTS ∪ vector fusion (reads `VecFromBytes`), W4.6 project map as a view over the index.

## W4.5 — `read_memory` FTS ∪ vector + hybrid `codebase_search`: one shared fusion (committed, dormant without env)

**Plan:** W4 items 4–5 — memory recall upgrade (FTS ∪ vector, "same fusion") plus the deferred vector half of item 4's hybrid search. Register row 14 partially closed (map-as-view remains); register **row 16** (memory FTS-only recall) closed.

**`internal/recall` (new package — the scope's "same fusion"):**
- `Fuse(ranked ...[]string) []string` — reciprocal rank fusion (score = Σ 1/(K+rank), K=60) over ranked ID lists. It never normalizes scores: bm25, cosine, and recency numbers mean nothing to each other, and RRF deliberately never looks at them. Ties keep first-seen order, so callers pass lexical before vector — a tie resolves toward bm25 evidence. A duplicate ID within one list counts once (first rank); empty/nil legs degenerate to a stable copy of the other, so single-leg calls return today's order untouched.

**Store legs (`store/vectorsearch.go`):**
- `cosine(a, b) (score, ok)` — `ok=false` on dimension mismatch (a model change mid-cache: the read-path validation W4.3's notes reserved) and zero norms (no direction, no meaning). Never an error.
- `SearchMemoryVector(pid, query, entryType, limit)` — brute-force cosine over `project_memory.embedding` (in-process, the documented sqlite-vec rejection at workspace scale), `decryptContent` exactly like the FTS leg, type filter mirroring FTS, scan order `created_at DESC, id DESC` so ties resolve by recency (matching FTS intuition), stable-sorted by score, capped at 50. Empty query → (nil, nil), no scan.
- `SearchCodeChunksVector(pid, query, limit)` — same shape over `code_chunks.embedding`; `Score` carries cosine (fusion reads ranks only); deterministic path/line/name scan order.

**Tool fusion (both tools, literally one component):**
- `read_memory` (`tools/memory.go`): a `fuse` closure over row IDs calls `recall.Fuse(ftsIDs, vecIDs)` — FTS list first. New optional `StoreAdapter.VectorSearchMemoryFn` (nil = vector layer off, by configuration); an error degrades to FTS silently — configured-but-unavailable never fails a recall the lexical leg can still answer. **Fuse runs only when the vector leg produced rows**: FTS-only output is byte-identical to today's (existing tests untouched; the deployment law).
- `codebase_search` (`tools/codebase.go`): new optional `VectorSearch` field; `hitID` = `path:line:name` — both legs read the same reconciled rows, so chunk identities pair exactly; `fuseIndexHits` runs `recall.Fuse` and truncates to limit. FTS-empty + vector-nonempty uses the vector list directly; a failing leg appends an honest `[vector leg unavailable: …]` note while lexical results keep flowing (same degrade contract as the reconcile note). **Item 4's hybrid is now complete** — the fixture benchmark stays green because tests configure no provider (lexical-only path).

**Provider seam (`api/buildsession.go`):**
- `embedQuery(query)` — shared by both legs: `embed.FromEnv()` per call (nil → `(nil, nil)`: an absent vector layer is configuration), 8s timeout bounding the tool's latency budget. Wired as `VectorSearchMemoryFn` in `storeAdapter()` and `VectorSearch` in the `CodebaseSearch` registration. Zero signature churn anywhere — a missing provider reaches every tool as a nil field or empty leg.

**Gates:** `gofmt`/`go vet` clean; `recall/TestFuse{SingleListIsStable,OverlapBoosts,TieKeepsFirstList,DeduplicatesWithinList,EmptyLegs,SkipsEmptyIDs}`; `store/TestSearchMemoryVector{RanksByCosine,BadRows}` (type filter, limit, dim-mismatch/zero-norm/garbage-blob skips, empty query) + `TestSearchCodeChunksVector` + `TestCosineUnit`; `tools/TestReadMemory{FusesVectorLeg,VectorOnlyAnswers,VectorOffIsUnchanged,VectorErrorDegrades}` + `TestCodebaseSearch{FusesVectorLeg,VectorOffIsUnchanged,VectorErrorNotes}` — both tools pinned end-to-end through real `Execute` output; retrieval benchmark + index/embed untouched and green; `make evals` EXIT=0 (first run, no flake); `go test -race ./...` EXIT=0 (46 pkgs).

**Honest deviations:** (a) The fusion *call sites* live in each tool (a closure in `ReadMemory`, helpers in `CodebaseSearch`) because the two adapters' shapes differ — but the *ranking* is literally one function (`recall.Fuse`) used by both, which is what scope's "same fusion" means. (b) The vector leg re-reads `embed.FromEnv()` per query instead of capturing at build time: deliberate — zero signature churn, env reads are cheap, and the 8s context bounds the cost. (c) When the vector leg answers and FTS found nothing, the substring fallback is skipped (the fused result wins); substring stays the last resort only when both legs miss. (d) Raw scores never reach the model — rank order only, consistent with `IndexHit`'s deliberate Score-absence.

**Web/mobile connection:** no UI change — both are model-facing tools; the web timeline renders their labels/results over the same SSE events the mobile app consumes. The behavior difference is visible only with `TALONSTUDIO_EMBED_URL` set (results fuse); unset in every shipped environment.

**Remaining in W4:** W4.6 project map as a view over the index.

## W4.6 — project map as a view over the index: W4 closes (committed, dark without env)

**Plan:** W4 item 6 — the map becomes a *view* over the index (never a one-shot snapshot); "refresh on fingerprint change is automatic because the index is event-fed." Register **row 15** closed; register row 14's remainder closed — **W4 complete**.

**The design — one renderer, one extractor, one file-set law:**
- `tools/projectmap.go` refactor: `MapFile{Path,Exports,Imports}` row type; `renderMap(files)` extracted as the single renderer (grouping, ordering, header, size cap untouched — byte-identical output); `ProjectMapText` = walk → rows → render, `ProjectMapTextFromIndex(rows)` = rows → same render (pure). `ExtractExports`/`ExtractImports` wrappers expose the walk's own extractors to the index — the view stores the walk's data, so it cannot drift from it.
- `ProjectMap` tool gains optional `Reconcile`/`IndexFiles` fn fields (StoreAdapter law — tools never import the store): wired → reconcile-on-read, render the view, `[index refresh failed: …]` note when rows rendered behind a failed refresh; fetch error or empty view → silent fall to the walk (always fresh — it reads the tree directly); nil fields (flag off / no store) → byte-identical old walk.
- Store: `code_files` gains `exports`/`imports` TEXT columns (CREATE + guarded ALTER, the W4.3 embedding pattern); the first boot that successfully ALTERs also runs `UPDATE code_files SET hash = ''` — an old database can't backfill parsed data in SQL, so invalidating forces the next reconcile to repopulate every row; a failed ALTER (fresh or already-upgraded) must NOT invalidate (no full re-index per restart — both branches pinned). `SetFileExports` upserts rows WITHOUT touching hash (`ON CONFLICT … SET exports/imports` only); `CodeFilesWithExports` selects path-ordered rows; corrupt JSON surfaces as an error so callers degrade to the walk rather than render a wrong map.
- `index.Reconcile`: `SetFileExports` runs BEFORE `ReplaceFileIndex` — the order IS the correctness argument: an exports failure leaves the hash unmoved (retry next reconcile), while the reverse order could leave a hash-matched row permanently missing map data (reconcile skips hash-matches forever).
- Injection seams (`api/mapview.go`, new): `projectMapTool(fs, pid)` wires the tool's fields; `projectMapView(ws, pid)` = reconcile → select → view, walk on any failure; `mapFilesFromRows` = the boundary conversion (StoreAdapter law). Used by the build-session registration, build-start injection (`prompts.go`), and the W3 plan prompt (`planPromptSections` gains `projectID` — flag-off byte-identity pinned by `plan_prompt_test`).
- Refresh (`orchestrator`): `SessionState.mapRender` set at build start; `refreshProjectMap` prefers it over the walk when the FP check trips. The fingerprint stays the trigger (the plan's "refresh on fingerprint change" — this repo's event-feed is reconcile-on-touch); the index only changes what the rebuild reads.

**Gates:** `gofmt`/`go vet` clean; `tools/TestProjectMapIndexViewMatchesWalk` (THE parity gate: index-path rows render byte-identically to the walk) + `TestProjectMapTool{PrefersIndexView,WithoutIndexWalks,IndexFetchErrorWalks,ReconcileErrorNotes,EmptyIndexFallsToWalkMessage}`; `index/TestReconcileStoresMapRows` (store → edit → delete lifecycle through the walk's extractors, flag-gated); `store/TestSetFileExportsKeepsHash` (both write orders compose; tenant-scoped) + `TestMapColumnsUpgradePath` (upgrade invalidates once; re-open preserves — no restart re-index); W3 plan-prompt tests green with the new pid param; `make evals` EXIT=0; `go test -race ./...` EXIT=0 (46 pkgs).

**Honest deviations:** (a) The plan says "event-fed"; this repo ships no filesystem watcher (W4.1's documented law) — reconcile-on-touch at every render point is the equivalent: the view is fresh by construction wherever it renders, and the FP check remains the prompt-cache trigger (its existing job). (b) Flag on with a fetch error or empty view degrades to the walk rather than failing — the walk IS the flag-off path verbatim, so degradation is byte-identity, not a lesser answer. (c) `projectMapTool` and `projectMapView` share the rows→tools conversion (`mapFilesFromRows`) and both call `ProjectMapTextFromIndex`/`ProjectMapText` — render and extraction are single-sourced; only the wiring contexts differ (tool field vs prompt section).

**Web/mobile connection:** no UI change — the injected map rides the same system-prompt sections and SSE/tool events both clients already consume; flag off, every byte is what shipped before. Flag on, both clients' agents see a map that cannot lag a write already indexed this turn.

## W5 — Ground truth: LSP + formatter: gate zero, the senses, the feed (W5.1–W5.4)

**Plan:** W5 items 1–4 + eval gate. Register **row 17** closed — **W5 complete**.

**The fleet (`internal/lsp`, behind `TALONSTUDIO_LSP`, truthy `1|true|on|v1`):**
- Device-reaper pattern for process lifetimes: `Manager` keyed `root|lang` (LRU cap 4, idle 10m / sweep 1m, restart-on-crash, `Shared()` lazy singleton); `resolve` = `.talon/config.json` `"lsp"` override → builtin (`.go`→`gopls`, ts family→`typescript-language-server --stdio`); undetected extension → `""` (tool degrades, never errors); missing binary → structured `UnavailableError` (`"LSP_UNAVAILABLE: …"`, `IsUnavailable`) — **never a block** (fail-open-availability / fail-closed-truth split).
- `Client` = kill-group exec (`Setpgid` + group-kill in `hardClose`; no `cmd.Cancel` — os/exec rejects it on non-`CommandContext` commands and lifetime is owned by `Close`), initialize 30s timeout, `didClose`+`didOpen` to re-open, per-file diagnostic version + broadcast wait, 1-based surface → 0-based wire. `KindName` uses the full LSP spec numbering (5=class, 7=property, 22=enum-member, 26=type-parameter, default `kind-N`).
- **No real servers exist in this environment** (no network, no binaries) — the fake stdio server is the test binary itself: `TestMain` intercepts `TALON_LSP_FAKE=1`, `.talon/config.json` `"lsp"` key points at `os.Args[0]`, behavior scripted by `FAKE_*` env vars (once-crash via marker file). Protocol + bytes are pinned honestly; real tsserver/gopls behavior is not claimed.
- Config surface: `.talon/config.json` `{lsp, formatter}` — `Fields`-split, no shell (injection-free).

**The senses (`internal/tools/lsp.go` + `lspwrite.go`):**
- Seam law: tools own an `LSPConn` interface + `LSPResolve` fn field — `*lsp.Client` satisfies it structurally (no adapter, fakes trivial). Read tools: `lsp_diagnostics` (returns `VerifyResult` JSON — dual model/gate role), `lsp_references`/`lsp_definition` (1-based positions validated loudly: `line`/`col` must be ≥1), `lsp_symbol` (`line kind name` lines, case-insensitive `name` filter, honest empty messages).
- Workspace sweep: `maxLSPWorkspaceFiles=400` with an honest truncation note; grouped by `lsp.LangFor` — one resolution attempt per dead class (`conns[class]=nil` sentinel), all-dead → single `errResult` so the ladder records the skip, partial death → note in Summary. File set = `LangFor`'s domain (ts family + Go) under the map's *walk law* (skip dirs/symlinks/dotfiles) but deliberately **not** `IsMappableSource` — that predicate is the map/index same-file-set law and stays ts-only by design (`projectmap.go` documents widening it as its own coordinated change).
- `lsp_organize_imports` (W5.4): the one write any lsp tool performs. Client `OrganizeImports` → `textDocument/codeAction` (`only: ["source.organizeImports"]`, whole-doc range) → `parseCodeActions` → 1-based `lsp.TextEdit`s → `lsp.ApplyEdits` (pure: back-to-front splice, clamp on stale positions, UTF-16 column math — non-BMP = 2 units) applied through `FS` — journaled, workspace-scoped, write budget. Server proposes; the tool writes.

**Gate zero + the failure-index feed (W5.3):**
- `verifyGates()` prepends `{stage:"lsp", tool:"lsp_diagnostics", feedback:lspFeedback}` **only when `lsp.Enabled()`** — flag-off ladders are byte-identical (a permanently-skipped "lsp" entry in every deployment's `SkippedGates` would be a behavior change, not a note). Unavailable tool → `errResult` → `runGate` isError → `NoteGateUnavailable` → `SkippedGates` → ladder continues to tsc.
- `Diagnostic.FormatTsc()` renders tsc's own shape (`path(line,col): error TS2322: msg`; numeric code→`TS+n`, `tsNNN`→`TSNNN`, else verbatim) so `knowledge.NormalizeError` + the seeded `run_typecheck` `ts\d+` patterns match gate-zero output with **no second rule set**. `lspFeedback` mirrors `typecheckFeedback` (same repair job, names files via `errorFiles`, points at `lsp_diagnostics` for the instant re-check and `run_typecheck` for the authoritative pass).
- **Feed (`orchestrator/failurefeed.go`):** failed verify *verdicts* now consult `DefaultFailureIndex` — previously only tool errors did. Fast-path substring gate (`"passed":false`) keeps passing results allocation-free. The verdict path consults the `run_typecheck`-scoped seeds **by shape** (gate zero prints tsc's bytes; scoping by the printing tool would hide a known fix behind a name change) — error paths keep their exact existing scoping, cooldown still yields one nudge per window. `runtimePattern` normalizes recorded matches at load (`TS17004` recorded → matches lowercased normalized text); `parseFailurePattern`'s raw contract untouched (pinned by test).

**The formatter key (W5.4):** `formatterRunner` in the tools package consults `.talon/config.json`'s `"formatter"` key **behind the flag** — flag-off `run_prettier` is byte-identical shipped npx prettier. `run_prettier` remains the formatter of record (the key swaps the binary, not the tool); no LSP formatting path added, per the plan.

**Wiring:** one `lspResolve` closure in `api/buildsession.go` under `if lsp.Enabled()` (after `InspectPackage`); name switches: `ai/coreToolSet` (5), `tooltimeout` read budget (4) + write budget (organize), orchestrator probe list + `probeTarget` (4 reads), `describeTool` (5), `toolTarget` (5).

**Gates:** `gofmt`/`go vet` clean on every touched package; `internal/lsp` (handshake, cross-file refs/definition, tsc byte-exact `FormatTsc`, missing-binary unavailable, crash→fresh restart, LRU/reaper/resolve, flag-off, organize round-trip, `ApplyEdits` insert/delete/multi/clamp/UTF-16); `internal/tools` (file-scope verdict, sweep, unavailable, 1-based rejection, refs/def hits, symbols+filter, organize-through-FS + journaled `Touched()`, formatter flag matrix); `orchestrator/lspgate_test` (flag-gated ladder byte-identity, unavailable → `SkippedGates` + ladder continues, failure names the file with **only** `lsp_diagnostics` consulted) + `failurefeed_test` (verdict consult, cooldown suppression, recorded-pattern normalization); `make evals` EXIT=0; `go test -race ./...` EXIT=0.

**Eval gate clauses (plan :346-348), honestly:** (1) planted error named without tsc — the fake server round-trip (`TestClientHandshakeDiagnosticsTscShape`) + gate test prove no tsc is consulted; real tsserver is not in CI, the fake stands in; (2) references resolve across files — pinned across `app.ts` + `other.ts`; (3) unavailable → current path + `SkippedGates` contains "lsp" — pinned end-to-end through `o.verify`.

**Honest deviations:** (a) fake server stands in for real tsserver/gopls (no network/binaries in env) — protocol and bytes are real, import *sorting* logic is the real server's; (b) the sweep walks `LangFor`'s set, not `IsMappableSource` (map law untouched — documented in the code comment); (c) verdict-path shape-consult of `run_typecheck` seeds is a deliberate, text-gated scope extension (worst case: a correct reminder); (d) no LSP `textDocument/formatting` — prettier stays formatter of record per plan item 4; (e) `lsp_organize_imports` is not in the probe list (probes track reads; writes aren't probes).

**Web/mobile connection:** no UI change — lsp_* ride the same SSE tool events and timeline rows both clients already render; gate zero appears as a stage in the verification ladder the web app draws, and its unavailable skip surfaces through the existing `SkippedGates` "Verification note" both clients show. Flag off (every shipped environment): registry, ladder, and `run_prettier` are byte-identical to what shipped before.

## W6 — Verification becomes policy: the matrix, the verdict, the shipping rule

**Plan:** W6 items 1–4 + eval gate (PLAN :352-374). Register **row 18** closed; **row 19 partially** — W6.3 (named `scorecard` stage) landed, W13 (nightly publish) remains.

**The matrix (`orchestrator/verify_policy.go`):**
- `GateClass` = `required | best-effort | off`. Defaults (`defaultGateClass`): typecheck/lint/bundle/gradle **required**; boot/vision/interact **required iff `RunOptions.DeviceAttached`**, else best-effort (device-less CI/desktop cannot satisfy them); lsp **best-effort** (W5's never-a-block law, promotable per project); anything unknown → required (fail-closed).
- Per-stage override: `.talon/config.json` `"verify_gates"` (e.g. `{"vision":"off"}`, `{"lsp":"required"}`); an invalid entry logs and degrades to the default — **never to off**. `off` stages are filtered out of `verify()` before running: never run, never recorded as skipped (an explicit project choice is not an infra gap). Default configs never resolve to off, so default ladders are unchanged.
- Policy resolution (`resolveVerifyPolicy`): project `verify_policy` → env `TALONSTUDIO_VERIFY_POLICY` → `strict`; unrecognized value → strict + logged (a typo in a safety flag must not mean permissive, and an invalid project value must not fall through to a looser env). `.env.example` documents both keys; truthy-style values are deliberately *not* used here — `strict|besteffort` only.

**The verdict (outcome site `orchestrator.go`, ladder-failure exit):**
- `classifyVerification(policy, ws, skips, device)` → `verified` (no skips) | `best-effort` (every skip is best-effort-class, or any skip under the `besteffort` policy) | `refused` (a required skip under strict; the reason names the gates).
- `BuildOutcome` gains `VerifiedBestEffort` + `VerifyPolicy` — **bare `Verified`-as-unannotated is unreachable**: refused sets `Verified=false` and appends a `"Verification policy: …"` line to `FinalText`; best-effort keeps `Verified=true` (protects evals `sample.Verified` and every device-less test) with `VerifiedBestEffort=true`.
- The `maxVerifyAttempts` exit records `refused` with `verifyStateReason(6)` — a build that never compiled cannot hide behind a stale pass.
- Classification only ever sees `SkippedGates` (tool-error / unreadable / missing-verdict / lsp-errResult). G4's "passed + unavailable summary" stays a **pass**, exactly as shipped — evals' gradle/bundle soft-skips still count as passes.
- `remainingNote`/`RemainingTasks` unchanged in content; `RemainingTasks` is now also a policy input (recorded into the state row and named in best-effort refusal reasons).

**Durable shadow (`store/project_verify_state`, `store/verify.go`):**
- One row per project, upsert — newest sweep wins (a later pass clears an earlier refusal; a later refusal re-closes a pass). `skipped`/`remaining` are JSON arrays (nil coerced to `[]` at write). `GetProjectVerifyState` returns `(found, st, err)`; `found=false` is "never swept", not an error.
- `recordVerifyState` is nil-store safe (test harnesses record nothing); write errors log and never fail the turn — the outcome is primary truth, the row is its shadow.

**The shipping rule (shared, `orchestrator.VerifyReleaseRefusal`):**
- `""` = ship. No record → *"no verification record"* (absence of evidence is not a passing build); `verified` → ship; `best-effort` → refuse naming the skipped gates **and** pending plan tasks; `refused` → refuse naming the reason.
- **Export/release:** `enforceVerifyPolicy` runs in `Create` after `whats_new` validation and **before** the approval seam and `CreateExportJob` — 403 carries the reason; `override:true` (the named human key) ships and writes **one `verify_override` audit row per attempt** (`AuditLog(userID, project, "verify_override", "export", reason)` — deliberately no dedup: each ship attempt is its own accountable act).
- **APK-ship:** the `build_apk` guardrail denies only on a **recorded** `refused` status (never-verified stays fenced by the existing scope rule, so `TestBuildApkGuardrail*` behavior is untouched); the `device_build` approval doubles as the override key — approved + refused → allow + one `verify_override` audit row per call; unapproved + refused → deny with the refusal reason.
- Wire format additive only: `verified_best_effort` + `verify_policy` in the JSON response default map and the SSE finish payload; `outcomeLabel` gains `verified-best-effort`.

**W6.3 scorecard stage (`Makefile`):** `evals: vet scorecard test`; `scorecard` runs `TestFixtureScorecard|TestGrade` over `internal/evals/` — the four fixture cases are pinned (clean → `Passed()`+1.0; browser-wallet → critical `no-browser-wallet`; secret-material → critical `no-secret-material`; web-primitive → non-critical `native-components-only` with `Passed()` still holding). Grader correctness now gates every merge; W13 wires the nightly `make e2e-live` publish.

**Gates:** gofmt + `go vet ./...` clean on every touched file; `orchestrator/verify_policy_test` (precedence + invalid→strict, class/config matrices, classify table incl. "off can never refuse", off-filter through `o.verify` with a no-config control, full-loop best-effort-keeps-verified+records, full-loop required-skip-refused with `Verified=false` + reason naming the gate, full-loop ladder-failure records refused with the repair-budget message); `store/verify_test` (roundtrip, upsert-latest-wins, not-found, nil→empty); `api/export_verify_test` (no-record/refused/best-effort → 403 with reason + zero audit rows; override → ship + exactly-one audit row naming actor/action/reason; verified → ship with no audit noise; Create-level 403 short-circuit before seam/job — nil builder proves the short-circuit); `evals/scorecard_test`; `make evals` EXIT=0; `go test -race ./...` EXIT=0.

**Honest deviations:** (a) `refused` under strict flips the outcome's `Verified` to `false` *after* the ladder passed — the ladder stays fail-open (missing infra never loops the model), only the claim changes; (b) the ladder-failure refusal is recorded at the terminal exit, not on each failed attempt (mid-repair sweeps are not final verdicts); (c) release refusal for `best-effort` is intentionally strict per plan — override is the escape; (d) no client UI change ships with W6 — the new fields are additive on the response/SSE both clients already read.

**Web/mobile connection:** no client change required — `verified`, `skipped_gates`, and `remaining_tasks` render where they always have; the new `verified_best_effort`/`verify_policy` ride the same JSON response and SSE finish payload both clients already parse (additive keys, ignored until a client chooses them). The refusal itself is user-visible in the turn's final text ("Verification policy: …") and enforced server-side at export/APK-ship — a client cannot bypass it by ignoring the fields.

## W7 — Provider router: no single point of failure (flag `TALONSTUDIO_ROUTER`)

**Plan:** W7.1–W7.4 + the four-clause eval gate (PLAN :376-402). Register **rows 20/21/22 closed**; **row 24 amended** — `harness/settings` is resolved here, W8.2/W14 keep composition/ralph/goals. `.env.example` documents `TALONSTUDIO_ROUTER` and the four-section document; `harness/composition`'s package doc repointed at `ai.AttachSettingsFile`.

**One watched settings document (W7.4) — `ai.AttachSettingsFile` (main.go:119) is the single hot-reloaded file, four sections:**
- `{"routing": {…Policy…}}` — parse semantics unchanged (routing section, else flat policy). **New:** a valid document whose only sections are extras is no longer logged as "invalid" — routing rides env/defaults until a routing section appears. `harness/settings` **deleted** (plan-sanctioned "consumed or deleted": its camelCase vocabulary never shipped a consumer); H12's settings-flip gate moved to turn level (`orchestrator/TestSettingsEditFlipsRoutingNextTurn`) per the plan's "finally run" note.
- `{"pricing": {"model": [in, out, cached]}}` — same shape as `ROUTING_PRICING_JSON`; env seeds, document overlays and **wins** (`ai.LoadPricing`, flag-gated: flag off = `DefaultPricing()` byte-identical).
- `{"providers": [{name, protocol, base_url, api_key, model}]}` — failover targets (`ProviderSpec`); unnamed or endpoint-less entries are dropped, never pinnable by accident.
- `{"model_tier": "strong|medium|weak"}` — tier display (`ai.TierOverride`; env `AI_MODEL_TIER` fallback, `capabilities.go` reads the override instead of env directly).
- Discipline shared: mtime-checked reload with last-good hold — an unreadable document keeps the last good extras rather than dropping configured providers mid-turn.

**W7.1 — multi-provider failover (everything behind `TALONSTUDIO_ROUTER`, truthy `1|true|on|v1`):**
- `Client` gains `providerName` (env client = `DefaultProviderName` `"primary"`), `ProviderName()`, and `ai.ClientForProvider(spec, base)`: endpoint/credentials/protocol swap over base's http client/pacer/retry hook (via `clone`, field-by-field); partial specs inherit base pieces; borrowed `lastErr` is cleared — one endpoint's failure is not evidence about another.
- Health registry + three-state breaker (`internal/ai/router.go`): closed → open (threshold var, default 1 — by the time a failure escapes `ChatWithTools` the client already retried internally, so one observed failure means the endpoint is down) → half-open after the cooldown (var, default 45s) admitting **exactly one probe** → closed on success, reopened fresh on probe failure. Fed only from the orchestrator loop, flag on: listener failure, failed failover target, and success after any answering round (the probe included).
- Failover path: `requestErrorListener(round, provider, model, err, fellBack)` records the provider failure, then `routing.fallback_provider` (spec exists + `AllowProvider`) → `loop.RetryOnProvider` → client swap + latch (`latchedModel` **and** `latchedProvider` move together — the model and the endpoint that answered). Target missing from the providers block or circuit-open → honest Terminal naming which. Flag off: byte-identical today's policy (fellBack → terminal; `fallback_model` path unchanged).
- Round start: the latch rebuilds the client on the latched provider; unlatched, `SelectStartProvider` skips an open primary for the fallback spec (that call is itself the half-open probe once the cooldown passes); nothing provably healthy → current anyway — the request and listener stay the last resort, because refusing to send hides an outage behind a stall.
- Audit pinning: the cross-provider retry appends `llm/retry` (`Attempt:1, BackoffMs:0`, `RetryPayload.Provider` — the wait a client-side retry took is not this event's story); `usage/record` pins `UsagePayload.Provider` and the **served** model (a mid-round failover is attributed to who answered); `outcome.RoutedProvider` set flag on. Flag off: `provider` fields omit out of the JSON (`omitempty`), model bytes unchanged.

**W7.2 — `routing.vision_model`:** `ai.ForVision(c)` returns the chat client untouched off-flag or unset, else a clone on the vision model; both vision seams call it (`buildsession.go`: `visionAdapter` for `capture_screenshot`, `gateVision` for the G4 `run_vision`/`run_interact` gates). Effective the next turn like all policy reads (the document is mtime-hot).

**W7.3 — turn spend cap:** `routing.max_turn_cost_usd` (flag on only) read from the same once-per-turn policy load; each round's usage is priced with `ai.LoadPricing()` on the **served** model and accumulates. Top of the next round — **after `flush()`**, so every finished round is durable — spend ≥ cap: an assistant message naming spend, cap, and rounds is appended to the transcript, `outcome.CostUSD` + `outcome.BudgetStop=true`, `logTurn("budget-stop")`, **nil error** (deliberate like `Exhausted`), zero further requests dispatched. Flag off: budget stays 0 — no accumulation, no stop, `CostUSD` stays 0.

**Wire + clients (additive, flag-gated):** the JSON response map and SSE finish payload gain `cost_usd` / `routed_provider` / `budget_stop` **only when `RouterEnabled()`** — flag-off bytes identical. Web app and companion mobile app read the same response/SSE shapes both clients already parse: the keys are additive and ignored until chosen (e.g. an "answered by secondary" badge, a spend line on the turn). The budget stop needs no client change at all — it speaks in the turn's final text, dollars named, and returns like any completed turn.

**Gates (the plan's four clauses + unit pinning):**
1. `orchestrator/TestRouterFailsOverAcrossProviders` — primary 500s (instant, no backoff), secondary serves write+done: latch proven by the second secondary call, `RoutedProvider=="secondary"`, exactly one `llm/retry` naming secondary, `usage/record` pinned secondary ≥1 with primary ==0, and a **second turn** that round-starts on secondary with the primary's call count frozen (breaker skip).
2. `api/TestVisionGateRoutesToVisionModel` — `gateVision{c: ai.ForVision(...)}.VisionCheck` hits the fake with `body.model=="vision-model"` flag on, `chat-model` flag off.
3. `orchestrator/TestRouterBudgetStopsTurnHonest` — $1-per-token pricing vs a 0.0001 cap: exactly one request ever dispatched, `BudgetStop`, `CostUSD≥cap`, `$` in FinalText, nil error.
4. `orchestrator/TestSettingsEditFlipsRoutingNextTurn` — turn 1 model-a, rewrite + `Chtimes`, turn 2 model-b, same process, **flag off** (hot reload is the document's discipline, not a router feature).
Plus `ai/router_test.go`: truth set, breaker state machine (threshold trip, single in-flight probe refusal, failed-probe reopen, success closes), `SelectStartProvider` switch/refuse rules, providers flag-gating + partial-spec inheritance + nil-endpoint/nil-base cases, pricing overlay (doc wins; flag off = env seed), tier override precedence, `ForVision` pointer identity, `applyOverride` merges (and an empty override never clears). Closeout: gofmt on touched files (whole `routing.go` — pre-existing dirty, touched), `go vet ./...`, `make evals`, `go test -race ./...`.

**Honest deviations:** (a) breaker threshold defaults to 1, not N — internal client retries already burned the network attempts, so an escaped failure *is* N+1 real failures; threshold/cooldown are test-overridable vars, deliberately **not** env knobs (no operator surface for an unproven number); (b) `max_turn_cost_usd` merges only when `>0` — a partial document cannot express "remove the cap" (removing it means removing the field at its source); (c) the budget check sits at the top of the round after flush — the in-flight round's tools complete first, so no dangling `tool_use` reaches the transcript; (d) usage `Model` becomes the served model **only when routed** — flag off keeps today's `client.Model()` bytes even after a same-endpoint fallback (pre-existing attribution quirk left byte-identical rather than silently fixed inside W7); (e) provider names are case-sensitive — they are pin values in events and breaker keys, not display labels; (f) `harness/settings` deletion changes no runtime path (zero importers beyond the eval block that went with it).

**Web/mobile connection:** no client change required anywhere — new response/SSE keys are additive and flag-gated, the budget stop is ordinary final text, and provider pinning is available for a UI badge without one. The outage behavior *is* user-visible where it matters: a turn that would have died now completes on the second provider, with `llm/retry` in the session log explaining why.

## W8 — Ship or delete: the dark-code reckoning

**W8.1 — the two flippable flags flipped (default ON, env = rollback):**
- `TALONSTUDIO_PIPELINE` (`orchestrator/pipelineEnabled`): unset now means the H3 bounded pipeline; `0/false/off` restores legacy sequential dispatch. The flip's whole safety argument is `TestPipelineTranscriptParity` — same scripted provider, both modes, byte-identical transcripts — so the legacy leg of that test now pins `off` explicitly instead of `""`. The call-site comment ("ships dark") and `.env.example` header were rewritten to match; `TALONSTUDIO_TOOL_POOL` (clamped [1,32] resilience bound) got its documented line in the same pass.
- `TALONSTUDIO_PATCH_BUFFER` (`orchestrator/patchBufferOn`): unset now means the try_edit interception lane is on (type-breaking edits auto-revert); `0/false/off` restores the legacy benign fallback. `RunOptions.PatchBufferEnabled` stays as a force-on OR — production never sets it, so env is the single control. `TestPatchBufferOffOwnsNothing` pins the opt-out explicitly and the new `TestPatchBufferDefaultOn` pins the default both ways.
- **Deferred by gate, not by mood:** `TALONSTUDIO_SUBAGENTS` waits for W12 spend caps (flipping it multiplies cost — pool=2 and 429 cooldown are not spend control); `TALONSTUDIO_SESSIONLOG=derive` waits for the two green CI weeks its own W2.2 gate requires. Both conditions are recorded in register row 23 so neither decays into folklore.

**W8.2 — dead packages got decisions:**
- `harness/ralph` **deleted** (plan's explicit either/or — we took "delete with a note"): zero non-test importers, no W-phase consumer (W14 eats `composition`/`settings` only). The note lives in `PLAN_HARNESS.md §H14` including the recovery command (`git show e787549:backend/internal/harness/ralph/ralph.go`); `evals/phases_test.go` lost its `TestPhaseH14Ralph` block with a comment pointing at the note.
- `harness/goals` **declared headless-only** in its package doc: sole non-test importer is `cmd/talon-headless` ("continue the goal" after a crash); exposing goals to server sessions is named as an open product decision, not left as an accident.
- `harness/composition` stays, as the plan intends (W14.2 boot file) — but under the new law's only exception, whose text carries the reason and the closing gate.

**W8.3 — the flag surface is configuration, not folklore:** every `os.Getenv`/`os.LookupEnv` literal in non-test code was diffed against the union of root + `backend/.env.example`. Added: `AI_MAX_ROUNDS`, `ROUTING_POLICY_JSON`, `ROUTING_PRICING_JSON`, `TALONSTUDIO_HEADLESS_PROMPT`, `TALONSTUDIO_SANDBOX`, `TALONSTUDIO_TOOL_POOL` (plus the two flipped flags from W8.1). The `TALONSTUDIO_EMBED_*` trio read via named constants (`Getenv(EnvURL)`) — verified by hand, they were documented honestly all along. Residual undocumented vars are deploy secrets outside the plan's `TALONSTUDIO_*`/`AI_*` wording (AUTH_* endpoints, R2_*, keystore paths, `TALON_HOME`/`TALON_PLUGINS`) — recorded in row 25 rather than silently skipped.

**The law, permanently:** `evals/TestPhaseW8NoDeadImports` walks `internal/harness/*` and fails `make evals` on any package with zero non-test importers — unless it's in `deadPkgExceptions`, where every entry must carry a reason AND a closing gate. The test also fails *stale* exceptions (package gained an importer, or was deleted) so the allowlist cannot rot into a second folklore. Intra-harness importers count as consumers (the plan says "zero non-test importers", full stop).

**Gates:** `TestPipelineTranscriptParity` + full orchestrator/api/evals/tools suites green under both flips; `TestPhaseW8NoDeadImports` green (composition sole exception); evals suite green after the ralph excision; gofmt clean on all touched files (whole-file on the pre-existing-dirty `failure_index_test.go`, touched by the flip); `go vet ./...`, `make evals`, `go test -race ./...` as the closing battery.

**Honest deviations:** (a) the plan's law text has no exception mechanism — composition is staged by the plan itself for W14, so a *reason-carrying* exception reconciles law 8 with W14.2 rather than deferring the law to W14; (b) the audit's alias-read blind spot (`Getenv(someConst)`) was closed by hand for the one package that uses it (`internal/embed`); (c) `patchBufferOn`'s env switch accepts any unrecognized value as "on" post-flip (the truthy set semantics inverted with the default) — typos fail toward the shipped behavior, not toward a surprise legacy path.

**Web/mobile connection:** both flips are server-internal scheduling/posture changes with proven transcript parity — the web and mobile clients see byte-identical message shapes, so neither needs a change. Two user-visible ripples are *product* signals, not wire breaks: a fleet-wide `TALONSTUDIO_SANDBOX=read-only` shows up as refused writes (both clients already render tool errors), and the patch-buffer lane's `Reverted: <reason>` appears on `tool_result`s the clients already display. `TALONSTUDIO_HEADLESS_PROMPT` is aimed at CI/daemon wrappers of the headless binary — the same knob a mobile "run task" shortcut would set when it shells out.

## W9 — One permission authority (OpenCode-class permission model)

**The authority:** `internal/harness/approval` now owns the whole story — `Mode` (`read-only | ask | accept-edits | auto`), `ParseMode`/`ResolveMode`/`ProjectMode`, the standing-grant file, and the seam's decision order. One `Seam.Ask` runs: composition's `PolicyNever` (absolute, headless fail-closed first) → **read-only** refuses with a structured reason → **auto** allows minus hard-deny → **accept-edits** allows the edit class (`sandbox-escalation:workspace-write`, `file-edit`) → **standing-grant replay** → today's answerer prompt. With mode empty, flag off and no `HardDeny` hook, every branch is a no-op: the pre-W9 answerer loop runs byte-identically (pinned by the untouched `approval_test.go`).

**Composition, not a fourth mechanism:**
- *Modes over sandbox:* `newBuildSession` resolves `permission_mode` once — request-body override over `.talon/config.json`, default ask — and a read-only posture downgrades the capability itself (`sandbox.New(ModeReadOnly, …)`), so writes get the existing structured refusals. The same resolved mode rides `RunShell.Permission` into every tier-2 escalation (`sandbox.Escalation.Permission/Workspace` → `approval.Request`).
- *Modes over the seam:* `approveUserAction` (push/export) resolves the project posture per call — read-only → `Rejected` → the handlers' existing 403 with the seam's reason; accept-edits/auto never auto-allow a button press (`IsHumanInitiated` guard: the person is already standing there).
- *Modes over the device-build gate (W9.2):* `RunOptions.Approval`/`PermissionMode` reach `guardrail` — `deviceBuildApproved` asks the seam (one Request, one `ApprovalDecided` event, the turn answerer reads `turn_approvals` = unchanged binding record). `auto` deliberately does **not** build an APK the user never green-lit: device-build is per-turn consent in every mode; read-only refuses even a granted approval. Nil seam (bare harness) = the old table read, byte for byte.
- *Hard-deny over the shell classifier (W9.4):* `seamHardDeny` (api) parses the exact command out of the escalation Action (`tools.CommandFromAction`) and defers to `tools.BlockedReason` — the one `tierBlocked` definition (secrets, host, network). It runs before auto **and** before grant replay, so no mode and no allowlist entry can waive it.

**Standing grants (W9.3):** `TALONSTUDIO_STANDING_GRANTS` truthy → an `AllowedOnce` for a model-initiated scope persists `{scope, action, sha256(action)}` into workspace `.talon/approvals.json`; the next identical ask replays with a `standing-grant replay` audit reason and no prompt. Exact scope+action only (never prefixes), hash-pinned like plugins (tampered entry → fails closed to a prompt), human scopes never recorded/replayed, a malformed file is never silently replaced, and flag-off never reads or writes it — today's one-shot contract intact.

**Gates:** `approval/TestModeMatrix` (the plan's matrix row for row: read-only structured refusal + no prompt; ask = exactly one audit event; auto allows without prompting; auto hard-denies `.env` exfil *even with a seeded grant*; accept-edits allows edit-class/prompts the rest; `PolicyNever` absolute) + `approval/TestStandingGrantLifecycle` (record→replay skips prompt; flag-off asks every time; tampered hash prompts; human scope unrecorded; hard-deny beats grant) + api's three composition tests + orchestrator's two device-build tests — all inside `make evals`/`go vet`/`go test -race ./...`.

**Honest deviations:** (a) the plan's "policy composition, no fourth mechanism" landed as mode-awareness *inside* the seam rather than a wrapper layer — the seam was already the single decision point, so it composes or nothing does; (b) `accept-edits`' edit class = the workspace-write escalation rung + a reserved `file-edit` scope (the plan didn't enumerate one, and no file-edit seam scope exists yet); (c) auto is stricter than "allow everything" for device-build (plan's matrix is silent; consent-preserving choice, documented in the gate); (d) hard-deny reuses `tierBlocked` instead of a second pattern list — the hook only classifies shell-shaped Actions, and non-shell acts keep their pre-seam protections; (e) the session override is per-request (clients resend it), not server-persisted — session state stays where it already lives.

**Web/mobile connection:** the wire change is one optional additive field — `permission_mode` on the prompt request; absent = today's behavior for old clients, and neither client needs an SSE/response change (all decisions surface as tool errors/`approval is refused` messages they already render, plus the existing `GET /v1/projects/{projectID}/audit` trail). A mobile "safe mode" toggle or a web project setting maps directly: project posture = committed `.talon/config.json` (diffable next to the code it governs), session override = the field per request. `TALONSTUDIO_STANDING_GRANTS` and `.talon/approvals.json` are server/deploy knobs — operators review standing grants in the same commit review as the commands they trust.

## W10 — Hooks: determinism as a product feature

**The mechanism (`internal/hooks`):** the plan's ten-event taxonomy (`PreToolUse, PostToolUse, PostToolUseFailure, UserPromptSubmit, TurnStart, TurnStop, SessionStart, SubagentStart, SubagentStop, ApprovalRequest`) with the decision contract `{decision: allow|deny|ask, reason?, additionalContext?}`. Handler types: **shell commands** (stdin JSON payload, stdout JSON decision; exit 0 = proceed — empty stdout = allow, exit 2 = block with stderr as the reason; any other exit, spawn failure, timeout, invalid JSON, or unknown decision word = extension failure the caller fails closed with) and **in-process Go handlers** — the three built-ins, which prove the pattern: `block-env-edits` (sync gate on path mutators `write_file/edit_file/multi_edit/delete_file/try_edit` when the basename is a real `.env*`; templates — `.env.example/sample/template/dist` — stay editable and the deny reason points at them), `run-tests-after-write` (async-forced; needs `command` = the project's test command), `slack-on-turn-fail` (async-forced; needs `webhook`; POSTs `{"text":…}` only when TurnStop's outcome is failure). HTTP/MCP handlers come later behind the same `Handler` interface, as the plan stages them.

**Config & flag:** `.talon/config.json` gains a `"hooks"` object — the same file W6 (`verify_gates`) and W9 (`permission_mode`) already read. Entry fields: `command` XOR-driven with `builtin` (a builtin carries *its own* parameters and rejects fields it does not use — a typo'd parameter fails the whole config loudly), plus `name`, `async`, `timeout_ms` (default 10s for commands), `tools` (tool-name allowlist so run-tests fires only on writes). `TALONSTUDIO_HOOKS` truthy (`1|true|on|v1`) gates loading; absent flag, empty workspace, no config, empty `hooks`, or any invalid entry/unknown event key → `Load` returns **nil** (an invalid hook config disables hooks entirely and logs — partial enforcement would be a silent lie). Nil registry = `Check` returns allow with zero I/O: the pre-W10 path, byte for byte.

**Decision discipline:** sync entries run inline in config order — first deny wins outright (reason = the hook's), ask is the fallback, `additionalContext` from consulted hooks concatenates. **Async entries spawn from `Check` itself** and their decisions never gate (a fire-and-forget cannot un-happen what already ran); their executions still audit from their own goroutine under a 10-minute ceiling (or the entry's own timeout). Sync failure = extension failure (`ExecutionError` naming hook+event).

**The seven wiring points:**
1. **PreToolUse** (`executeTools/runCall`, right after guardrails): deny → `EmitGuardrail("project hook", reason)` + failed `tool_result` "Blocked by a project hook: …" (the model reads exactly what a guardrail refusal teaches); ask → the turn's approval seam (`AllowedOnce` proceeds; any other outcome, or a nil seam, refuses with it named — fail closed); extension failure → block this call *and* latch `SessionState.SetHookStop` → the round loop closes the turn at the next boundary after `flush()` — `HookStop=true`, honest FinalText naming the hook, **nil error**, `logTurn("hook-stop")` (the budget-stop template: extension failure never corrupts the loop, it ends the turn).
2. **PostToolUse/PostToolUseFailure** (after dispatch): the call already happened, so sync feedback is *context, not a gate* — deny/ask/`additionalContext` append to the result as labeled `[hook]` lines the model reads next; a sync failure latches hook-stop and says so on the result; async entries (run-tests) spawn after Write without delaying the round.
3. **TurnStart** (top of `RunAgentLoop`, after `outcome`/`logTurn`): deny, ask, or failure closes the turn before round one (ask = fail-closed here: no tool and no seam action exists to approve yet); `additionalContext` extends the system prompt.
4. **TurnStop** (a `defer` on `RunAgentLoop` — the signature became named `(outcome, runErr)` so every exit path — budget stop, hook close, error — classifies honestly): outcome success/failure + reason; notify-class, async entries (slack) spawn with `context.Background()` (the loop's ctx may already be cancelled).
5. **UserPromptSubmit** (`runBuild`, before `CreateSession`): deny refuses the prompt through the existing `friendlyAIError` paths — it is never stored or sent; failure of the hook itself also refuses (failed closed); `additionalContext` rides along as a labeled `<hook-context>` block appended to the prompt.
6. **SessionStart** (`newBuildSession`, right after `hooks.Load`): notify-class — the decision is recorded, never gates (the session has not yet become a turn); entries marked async return immediately.
7. **ApprovalRequest** (inside `Seam.Ask`, after `PolicyNever` + the hard-deny computation): deny → `Rejected` with the hook's reason; a **sync hook's** allow → `AllowedOnce` with a `hook allow (W10)` reason — never under read-only (posture absolute), never over hard-deny; ask and hook failure fall into/short-circuit the existing flow (failure = `Rejected`, failed closed). Three-layer gate keeps hookless flows byte-identical: nil registry → skip; registry loaded but **zero sync entries that ran** for this event → normal flow (a nil/empty `Check` also answers `Allow`, so only a hook that actually answered may act — the `syncRan` counter, regression-pinned by W9's untouched matrix); async entries never gate (`ExecRecord.Async`).

Plus **SubagentStart/SubagentStop** (`childRunner.RunChild`): start deny/ask/failure refuses the spawn (`Result{Errored}` — the parent model reads the reason through the delegate tool, after SessionStart already fired in `build()`); stop is notify with the child's outcome (err/stalled/hook-stop = failure) on `ctx` (the child turn may already be over).

**Audit:** new `events.HookExec` kind (`hook/exec`, non-surface) + `events.HookPayload{event, hook, decision, reason, tool, async, error, duration_ms}`. Loop dispatches append via `RunOptions.EventLog` (`orchestrator.appendHookExec`, nil log = quiet); api-layer events (SessionStart, UserPromptSubmit, Subagent*) append via `api.appendHookRecord`/`appendHookExecToLog` using `openSessionLog` (approvalAudit's open-append discipline); approval-layer executions ride the approval record itself — its reason names the hook — because the seam carries no log handle. The hooks package *always* prints the same execution to the server log (`logExec`), so nothing is invisible even with no session log.

**Gates:** `evals/TestPhaseW10HookDeny` (block-env-edits denies `.env`: zero `Execute` dispatches, "Blocked by a project hook" + `.env.example` in the model-visible messages, exactly 2 model requests — refusal reaches the model and it gets to answer, one denied `hook/exec` row), `evals/TestPhaseW10AsyncHookRunsAfterWrite` (turn returns in <900ms while the hook sleeps 1s; the row is stamped *after* the write and carries `duration_ms ≥ 900` — the plan's "log timestamps" clause), `evals/TestPhaseW10HookFailureClosesTurn` (exit-3 hook: zero dispatch, `HookStop`, FinalText names the failure, **exactly 1 model request** — the turn closed before round two could dispatch, error row audited). Plus `hooks/` unit tests (flag truth-set, invalid-config-disables-all incl. both/neither/unknown-builtin, decision contract with exit-2/stderr, contract violations as errors, tool scoping, nil byte-identical, async-spawns-and-never-gates, both built-ins' attributes) and the full approval/orchestrator/api suites proving W9's flag-off behavior stayed byte-identical.

**Honest deviations:** (a) "Go plugins" landed as the in-process `Handler` interface + the three built-ins — no `plugin.Open` .so loading (platform-fragile; workspace-trust code already runs as shell commands, the same trust class); HTTP/MCP stay staged behind the same interface per the plan; (b) post-event deny/ask are appended as labeled context on the result rather than gating — the write already happened, so the honest contract is feedback the model reads next, which is also how the plan's `additionalContext` field is defined; (c) ApprovalRequest executions audit through the approval record (reason names the hook) instead of a `hook/exec` row — the seam has no log handle, and duplicating the event would double-count decisions; loop and api-layer dispatches carry real `hook/exec` rows; (d) TurnStart's `ask` fails closed (there is nothing to approve yet) — the plan's contract doesn't say, symmetry with deny chosen; (e) async hook failures log (server log + audit row) but never close a turn that already moved on — only *sync* failures close the turn, which is the only kind the DSH clause can mean; (f) SessionStart's decision never gates (notify-class) — `deny` there would need a "refuse to construct a session" path that no caller has, and UserPromptSubmit is the prompt's real gate; (g) hook config validation fails *all-or-nothing* (any bad entry disables every hook + logs) rather than dropping just the bad entry — dropping silently would leave a workspace believing a deny is armed when it isn't.

**Web/mobile connection:** zero wire change — hooks are committed workspace config (`.talon/config.json`), and every refusal reaches users through surfaces the clients already render (failed `tool_result`s, the prompt-error path, the audit trail). The audit rows ride the existing session-log replay and SSE event stream (additive non-surface kind — old clients ignore unknown kinds). A future "Hooks" panel in the web app or the mobile project settings is a pure config-UI over the same JSON; `slack-on-turn-fail` is itself the notification-shaped example the plan wanted — the same shape a companion-app push notification would take if a client subscribes to `hook/exec` later.

## W11 — Open seams: MCP + plugin tools

**MCP client (`internal/mcp`):** `.talon/config.json "mcp.servers"` (the same file W6/W9/W10 read) declares servers: **stdio** (`command` argv + `env` additions — the process runs in its own process group so `Close`'s SIGKILL takes wrapper children like npx's, handshake bounded at 15s, the last 16 stderr lines ride back inside every dial/handshake-death error) or **http** (`url` + `headers` with `${ENV}` expansion — see deviations re: OAuth; replies parsed as either `application/json` or a single-event `text/event-stream` frame, notifications on the shared stream skipped by id). The client speaks JSON-RPC 2.0 `initialize` → `notifications/initialized` → paginated `tools/list` → `tools/call`; server-initiated *requests* get an honest `-32601 method not supported` reply (Talon implements no reverse channel); a non-http(s) scheme is refused at `Dial` too, not just at `Load`. `Load` validates per server (name shape, unique names, transport derivable, argv/url present), skips bad entries with a log line — MCP is additive capability, one bad server degrades to that server missing (contrast: hooks' invalid config disables *all* hooks, because hooks are enforcement and a partial deny-set is a lie; a partial server-set is just fewer tools).

**Pipeline join (no second execution path):** `api.registerMCP` dials + lists at session construction and registers each remote tool as `mcp__<server>__<remote>` via `tools.MCPTool` — a normal `tools.Tool`, so `validateArgs`, the guard stage (guardrails + W10 PreToolUse/PostToolUse), dispatch pooling, output caps, the audit trail, and the executor's sandbox ring all apply by construction. Policy (`allow`/`deny`) is baked in at registration as the tool's call-time refusal — the tool **stays in the catalog byte-identically** across a deny flip (H3: refusal, not hiding; pinned by `api/TestPhaseW11MCPCatalogStable` marshalling defs across a config flip). `mcpPostureRefusal` runs in the executor *before* the path-targeted sandbox ring: a `mutate`-allowed tool still refuses under either read-only posture (`permission_mode` first, then `TALONSTUDIO_SANDBOX`), with the reason naming which one; read-only-classified MCP tools pass through untouched (proved both ways in the gate). Names: `mcp.ValidName` drops malformed remote names at registration; `SchemaOrObject` swaps a missing/malformed `inputSchema` for an empty-object schema so validateArgs never rejects good calls against garbage. Lifecycle: per-request servers like the per-request registry — `bs.mcp` closers, `closeMCP()` deferred in `runBuild` and `childRunner.RunChild` (idempotent, nil-safe).

**Plugin tools (item 2):** `pluginManifest` gains `"tools": [{name, description, input_schema, command, mutating?}]`. `RegistryProvider.PinnedTools(pins)` mirrors `LoadForPins`' failure posture (index unreachable → cache-verified declarations; one bad plugin skips with a log line; nothing fails the session): it fetches the pin-exact manifest (drift serves cache, `loadPlugin`'s adoption-hint error when cold), then materializes **every** hash-pinned file through the fetch→sha256→cache path before declaring anything — the cache write *is* the supply-chain boundary, and a mismatch fails that plugin's tools closed (per-plugin: `skills/TestPinnedToolsTamperFailsPluginClosedOnly` pins that the sibling plugin's tools survive). `api.registerPluginTools` registers each decl as `plugin__<plugin>__<tool>` (`tools.PluginTool`): direct argv exec (no shell added) with cwd = verified cache dir, JSON args on stdin, stdout as the result, 1MB output cap, nonzero exit = `is_error` carrying stderr — W10's shell contract applied to tools. Read-only always: a `mutating` declaration skips loudly (no workspace allow path exists for plugin mutation — MCP's `mutate` list is server-scoped; see deviations).

**Gates:** `evals/TestPhaseW11MCPThroughPipeline` (catalog sorted + byte-stable across listings with the fixture schema riding through; execution lands `ok:echo:…` in the conversation with exactly one server call; schema violation refuses at validateArgs with zero server calls and a model-visible "did not run"), `evals/TestPhaseW11MCPGuardCoverage` (guard stage refuses an MCP call exactly like a native one: model-visible reason, `guardrail_reject` emitted, `hook/exec` audited deny, fixture untouched, 2 requests — refusal feeds the loop), `api/TestPhaseW11MCPCatalogStable` (sorted; byte-equal defs across a deny flip; deny/pipeline/posture refusals each with zero server calls; flag-off registry has no `mcp__`), `api/TestPhaseW11PluginToolPinnedOnly` (pinned → registered read-only with valid schema; unpinned → absent), `mcp` package tests (flag gate, validity/dedup/derivation, `${ENV}` header expansion, http round-trip + SSE frame decode + scheme refusal, stdio through the **real test-binary subprocess** — including dial-failure stderr-tail capture and double-Close safety), `skills/TestPinnedTools*`, `tools/TestMCPTool*`/`TestPluginTool*`.

**Honest deviations:** (a) the plan's "**OAuth** where remote" landed as static per-server `headers` with `${ENV}` expansion (operator attaches a token from their own OAuth dance) — a full interactive/dynamic OAuth client (registration, device flow, refresh, token storage) is a project of its own with real secret-handling surface; recorded here as the explicit seam where it would attach (Dial/`headers`); (b) **mutating plugin tools** skip at declaration time rather than getting a workspace allow path — the plan grants the allow path only to MCP tools ("mutating MCP tools require explicit allow"), and inventing a plugin-side escalation without the plan naming it would be the wrong default; `mutate`-style config for `plugin__` names is the obvious follow-up; (c) MCP servers **dial per session-construction** (accepted for the per-request registry's isolation; `tools/list` costs a handshake per request — keep-alive/connection reuse across prompts deferred with the rest of session pooling); (d) "guardrail coverage" in the eval gate is asserted through the guard stage's observables (`guardrail_reject` + `hook/exec` + zero dispatch) because the *built-in* guardrail rules key native tool names exactly (`guardrails.Check` switches on `install_package`/`write_file`/…) — by design MCP's policy/posture enforcement lives in the tool and the executor, and the stage in front of dispatch is what must (and does) cover remote calls; (e) `Load`'s invalid-*entry* skip (vs hooks' all-or-nothing) is deliberate for the reason in the first section: additive vs enforcement.

## W12 — Isolation & parallelism: children become safe to scale

**Child write isolation (W12.1):** `TALONSTUDIO_CHILD_ISOLATION` selects the posture per session — unset/`readonly` (default) keeps children exactly as they were (read-scoped registry over the shared workspace; byte-identical), and a delegate call declaring `mutating: true` refuses at the Service seam with `DELEGATION_ISOLATION` *before* reserving a pool slot, run, or record. `worktree` opts mutating children into `worktree.Overlay`: a private detached checkout (`git worktree add --detach <tmpdir> HEAD`) whose root becomes the child's build-session root — session FS, sandbox ring, hooks, approval posture, and MCP config all key on that root (`.talon` is copied in at Open because untracked files never reach a checkout). Dispose is three staged and always-cleans-up: **capture** (the worktree's own per-worktree index `git add -A` + `git diff --cached --binary`, so creations ride), **apply** (`git apply --binary --whitespace=nowarn` against the parent working tree — a disagreement is `CHILD_WORKTREE: merge apply failed` with the patch in the error, parent state untouched), **verify** (a reverse `--check` of the same patch proves it landed); then `git worktree remove --force` (+ prune/RemoveAll fallback) — on success, on conflict, on child failure (failure *discards*: a broken child's partial state never touches the parent), and on any early return (defer'd discard). Parallel merge-backs serialize per workspace (`wsLocks`) so two children's patches apply in any order without interleaving git metadata. Child builds gained `childBuildFor(root, mutate)`: it **Removes** `delegate`, `delegate_fork`, `list_children`, `run_workflow` from the child's registry (the old filter left them unadvertised-but-executable — depth-1's second wall is now true, law 12), and applies the read-only def filter only when `mutate` is false.

**Caps, law 10 (W12.2):** the Service (one per turn — its lifetime *is* the accumulator's window) now bounds **spend** (`SetSpendCap` / `TALONSTUDIO_CHILD_SPEND_CAP`, default `$2`, 0 = unlimited: each completed child's `BuildOutcome.CostUSD` — priced from `ROUTING_PRICING_JSON` — accrues in `finish`, and `reserve` refuses at the cap with the spent/cap figures) and **wall-clock** (`SetWall` / `TALONSTUDIO_CHILD_WALL_SECS`, default 600s, 0 = unbounded: every spawn runs under `context.WithTimeout`, and a deadline death is reclassified to `DELEGATION_WALL` unless the child genuinely finished). Depth 1, the pool, and the H9 429 cooldown were already at the same seam — `reserve` is now the *single* gate every structured check passes through, shared verbatim by the sync and background paths. The failure index gained three `delegation_` patterns inserted *before* the generic throttle row (first-match order is load-bearing: "back off and retry" is wrong advice mid-cooldown or against a spend cap).

**Background children (W12.3):** `delegate` defaults to background — `SpawnBackground` reserves synchronously (every refusal immediate; the running `Child` record exists before the call returns), then runs the loop detached under `context.Background()` + the wall budget. `finish` lands report/rounds/error **durably on the record**, and `list_children` renders finished children through the same `RenderResult` frame the sync path uses (BEGIN/END CHILD REPORT markers — child text is data on both paths). `delegate_fork` stays inline (see deviations). `run_workflow` (W12.4) needs no change: the engine calls `Service.Spawn`, so pool/spend/wall bound it by construction and phase narration is untouched.

**How it connects:** the web/mobile "Delegated" trace section (H9's frontend tail) reads the states the service now exposes — a client polls `list_children` (or renders the delegate ack) and shows `running → done/failed` rows, expanding a done row's framed report; the `mutating`/`background` switches are optional inputs on `delegate`'s schema, so existing prompts without them behave (background defaults on; mutating defaults off → readonly refusal only when explicitly declared).

**Gates:** `api/TestPhaseW12MutatingChildrenIsolated` (readonly refusal with zero side effects; non-git workspace → structural `CHILD_WORKTREE` refusal from the runner; two parallel mutating children → disjoint diffs, *both* `shared.txt` line edits survive alongside 12-line integrity (no lost update), plus worktree registration + temp-dir cleanup), `evals/TestPhaseW12SpendCapStopsRunawayFanout` (phase 1 fills the $0.50 cap, phase 2 all-null, parent result keeps `checks (2 results, 0 dropped); more (2 results, 2 dropped)`, next spawn = `DELEGATION_SPEND` with figures), `evals/TestPhaseW12BackgroundStatusVisibleInListChildren` + `tools/TestDelegateDefaultsToBackgroundAndListCollectsReport` (ack without report → `running` → `done` + framed report through the tool), `subagent/TestMutatingChildRefusedUnderReadonlyPosture` + `TestSpendCapStopsRunawayFanout` + `TestWallClockBoundsChild` + `TestSpawnBackgroundDurableReport`, `tools/TestRegistryRemoveTakesToolOutOfCatalogAndDispatch`, `knowledge/TestLookupDelegationRefusals` (all six `DELEGATION_*` codes → their known fixes, cap family beating the generic throttle), `worktree` package (apply/conflict/discard/no-change/.talon-fidelity/cleanup-on-failure).

**Honest deviations:** (a) `delegate_fork` remains inline-by-default — the plan names `delegate` for the background flip, and a fork's contract is "continue this analysis", which reads inline (the same `background` param can be added to fork later without schema churn); (b) a **failed** child's overlay is discarded, not merged — applying a broken child's partial state to the parent is the lost-update class this feature exists to prevent (a successful child whose merge then conflicts *does* report the patch in the error); (c) the worktree carries **tracked files + `.talon`** only — untracked dependencies (`node_modules`, build outputs) are absent, so a mutating child that needs them must install/build first (full copy-on-write fidelity is the plan's alternative and costs disk per child); (d) "durable" report = session-lifetime in memory — the Service dies with the session, so a server restart does not resurrect a background child's report (persisting `Child` records to the store is the natural follow-up if cross-restart collection is wanted); (e) spend accounting is honest only when `ROUTING_PRICING_JSON` prices the served model — unpriced children accrue $0 and the cap never trips (stated in the env docs); (f) the trace's "Delegated" section **rendering** is client work (H9 frontend tail) — the server exposes every state and report it needs through `list_children`; (g) worktree add/merge concurrency serializes per workspace — a deliberate throughput bound, not a correctness hack.

**Web/mobile connection:** zero wire change — MCP/plugin tools are ordinary catalog entries with additive `mcp__`/`plugin__` names, so the web app's tool-call rendering and the mobile companion's transcript views need no new cases (an unknown-tool renderer already draws them from the catalog); refusals are standard `is_error` tool results the clients already surface, and the guard-stage deny rides the existing `guardrail_reject` SSE event. All configuration is committed workspace JSON (`.talon/config.json`, `.talon/plugins.json`) — diffable in the same review as the code it governs, and the natural target for a future web "MCP servers" settings panel or a mobile read-only view. Server health (dial failures, skip logs) surfaces through the server log and, for executed tools, the existing session-log/audit trail.

## W13 — Agent evals: behavior becomes a gated diff

**Golden transcripts (`internal/evals/golden`):** six recorded tasks (scaffold-weather-app, plan-existing-feature, fix-broken-import, wallet-connect-flow, steer-mid-build, resume-after-crash) replay through the **real** `RunAgentLoop` — real registry, guard stage, W10 hooks, session FS — against a recorded provider that serves the exact raw request bodies captured from a real run (sha256 digest per request, temp workspace root normalized to `<workspace>`). The system prompts in replay are the production consts (`ai.SystemPromptBuild` / `ai.PlanSystemPrompt(nil)`), so a prompt edit trips the digests exactly as a tool/guardrail edit changes the transcript. Each `*.golden.json` pins a `Transcript` of **invariants only, never prose** — task, outcome, rounds, tools invoked, files touched, request digests (asserting model wording would flake on sampling instead of catching regressions). `CheckCriteria` runs two channels — log lines (must appear / `!`-prefixed must-not) and the workspace diff (markers that must/must not land in touched files) — plus the existing `evals.Grade` fixtures where a task ships one.

**Three gate clauses (all green; suite 0.21s):** `TestGoldenSuite` (every task replays within criteria, <3min budget), `TestGoldenReplayDeterminism` (the same task replayed twice is byte-identical — the suite proves itself deterministic before it is trusted), `TestGoldenMutationDetection` (two synthetic reds must go red: a prompt drift and a behavior regression — the suite proves it can actually fail). Re-record flow: `TALONSTUDIO_UPDATE_GOLDEN=1 go test ./internal/evals/golden/ -run TestGoldenSuite` rewrites the goldens; CI never sets it and the suite's tree guard fails if a run would dirty the working tree — so **behavior change can only land as a reviewed `*.golden.json` diff in the PR**, which is the agent-behavior gate the plan asked for.

**W13.3 — agent metrics (`internal/metrics`):** five observers wired at their production seams — `ObserveAgentTurn(rounds, buildVerified, costMicroUSD)` (`api/prompts.go`, right after `ObserveTurn`), `ObserveToolCall(isError)` (orchestrator `runCall`, declared *before* the recover so a panicking tool still counts), `ObserveGuardrailReject()` (the guard's deny branch), `ObserveSpill()`/`ObservePrune()` (context-pressure paths), and context-pressure sampling inside `ObserveTurn`. Fourteen Prometheus families: `talon_agent_rounds_*`, `talon_agent_rounds_to_green_avg`, `talon_agent_tool_error_rate`, `talon_agent_guardrail_reject_rate`, `talon_context_spills|prunes|pressure_*`, `talon_agent_cost_per_verified_build_usd`, … Per-session surfacing already existed (`rounds`/`verified` in turn responses + the H13 meter); these are the fleet view over the same numbers.

**W13.4 — retrieval grade:** already closed by W4 — `evals/retrieval` pins a gold-path query set, hit@5, a grep-only baseline, and a non-vacuous floor (≥6), running inside `make evals` so recall is tracked over every change. No new code; documented as the plan's W13.4 evidence.

**W13.5 — nightly scorecard publish:** `make evals` gained the named `golden` stage (`vet scorecard golden test`) and CI a named "Golden transcripts (agent-behavior gate)" step; `make nightly` → `e2e/run-live.sh`, which exports `E2E_REPORT` (default `e2e/reports/nightly-latest.md`, gitignored, dir pre-created) and `cmd/e2e`'s `report()` now writes the scorecard markdown there on **every** exit path — the nightly run leaves a durable artifact beside the last green. CI stays secret-free by law (no AI key/device): golden + scorecard gate every merge from committed fixtures; the live nightly runs locally via `make nightly`. The rounds-to-green regression gate = the fixtures' `max_rounds` + the golden rounds pin — a red blocks merge.

**Web/mobile connection:** zero wire change — goldens and metrics are server-side gates; clients already render `rounds`, `verified`, and cost from turn responses, and Prometheus is scraped rather than surfaced. The natural client follow-up (a "quality" sparkline over `talon_agent_rounds_to_green_avg`) reads the same families any dashboard would.

**Honest deviations:** (a) `plan-existing-feature` ships **no grade channel** — that task writes no files (log + diff only), stated in the task file; (b) `ObservePrune` counts **H8 per-output pruning only**, not conversation compaction (no distinct seam exists yet); (c) `ObserveGuardrailReject` counts **deterministic guardrail denials only** — W10 hook denials stay in the `hook/exec` audit, not this counter (one metric, one meaning); (d) tool-error rate includes refusals — they share the `runCall` denominator because a refusal *is* an error from the loop's view; (e) nightly publish is a **local artifact** under the secret-free-CI / no-`gh` constraint — `E2E_REPORT` is the exact seam where a CI upload would attach later; (f) request digests are **raw-body sha256 with the workspace root normalized** — any other environment text (home paths, seeded-config timestamps) forces a re-record, which is the intended friction.

## W14 — Finish the loop extraction (H2 end-state, composition as data)

### W14.1 — cross-cutting calls become listeners (`TALONSTUDIO_LOOP=driver|legacy`)

**What landed:** a new `internal/orchestrator/looplisteners.go` makes PLAN_HARNESS §2.4's table concrete — the six cross-cutting calls the loop body used to make inline now flow through named seams, and the loop file keeps claim → pre-step → request → append → dispatch → append → close:

- `guardrail.Check` → the `toolPolicy` seam = the **ToolPolicy** event: built-in deterministic guardrails first (unchanged, byte for byte), then — driver only — composition listeners (first veto wins, refusal renders as a native guardrail block: `Blocked by a project guardrail (<rule>): <reason>`).
- `MarkSkillSeen` / auto-attach → the `skillDelivered` seam (**ToolDispatch**, `Stage:"skill"`) + `autoAttachSkills` moved here verbatim.
- `NoteFileWrite` nudge → the `fsWrite` seam (**FSWrite**, one event per successful write with a parsed path; all legacy bookkeeping completes before the publish).
- `EmitToolCall` running/completed → `emitDispatch`/`emitResult` (**ToolDispatch** `Stage:"start"` / **ToolResult** carrying the post-spill/prune output and `IsError`).
- verify scheduling → `enforcePeriodicVerify` moved here verbatim (the FSWrite counter listener's built-in — still a direct batch-boundary call: its inputs are the whole batch, writes AND passing checks).
- the failure-index reminder stays in `failurefeed.go` (its own file) reacting at assembly where the final post-hook output exists; the `ResultEvent` observers get is the emit-time output.

**Flag:** `TALONSTUDIO_LOOP` — only `driver` (or the house truthy set `1|true|on|v1`) arms the bus; `legacy`, `off`, `0`, a typo → today's path (fails *closed* to legacy — a migration-flag typo must not enable the new architecture). Read once per batch/turn, so a mid-flight env change can't split one batch across polarities. Subscriptions registered while the flag is off are silently inert (documented; that's the wrap stage's contract).

**Gates (all green):** `TestLoopDriverTranscriptParity` — same scripted provider through both polarities with the same listeners registered → identical transcripts byte-for-byte, zero events published under legacy, every §2.4 row observed under driver; `TestLoopPolicyListenerVetoes` — a veto registered from a NEW file refuses without touching loop code (the DSH extensibility test; the composition-row half lands with W14.2) and is inert under `legacy`; `TestLoopListenerPanicIsolation` — a panicking policy listener **fails closed** (refusal names the failure), a panicking observer is logged and skipped while the healthy observer still runs; `TestLoopFlagGate` — 13-case parse matrix; and the W13 golden suite re-replays all six tasks under `TALONSTUDIO_LOOP=driver` against the *same* committed goldens — bit-identical transcripts with digests included = H2's eval gate (legacy vs driver produce identical transcripts on the fake provider).

**Web/mobile connection:** zero wire change — the bus is server-side boot wiring; SSE event shapes and turn responses are untouched (the parity gate is the proof: the transcript does not move). The natural client follow-up is W14.4's `--dump-config` surfacing as a web "composition" settings panel or a mobile read-only view — rows are already structured data.

**Honest deviations:** (a) this is the **wrap** stage — built-ins are extracted but run *direct* in both polarities; the bus adds observers + policy vetoes around them. Built-ins becoming subscribers themselves (emitter-as-listener, the verify counter riding the FSWrite channel) is the *replace* stage, sequenced behind green parity per H2's own migration — flipping it early would change behavior for no gate credit; (b) policy listeners run **after** the built-ins — composition adds guardrails, it never weakens the security-critical ones; (c) listener panic policy: DSH says extension failure closes the turn — we fail the *call* closed instead (policy) / log-and-skip (observers), deliberately reusing the refusal path rather than re-wording W10's hook-stop text, whose wording is pinned by W10 tests — turn-stop-on-listener-failure is the follow-up when composition rows (W14.2) make real listeners routine; (d) `ResultEvent.Output` is the emit-time (post-spill/prune, pre-hook) output — the reminder's post-hook read happens at assembly, kept as-is to preserve byte-parity of what the model is reminded about.

### W14.2 — composition-as-data: the YAML composer lands (H12), with W14.4's `--dump-config`

**What landed:**

- **Loader (`harness/composition/yaml.go`)** — `Load(path, profile, preset)` parses one document with three overlay layers (`base` → `profiles[profile]` → `presets[preset]`), resolves rows **last-write-wins per id with whole-config replacement** (a later row replaces earlier one completely — configs never merge), and freezes **first-appearance order** so apply order and dump order are the same deterministic walk. Missing file → `(nil, nil)` = dark boot byte-identical to before; malformed file / row without id or component → error (wiring you cannot trust must not boot). `MarshalJSON` is the dump shape: base-name path (a golden must not bake machine paths), selected layers, `rows:[]` never null.
- **Apply layer (`internal/composer`)** — the ONE place a row takes effect, shared by `cmd/server` and `cmd/talon-headless`, so there is no second wiring for config-as-code drift to grow in. `Load(defaultProfile)` resolves `TALONSTUDIO_COMPOSITION` (explicit path **fails loud when missing** — a typo must not become a dark boot) or walks up from the working directory (launch from repo root or `backend/`, same file). `Apply(composition, orchestrator)` walks rows in order: `harness/loop` → `SetMaxRounds` (boot-time override of the `AI_MAX_ROUNDS` seed — data beats seed, the W7 settings-document precedent; bounds are the env reader's own, out-of-range fails the boot, no silent clamp), `policy/<name>` → registry → `SubscribeLoopPolicy`; **unknown component = error naming what is known** — never a silent skip.
- **Registry (`orchestrator/policycomponents.go`)** — `RegisterPolicyComponent` / `LookupPolicyComponent` / `PolicyComponentNames` (duplicate registration panics at boot: two components on one name is silent shadowing), plus `SetMaxRounds`/`MaxRounds` (boot-only contract, documented).
- **Shipped component (`orchestrator/policy_denytools.go`)** — `policy/deny-tools`: `config.tools` names the refused tools; refusal renders as `Blocked by a project guardrail (deny-tools): run_shell is denied by the workspace composition` — the native vocabulary, driver-only like every policy row. A config that names nothing is an error (a component that denies nothing is a typo).
- **Composer binaries + the file itself** — `cmd/server/main.go` loads + applies right after `orchestrator.New` and logs `(profile, preset, applied row ids)`; the repo ships **`talon.composition.yaml`** whose base `loop` row states the shipped default (60 — boot behavior does not move by landing the file) and whose `headless` profile overlay caps that profile's budget to 6 (demonstrates the layer for real; CI's headless job gets a bounded budget).
- **W14.4 `--dump-config`** — `go run ./cmd/server --dump-config` resolves the composition and prints it as JSON **before** db/AI/listener open → runs secret-free; byte-stable output committed as **`talon.composition.golden.json`**; gates: `make dumpconfig` (new named stage — `evals: vet scorecard golden dumpconfig test`) and CI step "Composition golden diff (dump-config)" right after the golden-transcripts step.

**Gates (all green):** composition package — overlay precedence with whole-config replacement proven (base keys do not survive an override), first-appearance order stable across LWW, unknown layers fall back to base, dark-boot nil/nil, four fail-loud parse cases, dump determinism (two marshals identical, base-name path, `rows:[]`); composer package — loop row overrides the seed / row-without-key keeps the seed, bad-row matrix (out-of-range, non-integer, unknown component, unregistered policy, nameless policy, deny-tools without `tools` — each error names the row), nil edges (nil doc = dark, nil orchestrator = error), Load resolution (explicit-missing fails, env profile override, cwd-walk from a nested dir), and **`TestPolicyRowVetoesEndToEnd` — the DSH composition-row half**: one YAML row vetoes with **zero Go edits**, the refusal is the native guardrail vocabulary, the same row is inert under `legacy`, and `PolicyComponentNames` lists `deny-tools`. Hand-verified: `GOLDEN_DIFF_CLEAN`, `TALONSTUDIO_PROFILE=headless` resolves `maxRounds: 6`, explicit missing path exits 1.

**Web/mobile connection:** still zero wire change — rows are boot wiring, SSE shapes untouched. The client follow-up named in W14.1 sharpens: `--dump-config`'s JSON (`path/profile/preset/rows`) is exactly the payload a web "composition" settings panel or a mobile read-only view would render — structured, LWW-resolved, byte-stable; until then the file + dump *is* the panel (edit `talon.composition.yaml`, read `--dump-config`).

**Honest deviations:** (a) only **two component families apply today** — `harness/loop` and `policy/<name>`. The plan's base example also lists sandbox/approval/verify/skills/sessionlog rows; those knobs already own live per-project contracts (`.talon/config.json` `verify_gates`, permission modes, env posture — W6/W9/…) and flipping their source of truth to composition rows without each one's own parity gate would be exactly the blind change the plan forbids — so a row for them **fails loudly** (`unknown component`) instead of pretending, and they stage as the follow-up that migrates one contract at a time; (b) the shipped file ships the default budget as a row — behavior-neutral by construction (60 = the env seed's default), but it does mean `AI_MAX_ROUNDS` now loses to the row (data beats seed — documented in `.env.example` and the file header, the W7 doc-beats-env precedent); (c) `Load`'s default search walks up from the **working directory**, not the executable — composition is per-tree wiring (a global install with no tree has no file = dark), which differs from `findProjectRoot`'s executable-first walk used for templates; (d) policy rows ride the driver bus, so they are inert under `legacy` — the composition file can name them safely while the flag migration completes; the round-budget row applies regardless of the flag (it is not a bus feature).
