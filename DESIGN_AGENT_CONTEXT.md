# DESIGN — Agent context: storage, retrieval, injection, memory

**Status:** current, with Phase 1 of the roadmap implemented (turn state card).
**Scope:** how a Talon conversation is stored, retrieved, assembled into a
prompt, remembered across turns, and controlled — the system that decides
what the build/plan model knows at every round.

This is the system design requested after two live failures: a companion-app
prompt that spent 14 minutes / 41 rounds re-reading files without writing a
line (prompt 53, project 7), and a "just add a screen" request that circled
for 12 minutes on a project the model had already worked in. The two
reference documents `TALON_AGENT_IMPROVEMENT_1.md` and
`TALON_AGENT_IMPROVEMENT_2.md` were absorbed in full; their model is mapped
onto Talon's machinery below, with what exists, what was added, and what is
deliberately deferred.

---

## 0. The design rule

The model is stateless. Every round it knows exactly what the request puts
in front of it — nothing more. So the system's job is not "remember the
conversation"; it is:

> **Assemble the right context every round: goal, position, constraints,
> what has been tried, what to do next — and make it cheap to re-derive.**

Both reference documents converge on the same three-way split:

| Layer | What it is | Talon's equivalent |
|---|---|---|
| **Transcript** | The raw record of everything said and every tool result | `conversation_messages` + `session_events` |
| **Memory** | Distilled facts, decisions, constraints that outlive a turn | `project_memory`, `architecture_decisions`, `plan_task_status`, AGENTS.md, skills, ADRs |
| **Context** | The per-round selection: transcript window + memory + injected state | system-prompt sections + wire shaping + tool-result notes + **turn state card** |

A conversation history alone fails in the ways both documents describe: it
grows unbounded, buries decisions under tool output, drifts stale, and gives
the model no next-step clarity. The observed 41-round read spiral is exactly
"history without situation awareness" — the model could replay what it read
but could not see that it had written nothing.

---

## 1. Storage — where a conversation lives

```
client (web / mobile SSE)
   │  POST /v1/prompts/stream
   ▼
PromptHandler.Stream  ──► RunAgentLoop (orchestrator)
   │                          │ per round: flush()
   ▼                          ▼
Persist callback ─────────► conversation_messages   ← CANONICAL prompt truth
        └───────────────► session_events (H1 log)    ← append-only audit / derive
```

| Store | Written by | Read by | Role |
|---|---|---|---|
| `conversation_messages` | `AppendMessage` (`store/conversation.go:124`), via the loop's `Persist` callback (`api/prompts.go:1412-1432`) and `persistTurn` (`:1547`) | `GetConversation(projectID, historyTurns=60)` (`:1867`) | **The transcript the model replays.** One row per message, blocks as JSON (text / thinking / tool_use / tool_result / context / plan / turn_end / verify). |
| `session_events` | `AppendEvent` (`store/sessionlog.go:24`) — same turns dual-written (`api/prompts.go:1424-1430`), plus non-surface events (retries, usage, dispatch) | UI live replay; optional derived read path (`TALONSTUDIO_SESSIONLOG=derive`, `storedConversation` `:1863`) | Append-only audit log; shadow-compaction lives here. Byte-equivalence to the table is proven by bridge round-trip tests; the flag flip is the migration's end state. |
| `prompts` | stream/finalize (`api/prompts.go`) | resume offers, status UI, `plan_json` | Per-turn row: status (`done/waiting/paused/failed`), round count, files_touched, plan JSON. |
| `project_memory` / `architecture_decisions` | `write_memory` / `write_adr` tools | session start → injected into the system prompt | Long-term curated memory (see §3.3). |
| `plan_task_status` | `mark_task_done`, plan seeding — mirrored from the in-memory `TaskTracker` (`session.go:152-156`) | every session start | Durable plan backlog across turns. |
| `checkpoints`, `build_receipts`, `turn_approvals`, `prompt_attachments` | loop / build / answer paths | undo, next-turn receipt injection, resume, replay | Supporting state. |

