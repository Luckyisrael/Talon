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
