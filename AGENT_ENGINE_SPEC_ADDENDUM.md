# SeekerStudio — Agent Engine Spec Addendum

Extends `AGENT_ENGINE_SPEC.md`. Sections continue that doc's numbering — hand both files together.

**This addendum covers:**
- §10 — Project Bootstrap Strategy (baked template, no per-session clone/install)
- §11 — Live Agent Execution Trace (OpenCode-style live thinking/tool-call stream)
- §12 — Known Bug → Structural Fix Mapping (the scaffold-visibility issue currently in progress)

---

## 10. Project Bootstrap Strategy

**Problem this solves:** a fresh `npx create-expo-app` + `npm install` + native module linking takes minutes. Project creation must feel instant. Don't clone or install per session — bake once, mount per session.

### 10.1 Source of truth
Your bootstrapped Seeker/Expo project lives in its own versioned git repo — `seekerstudio-template`. This is a real, CI-tested project, not a folder of loose files.

### 10.2 Build pipeline (offline, not per-session)
A CI job — triggered on template repo changes, never on user prompts — does the expensive work once:
1. Clone `seekerstudio-template`
2. `npm install`
3. Link MWA / Seed Vault / other baked native modules
4. Build the custom Expo dev client APK (from the native-modules discussion)
5. Publish the resulting folder + APK as an immutable artifact: `template:v1`

### 10.3 OverlayFS layering (this is what makes "clone" instant)
Each session workspace is an OverlayFS mount, not a copy:
- **Lower layer** — `template:v1`, read-only, shared across every session on that version. `node_modules`, base app shell, canonical wallet-connect hook, theme tokens.
- **Upper layer** — empty at session start, per-session, writable. This is where `write_file` (via staging, per §4.5 of the main spec) actually lands.

"Creating a new project" = mounting an overlay. No `git clone`, no `npm install`, no native build, at request time — ever. Those only happen in §10.2, ahead of time.

### 10.4 Enforced invisibility of the lower layer (structural, not a flag)
This is the fix for the bug in your screenshot. The current implementation likely treats scaffold-visibility as an optional query param (`includeScaffold=false`) — that's fragile, because it depends on every caller remembering to pass it correctly, and clearly something isn't.

**Correct architecture:** `GET /v1/projects/{id}/files` must be structurally incapable of returning lower-layer files, regardless of query params. Two ways to enforce this:
- **Option A (recommended):** the endpoint only ever lists the **upper layer** directly (`overlay/upper/`), never the merged overlay view. The lower layer isn't reachable from this code path at all — there's no flag to forget.
- **Option B:** if you need the merged view for anything (e.g. Metro needs the full merged filesystem to actually run the app), keep that separate from the file-tree API. The file tree and the runtime filesystem are different consumers with different visibility rules — don't let one code path serve both.

Remove `includeScaffold` as a parameter entirely once this lands — its existence is what let the bug happen.

### 10.5 Template versioning
One `template:vN` = one pinned RN/Expo SDK version = one dev-client APK = one lockfile. Never mutate a version in place. Bumping a dependency or adding a new baked native module means cutting `template:v(N+1)` and pointing new projects at it; existing sessions keep running on the version they started on.

### 10.6 Session creation flow
```
POST /v1/projects
  → mount overlay: lower=template:v{current}, upper=new empty dir
  → attach dev-client APK (already built, install to Redroid via adb install)
  → return project_id
  (no clone, no install, no build — all happened in §10.2)
```

### 10.7 Regression test (ties directly to §12)
**Mandatory test, not optional:** create a project, send zero prompts, call `GET /v1/projects/{id}/files`. Assert the response is an empty array. This is the exact scenario in your screenshot — bake it into CI so it can't silently regress again.

---

## 11. Live Agent Execution Trace

**Goal:** the OpenCode-style live feed — narration + tool calls appearing progressively as the agent works, not a wall of text after the fact. This extends the event schema from §7 of the main spec (don't replace it, add to it).

### 11.1 Event taxonomy

```json
// Narration — streams like chat tokens, this is the model's own "thinking out loud"
{"type": "thought", "text": "Two backends running — old on 8080, new on 9191. Let me check...", "streaming": true}

// Single tool call — ALWAYS rendered individually, never collapsed
{"type": "tool_call", "id": "tc_1", "tool": "run_command", "label": "Shell",
 "command": "netstat -ano | Select-String \"LISTENING\"", "status": "running"}
{"type": "tool_call", "id": "tc_1", "status": "done", "summary": "2 backends found on 8080, 9191"}

// Grouped tool calls — read-only exploration, collapsed once count > threshold
{"type": "tool_group", "id": "tg_1", "kind": "explore",
 "summary": "Explored 2 searches", "detail": "1 read, 1 search", "status": "done",
 "children": ["tc_2", "tc_3"]}
```

### 11.2 Grouping rule
- **Read-only / exploratory tools** (`read_file`, `list_files`, `grep_codebase`, `web_search`, `github_search`) — if 2 or more happen consecutively without a mutating call in between, collapse them into a single `tool_group` row: `"Explored N searches"` with an expandable `"N read, N search"` detail line, matching the screenshot exactly.
- **Mutating tools** (`write_file`, `install_package`, `run_build`, `run_typecheck`, `PROMOTE`) — **never** collapsed. Always their own visible row with a live status transition (pending → running → done/fail icon). These are the actions the user actually needs to track.

### 11.3 Streaming requirements
Every tool call emits **two events minimum** — not one event after the fact:
1. On dispatch: `status: "running"` — user sees the row appear immediately with a spinner.
2. On resolution: `status: "done"` or `"fail"` — row updates in place with a result summary.

Narration text streams the same way your existing chat tokens do (SSE, incremental). The trace should never feel like it "dumps" — every line the user sees in the screenshot example appeared progressively while the agent was actually working, not all at once at the end.

### 11.4 Frontend rendering contract
- Tool type as a bold label (`Shell`, `Explored`, `Read`) followed by the command/args in monospace.
- Grouped rows show an icon + short summary line, expandable via chevron to reveal the individual calls inside.
- Narration renders as plain prose between tool rows, exactly interleaved in the order events arrived — this is what makes it read like a running commentary, not a log dump.
- Full trace is retained per turn (scrollable history), not ephemeral — the user should be able to scroll back and re-expand a past group.

### 11.5 DoD
- Sending a prompt shows the first narration or tool-call row within ~300ms, not after the full turn completes.
- 3+ consecutive read-only calls auto-collapse into one group; write/build/verify calls never do.
- Every tool row transitions visibly from running → done/fail; nothing appears pre-resolved.

---

## 12. Known Bug → Structural Fix Mapping

Direct mapping from what you're seeing to what in this addendum fixes it, so this doesn't get treated as a one-off patch:

| Symptom (from screenshot) | Root cause | Fix |
|---|---|---|
| File tree shows files before any prompt is sent | `GET /v1/projects/{id}/files` unions scaffold + generated files, gated by a flag that isn't reliably respected | §10.4 — remove the flag entirely, make the endpoint structurally only read the upper overlay layer |
| No scaffold manifest found on disk | Scaffold files are currently baked directly into the workspace rather than a separate read-only layer | §10.3 — move to OverlayFS lower layer; there's nothing to "manifest," visibility is structural |
| Two backends running (8080 + 9191) | Stale process from a previous run not killed before restart | Operational — confirm process cleanup in your dev restart script; not an architecture fix |

Once §10 lands, "empty file tree until first AI write" stops being a behavior you have to remember to implement correctly per-endpoint — it becomes true by construction.