**Decisions:**

- **`conversation_messages` is the canonical truth** of what the model saw.
  The dual write into `session_events` is not duplication to reconcile —
  it is a projection for replay/audit, with `derive` as its eventual read
  mode. Retention is `TrimConversation` (`store/conversation.go:353`),
  currently **shadow-only** (`TALONSTUDIO_TRIM_SHADOW`): old turns drop out
  of what the model sees without ever being deleted from storage. Turning
  it destructive is a future decision, not part of this design.
- **Every finished round is durable before the next starts**: the loop
  flushes at the top of each round and checkpoints (flush + fsync) before
  any side effect, so a crash costs at most the current round.
- **The turn state card is never stored.** It is derived context, like a
  system prompt — storing it would create a second, drift-prone source of
  "what happened" next to the transcript it is derived from.

---

## 2. Retrieval — what the model sees at turn start

`turnHistory` (`api/prompts.go:1471`) assembles the replay in three shapes:

1. **Fresh turn** — `conversation()` = the turn-aligned window of the last
   **60 rows** (`historyTurns`, `:158`) passed through `TrimToValidHistory`
   (`ai/messages.go:539`: must start on a clean user boundary, end on an
   assistant turn, no orphaned tool_use), then the user's prompt appended
   and persisted up front. Pending questions are carried forward inside the
   prompt (`:1498-1502`), and unread **build receipts** are injected once,
   ack-on-read (`:1508-1527`).

   **Turn alignment** (`store.GetConversationWindow`): the row budget alone
   can cut a long turn in half — a turn writes two rows per round, so after
   a 40-round spiral the last 60 rows hold only that turn's middle. Its
   opener (the goal) falls outside the window and `TrimToValidHistory` then
   trims the fragment forward to whatever user text survived, opening the
   replay on a harness nudge as if it were the user's actual request. The
   window therefore extends its front back to the row where `prompt_id`
   changes — all legs of a turn (fresh, resume, answer) carry the original
   prompt's id — bounded at 4× the budget; a single turn longer than the
   bound degrades to the old tail behavior, never unbounded growth. Pinned
   by `TestConversationReplayOpensAtTurnOpener` (fails by opening on the
   nudge without alignment) and the `GetConversationWindow` store tests.
2. **Answer to a paused `ask_user`** — the raw tail is kept, the pending
   `tool_use` is answered in place (`ai.AnsweredHistory`); the validity
   trim is deliberately skipped so the question survives.
3. **Resume** — stored turns replay as-is plus a synthetic
   `resumePrompt` ("continue from where you left off…", `:411-414`),
   deliberately **not persisted** — it is instruction, not user input.

Images replay as bytes in the wire copy while storage keeps references only.

---

## 3. Injection — the per-round context pipeline

### 3.1 System prompt (cached prefix)

`enrichPromptWithContext` (`orchestrator.go:3219`) assembles an ordered,
fingerprinted section registry (`promptsec`) — a section whose render is
byte-stable keeps the provider's cached prefix; a change late in the order
does not invalidate what sits before it:

| Section | Content | Refresh |
|---|---|---|
| persona | mode prompt + device facts | static |
| rules/core + skills catalog | always-on rules, skill manifest (bodies via `read_skill` only) | static / plugin snapshot |
| instructions/chain | project AGENTS.md conventions | on touch |
| discipline | working discipline (read rules, Maestro opt-in, verify doctrine) | static |
| project/map | file-tree structure snapshot | fingerprint-gated, every round |
| project/known-files | paths read/written **this session** — the anti-reread pre-read half | on change |
| plan/checklist | settled screens/features from `plan_task_status` | on status flip |
| ADRs (≤12) / memory (≤20 × 240 chars) | settled decisions, durable facts | on change |

The rebuild guard (`orchestrator.go:1319-1342` post-edit numbering: counts
of ADRs/memory/task-version/map-fingerprint) only re-renders when something
actually moved — a rebuild that produces identical bytes would still bust
the cache.

