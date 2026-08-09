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

### 11.4 Action verbs must be explicit and file-operation-specific
Generic labels like `write_file` or `tool_call` are not acceptable in the rendered trace — every action shown to the user must use a plain-language verb that describes what's actually happening to that specific file, matching the interactive, narrated feel of the screenshot example:

- `read_file` → **"Reading `{path}`"**
- `write_file` on a path that does not yet exist in the workspace → **"Creating `{path}`"**
- `write_file` on a path that already exists → **"Updating `{path}`"** — and this label is what makes §2.13 of `PHASE_7_ENGINEERING_INTELLIGENCE.md` (mandatory read-before-write) directly visible: a "Reading `{path}`" row must appear before every "Updating `{path}`" row for the same file, in the same turn. If an "Updating" row ever appears without a preceding "Reading" row for that exact path in that turn, the read-before-write rule has been violated — this is not just a rendering detail, it doubles as a live, human-visible audit of that rule actually being followed.
- `delete_file` → **"Deleting `{path}`"**
- `install_package` → **"Installing `{package}`"**
- `run_typecheck` → **"Checking types…"**
- `run_lint` → **"Linting…"**
- `run_build` → **"Building…"**
- `run_command` → **"Shell"** with the literal command shown in monospace (as in the screenshot)
- Grouped read-only exploration (§11.2) → **"Explored N searches"**

This mapping must live in one place the frontend renders from directly — not be re-derived ad hoc per tool — so adding a new tool later means adding one new verb mapping, not touching rendering logic throughout the UI.

### 11.5 Frontend rendering contract
- Tool type as a bold label (`Shell`, `Explored`, `Read`) followed by the command/args in monospace.
- Grouped rows show an icon + short summary line, expandable via chevron to reveal the individual calls inside.
- Narration renders as plain prose between tool rows, exactly interleaved in the order events arrived — this is what makes it read like a running commentary, not a log dump.
- Full trace is retained per turn (scrollable history), not ephemeral — the user should be able to scroll back and re-expand a past group.

### 11.6 DoD
- Sending a prompt shows the first narration or tool-call row within ~300ms, not after the full turn completes.
- 3+ consecutive read-only calls auto-collapse into one group; write/build/verify calls never do.
- Every tool row transitions visibly from running → done/fail; nothing appears pre-resolved.
- Every file-touching action renders with its specific verb (Reading/Creating/Updating/Deleting), not a generic tool name — confirmed by inspecting a real trace, not the event schema alone.
- Editing an existing file is confirmed to always show a "Reading" row before its "Updating" row for that exact path, within the same turn — checked here as a UI requirement, and in Phase 7 §2.13 as the underlying engineering rule it makes visible.

---

## 12. Known Bug → Structural Fix Mapping

Direct mapping from what you're seeing to what in this addendum fixes it, so this doesn't get treated as a one-off patch:

| Symptom (from screenshot) | Root cause | Fix |
|---|---|---|
| File tree shows files before any prompt is sent | `GET /v1/projects/{id}/files` unions scaffold + generated files, gated by a flag that isn't reliably respected | §10.4 — remove the flag entirely, make the endpoint structurally only read the upper overlay layer |
| No scaffold manifest found on disk | Scaffold files are currently baked directly into the workspace rather than a separate read-only layer | §10.3 — move to OverlayFS lower layer; there's nothing to "manifest," visibility is structural |
| Two backends running (8080 + 9191) | Stale process from a previous run not killed before restart | Operational — confirm process cleanup in your dev restart script; not an architecture fix |

Once §10 lands, "empty file tree until first AI write" stops being a behavior you have to remember to implement correctly per-endpoint — it becomes true by construction.

---

## 13. Per-Project Identity Configuration

**Problem this solves:** `app.json`/`package.json` mix two categories of values — per-project identity (`name`, `slug`, `android.package`, `ios.bundleIdentifier`, `scheme`) and shared template config (`dependencies`, `scripts`, `plugins`, `expo.doctor`). These must be handled by completely different mechanisms, or you either get bundle-id collisions across projects or lose the shared build cache from Guide 2.

### 13.1 Fields that must be unique per project
- `expo.name` — display name
- `expo.slug` — internal identifier, derived from the user's chosen app name
- `android.package` — Android bundle id (reverse-DNS, must be a legal Java package name)
- `ios.bundleIdentifier` — iOS equivalent
- `expo.scheme` — deep-link scheme, must not collide across installed apps
- `package.json`'s `name` field

### 13.2 Fields that must stay identical to `template:vN`
`dependencies`, `devDependencies`, `scripts`, `plugins`, `expo.doctor`. These are what makes the shared Gradle/npm cache and the pre-built dev client (Guide 2) work at all — never let project creation or the AI touch these independently of a template version bump.

### 13.3 The dev-client identity tension — and its resolution
`android.package`/`scheme` are only read by `expo prebuild` when generating the **native** Android project. The shared dev client (built once per `template:vN`, per the native-modules guide) is itself a native artifact with some package name baked in — if every project gets a truly personalized bundle id from creation, that forces a per-project native rebuild of the dev client, which defeats Guide 2's core optimization.

**Resolution:**
- The shared dev client runs under one **fixed, generic development identity** (e.g. `com.seekerstudio.devshell`) across every session — it's internal infra, never published, so this is safe.
- `name`/`slug` personalize immediately and take effect at the JS/Expo Router level — no rebuild required.
- The **real, personalized** bundle id is stored as project metadata from day one, but only gets baked into an actual native build at **export time** (Phase 7 — `gradlew assembleRelease` for the signed APK / dApp Store bundle), when a full native rebuild is already happening anyway.
- Net effect: identity personalization is free during the fast inner loop, and only "paid for" once, deliberately, on export.

### 13.4 Where personalization happens
- **Source of truth is the database** (projects table: `name`, `slug`, `bundle_id`, `scheme`), not the filesystem.
- `app.json` in the workspace is a **rendered copy**, written deterministically by Go — a template-substitution step, not an LLM call — immediately after the overlay mounts (§10.6), before the first prompt is ever sent.
- Slug generation: kebab-case from the user's app name, collision-checked against existing projects, suffixed if needed.
- Bundle id generation: `com.seekerstudio.apps.{slug}` (or similar), validated as a legal reverse-DNS Java package name (lowercase, underscores not hyphens per segment, no leading digits).

### 13.5 Guardrail — identity fields are not agent-writable
Extend §5's guardrail layer: the `write_file` tool must reject any write targeting `app.json`/`package.json`'s identity keys, full stop — this isn't a prompt instruction, it's a Go-level check before `STAGE_WRITE`. Stronger version: don't expose these files to the agent's file tools at all. If generated UI code needs to reference the app's display name, provide a read-only `get_project_info` tool instead of raw file access to the config.

### 13.6 DoD
- Two projects created back to back have distinct `slug`, `android.package`, and `scheme` — assert no collision possible by construction (DB uniqueness constraint, not just app-layer checking).
- Sending a prompt that asks the AI to "change the app's bundle id" is rejected or ignored at the tool layer, not just discouraged in the system prompt.
- Export (Phase 7) produces an APK with the *real* personalized bundle id; the dev-loop dev client throughout the session ran under the fixed generic identity the whole time.