### 3.2 Per-round wire shaping (transcript economy)

- **Compaction**: when the conversation estimate exceeds
  `MaxContextTokens = 200_000` (`orchestrator.go:46`), it compacts to ¾
  budget — `CompactWithSummary` asks the model to write the checkpoint over
  the dropped span (summary carries intent, errors, decisions), degrading
  to static trim with no client. The session log is shadowed to the same
  boundary (`api/prompts.go:1437-1447`).
- **Superseded-read trim**: `TrimSupersededToolOutput`
  (`ai/messages.go:627`) collapses a re-fetched `read_file`/`list_files`
  body to a one-line pointer (only the newest fetch keeps bytes; ≥200-byte
  floor; wire copy only — stored transcript keeps full fidelity).
- **Spill / prune**: outputs >256KB spill to disk (re-read by range);
  >8KB prune to head+tail with the omission named (`orchestrator.go`
  tool-execution seam).
- **Prompt caching**: system prompt cache_control + conversation breakpoint
  at `len-2` (`agentic.go:762`) — the last message is always re-sent, the
  prefix is cached.

### 3.3 Memory layers → Talon mechanisms (the 7-layer model)

| Layer (docs) | Talon | Injection mode |
|---|---|---|
| Conversation history | 60-row window, wire-shaped | automatic |
| Session/task state | **turn state card (new)** + plan checklist + `prompts` row | automatic, every round |
| Working memory / scratchpad | turn state card + tool-result notes (see §3.4); `.agent/` files deliberately rejected (§6) | automatic |
| Codebase memory | `project_map` (structure + symbols), `known-files`, `read_skill` bodies | automatic + one round for skill bodies |
| Domain knowledge | skills catalog, rules/core, plugin triggers (auto-attach) | catalog automatic; bodies one round |
| Long-term project memory | `project_memory` (20 rows in prompt, `read_memory` for more), ADRs | automatic (bounded) |
| Tool/action memory | transcript tool_results + probe/repeat counters + failure index reminders | automatic |

### 3.4 Dynamic per-round steering (rides tool results and gaps)

- **Reread note**: "unchanged since round N — do not read it again"
  (on a re-read of an unchanged file).
- **Repeat reminders** at 3/5/8 same-tool calls; rewrite-thrash at 3
  rewrites of one file; probe counters (3rd look at the same package →
  "record it, don't re-probe").
- **Verify enforcement**: hard nudge at `verifyEnforceThreshold = 8`
  writes since the last passing check — a counter, not prose, because
  prompt guidance demonstrably failed (a 60-round turn with two
  verifications).
- **Failure reminders**: known-pattern index injects the one-line fix with
  a cooldown; device crashes file into a run-scoped crash index.
- **Skill auto-attach**: matched skills inject as `context` blocks (replay
  verbatim, never render as chat), each at most once per session.
- **Steers**: mid-turn user messages drain at round boundaries into their
  own honestly-labeled user turn.
- **Ramble breaker**: tool-less max_tokens rounds end the turn as stalled.

### 3.5 Round & spend control

- Round budget `defaultMaxRounds = 60` (`AI_MAX_ROUNDS`, clamped 1..500).
- Per-turn cost cap (W7.3): stops gracefully before a round the cap cannot
  pay for; work already flushed.
- Latching failover (W7): a provider that saved the turn keeps it; usage /
  cost attributed to who actually served.

---

## 4. The turn state card (Phase 1, this change)

**The gap the two documents point at, and both failures shared:** nothing
in the per-round context said *what this turn has actually done*. The model
could replay its transcript but not see "round 14/60, 19 reads, 0 writes".
Prompt prose ("read each file once") is skimmable; a state report in the
freshest context, restated every round once the condition holds, is not.

**What it is** (`orchestrator/turnstate.go`): a compact situation card,
regenerated every round from the transcript:

```
## Turn state (auto — system-maintained working memory, not a user message)
Goal: Add a profile tab to the app
Round 7/60 · reads: 4 (app/tabs.tsx ×2, grep_codebase, app/missing.tsx)
writes: 1 (app/profile.tsx) · unverified: 1
Checks: run_typecheck FAIL (TS2304 in app/profile.tsx)
Last error: read_file: ENOENT: no such file
NEXT: No file writes after 7 rounds (4 read calls) — stop gathering. If this
task changes code, make the first edit now; if it is a question, answer it
from the transcript.
```

**Mechanics:**

- **Derived, never stored**: `scanTurnState` walks only this turn's
  messages (boundary = one past the last `turn_end` marker — every settled
  turn writes exactly one — so a resumed turn reconstructs the identical
  card as an uninterrupted one). Counts `read_file`/`list_files`/grep/
  explore-class calls, `write_file`/`edit_file`/`multi_edit`/`delete_file`
  writes, check verdicts (`passed` field where the tool speaks the
  contract; `ERROR` on tool failure; plain `ran` otherwise — never inferring
  pass/fail from banner text), and the last error with an excerpt.
- **Wire-only**: appended as a text block inside the trailing user message
  of `sendConversation` (`orchestrator.go:1366-1375`) — never to
  `conversation`/`outcome.Messages`, so nothing persists and nothing
  accumulates. The append copies before writing (a shared slice would leak
  the card into storage).
- **Cache-safe**: the card rides the last message; the Anthropic breakpoint
  is `len-2`, one message earlier — the cached prefix never moves. Cost is
  ~150-400 tokens in the always-re-sent tail, replacing itself each round.
- **Both providers**: Anthropic gets mixed content in one user turn;
  OpenAI's converter emits tool messages before accompanying user prose,
  so ordering stays valid.
- **NEXT directive** (one rule, in priority order): round budget > ¾ →
  finish; plan mode → read budget of 6 then "write the plan now"; build
  mode with 0 writes from round 4 → "stop gathering, make the first edit
  (or answer the question)"; 0 writes → orient-then-act; unverified ≥ 8 →
  run checks; else continue-and-verify.
- **Flag**: `TALONSTUDIO_TURN_STATE` (default on; `0/false/off` restores
  the plain transcript), documented in `.env.example` (flag-surface law).

**Tests**: `orchestrator/turnstate_test.go` — boundary finding, count
accuracy (reads ×2, writes, verdicts, errors), earlier turns and injected
nudges excluded from the scan, all NEXT directives, card line budget,
no-mutation of the shared slice, flag-off, assistant-tail fallback.

---

## 5. Gap analysis vs the two documents

| Recommendation (docs) | Status | Where / phase |
|---|---|---|
| Three-way split: transcript / memory / context | ✅ have | §0-§3 |
| **Current Situation Card**, refreshed every turn | ✅ **added** | §4 |
| Structured task state (goal, steps, decisions, files) | ✅ partial | card (goal, reads/writes, checks, next) + plan checklist + `prompts` row; no free-form decision list (ADRs cover the durable kind) |
| "State goal and next action before acting" | ✅ added | card's Goal + NEXT lines, mechanically |
| Anti-circle: stop re-reading, write early | ✅ added (structural) | card NEXT + existing reread/repeat/verify notes |
| Update memory continuously as work happens | ✅ partial | system-maintained card (no model cooperation needed) + `write_memory`/`write_adr` tools; see roadmap for automatic durable-memory proposals |
| Rolling summary of older turns, last N verbatim | ⚠️ partial | compaction-with-summary exists but is threshold-gated (200k), not proactive; window = last 60 rows — Phase 2 |
| Pin user constraints per turn | ⚠️ partial | prompt + plan checklist + steer lane; no explicit constraint list — Phase 2 |
| Memory loaded when relevant, not blindly | ⚠️ partial | bounded (20 rows × 240 chars) but unranked — Phase 2 |
| Tool outputs as summary, not raw forever | ⚠️ partial | superseded-read trim + spill/prune + compaction; no age-based decay of old tool results — Phase 2 |
| Per-component token budget breakdown | ❌ not built | one global 200k knob — Phase 2/3 |
| Retrieval beyond recency (semantic/symbol) | ❌ not built | `project_map` symbols + grep exist; no semantic index — Phase 3 |
| Agent-maintained scratchpad (`.agent/task-state.md`) | 🚫 **rejected** | see §6 — the system maintains state instead |
| Multi-agent | 🚫 deferred | docs agree: not now |

---

## 6. Decisions and non-goals

1. **System-maintained state over model-maintained state.** The docs'
   `.agent/` scratchpad requires the model to spend rounds writing state
   files; the observed failure mode is a model that does not track state
   at all. The card derives the same information from the transcript for
   free, every round, with zero cooperation and zero drift.
2. **Nothing new persisted.** The transcript stays the single raw record;
   derived context (like the system prompt) is recomputed. A crash mid-turn
   reconstructs the exact same card from what was already durable.
3. **No storage schema change.** Canonical store, dual-write projection,
   and shadow retention are documented rather than reworked — the observed
   failures were context-assembly failures, not storage failures.
4. **Cache-first.** Every per-round change rides the tail; the system
   prompt and history prefix stay byte-stable. Context improvements that
   bust the cache every round would pay for themselves in latency they
   cannot afford.
5. **Guidance that is a counter, not prose.** Where prompt text demonstrably
   failed (verify at 8 writes, round 60, cost cap), the enforcement lives in
   a number the model cannot skim past; the card makes those numbers visible.

---

## 7. Roadmap

**Phase 1 — done (this change):**
turn state card + NEXT directives; turn-aligned replay windows
(`store.GetConversationWindow` — a long previous turn can no longer hide
its opener and open the next turn on a harness nudge); read-discipline
prompt (one whole-file read per path per turn, grep for ranges); Maestro
opt-in only on explicit request; gates green (orchestrator/ai/api/store/
golden suites; goldens re-recorded — digest-only diff, invariants
unchanged).

**Phase 2 — context economy:**
- *Age-based tool-result decay*: results older than the last K messages
  shrink to head+tail on the wire (generalizes the superseded-read trim).
- *Proactive rolling summaries*: summarize turns older than N (not only
  when the 200k ceiling is hit), keeping the last 5-20 verbatim with exact details
  preserved (paths, errors, commands).
- *Memory relevance ranking*: score `project_memory` rows against the
  current goal instead of a flat 20-row window.
- *Constraint pinning*: lift user constraints from the opener/steers into a
  durable per-turn list on the card.
- *Outcome line across turns*: card gains a one-liner for the previous
  turn's verdict (from `turn_end`'s Verify payload) so a follow-up turn
  starts oriented.

**Phase 3 — retrieval:**
semantic/symbol search over the workspace, version-specific domain docs
retrieval, a real token budget per component (docs' 200k breakdown),
optional derive-read migration (`TALONSTUDIO_SESSIONLOG=derive`) promoted
to default once soak is clean.

---

## 8. Verification

- `orchestrator/turnstate_test.go` — unit coverage of scan, directives,
  card shape, wire-only/no-alias guarantee, flag-off; plus the loop-level
  `TestAgentLoopTurnStateCardOnWire` (captured provider requests show the
  card arriving with correct state, firing the idle directive exactly at
  round 4, and never appearing in `outcome.Messages`).
- `store` window tests + `api/TestConversationReplayOpensAtTurnOpener` —
  the aligned replay opens on the turn opener; the api test fails by
  opening on a harness nudge with alignment removed (verified by
  reverting the wiring).
- Golden suite (`evals/golden`) re-recorded after the card: only
  `request_digests` changed — tool sequences, rounds, outcomes, files
  touched identical (the card changes what the model is sent, not what the
  agent does).
- Docs guard (`evals/w14_docsguard_test.go`): `TALONSTUDIO_TURN_STATE`
  wired in code and documented in `.env.example`.
- Full backend gates: `go build ./...`, `go vet ./...`, `go test ./...`.
