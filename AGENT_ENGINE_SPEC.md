# SeekerStudio — Agentic Coding Engine Specification

Enforces the end-to-end "vibe coding" loop (prompt → plan → research → code → install → build → verify → heal) inside the Go backend, with guardrails specific to Solana Mobile / Seeker app generation.

**Reference:** `IMPLEMENTATION_ROADMAP.md` (Phase 5 — AI Orchestrator), `Solana_Mobile_Vibe_Coding_Guide.md.txt`
**This doc governs:** `backend/internal/orchestrator`, `backend/internal/agent`, `backend/internal/tools`

---

## 0. Purpose

Tools like Lovable, Bolt, and Replit Agent aren't "an LLM that writes files." They're a **loop with tool calls, verification gates, and a recovery path**. The model never writes directly to the live workspace — it proposes actions, the engine executes them through typed tools, verifies the result, and only then does the user see it land. This doc specifies that loop for SeekerStudio, so no phase gets built as a one-off script — every phase is a stage in one enforced state machine.

---

## 1. Non-Negotiable Design Principles

1. **The LLM never touches the filesystem directly.** It emits structured tool calls; Go executes them. This is what makes verification, staging, and rollback possible.
2. **Every write passes through staging before it's live.** No file lands in the Metro-watched workspace until the batch it belongs to has passed the verification gates (§4.7). This is the mechanism behind the live-preview behavior we designed earlier (per-file streaming visible in the IDE, atomic promotion to the live workspace).
3. **Every tool call is logged as a structured event**, streamed to the frontend over the same SSE/WS channel as chat tokens. The user should see "Creating `HomeScreen.tsx`… Installing `@solana-mobile/mobile-wallet-adapter-protocol-web3js`… Running typecheck… ✅" the same way Bolt/Lovable show a live action timeline. This is not optional polish — it's the primary trust signal that the product is actually doing something, not just chatting.
4. **Nothing runs unsandboxed.** `run_command` is an allowlist, not a shell. No arbitrary `bash -c`.
5. **Every generation cycle ends in one of exactly three states:** `done`, `healed`, or `failed-needs-user`. There is no silent partial-failure state.
6. **Solana Mobile constraints are enforced in code, not just in the system prompt.** A model can ignore prompt instructions; it cannot bypass a Go-level validator that rejects the patch before it's staged (§5).

---

## 2. The Agent Loop — State Machine

```
INTAKE
  ↓
PLAN                    (LLM: screens/features/packages, user approves)
  ↓
CONTEXT_BUILD           (pattern detection — scan scaffold/existing files)
  ↓
RESEARCH (conditional)  (web/GitHub search, only if triggered — see §4.2)
  ↓
GENERATE                (LLM streams file-by-file patch, tool calls)
  ↓
STAGE_WRITE             (each file → staging dir, NOT live workspace)
  ↓
INSTALL (conditional)   (new packages → sandboxed npm/yarn install)
  ↓
VERIFY_STATIC           (tsc --noEmit, eslint — gate, not advisory)
  ├─ fail → HEAL (max 3 attempts) → back to GENERATE
  └─ pass ↓
PROMOTE                 (atomic move: staging → live workspace, Metro Fast Refresh fires)
  ↓
VERIFY_RUNTIME          (adb logcat tail + screenshot vision check)
  ├─ fail → HEAL (max 3 attempts) → back to GENERATE
  └─ pass ↓
DONE
```

`HEAL` is not a separate phase — it's `GENERATE` re-entered with the error, stack trace, offending file, and project tree appended to context. After 3 failed heal attempts on the same patch: stop, surface the error and diff to the user in chat, do **not** keep retrying silently.

---

## 3. Tool Interface (Go)

The LLM's only way to affect the world is by emitting one of these tool calls. Each is a typed Go function; the orchestrator validates arguments before execution, never `eval`s or shells out to what the model wrote raw.

```go
package tools

type Tool interface {
    Name() string
    Execute(ctx context.Context, session *Session, args json.RawMessage) (ToolResult, error)
}

// Filesystem — all writes go to session.StagingPath, never session.WorkspacePath directly
type ListFiles struct{}       // list_files(path string) []FileEntry
type ReadFile struct{}        // read_file(path string) string
type WriteFileStaged struct{} // write_file(path, content string) — writes to staging only
type DeleteFileStaged struct{}
type GrepCodebase struct{}    // grep_codebase(pattern string, glob string) []Match — pattern detection, §4.3

// Package management — allowlist-gated, see §4.6
type InstallPackage struct{} // install_package(name, version string) — checked against native-module allowlist

// Research — cached, rate-limited, see §4.2
type WebSearch struct{}      // web_search(query string) []SearchResult
type GitHubSearch struct{}   // github_search(query string, repo? string) []CodeResult

// Verification — these are gates, their output feeds VERIFY_STATIC / VERIFY_RUNTIME, not advisory chat text
type RunTypecheck struct{}   // run_typecheck() TypecheckResult
type RunLint struct{}        // run_lint() LintResult
type RunBuild struct{}       // run_build() BuildResult (dev-client JS bundle build, not native unless §4.6 triggers one)

// Device verification
type CaptureScreenshot struct{} // adb exec-out screencap, feeds vision-check
type TailLogcat struct{}        // adb logcat -d ReactNativeJS:E AndroidRuntime:E, bounded window

// Sandboxed shell — ALLOWLIST ONLY. Not a general exec.
type RunCommand struct{}
// allowlist: npm, npx, yarn, tsc, eslint, expo, gradlew (native rebuild path only)
// anything else → reject before spawning a process
```

**Rule:** if a tool isn't on this list, the model cannot invoke it. Adding a new tool is a deliberate backend change, not something the model can improvise around.

---

## 4. Phase Details

### 4.1 Intake & Planning
- User prompt + current project tree + `.scaffold-manifest.json` (existing files, per Phase 7 of the roadmap) go to the LLM in **plan mode**.
- Plan mode is enforced the same way you already have it: the model returns structured JSON (screens, features, navigation, packages needed) — it is not permitted to emit file contents in this phase. Reuse the existing plan/build mode split; this doc doesn't change that contract, it wraps what happens after the user approves.

### 4.2 Research (web / GitHub) — conditional, not default
Research is expensive (latency + tokens) and Solana Mobile APIs shift, so it should trigger, not run every turn.

**Trigger conditions** (any one):
- The plan references an MWA/Seed Vault API surface not present in a cached "known-good API reference" doc bundled with the system prompt.
- The model's own generation attempt fails typecheck against a package whose types don't match what's in context (signals the model is guessing at an API).
- User explicitly asks ("look up how X library does Y").

**Constraints:**
- Cache results per package+version for the life of the deployment — don't re-search the same MWA API on every project.
- GitHub search should be scoped to specific known-good repos first (`solana-mobile/mobile-wallet-adapter`, `solana-mobile/seed-vault-sdk`, your own template repo) before falling back to open search — this avoids the model copying patterns from unrelated/incompatible RN codebases.
- Research results get compressed into a short context note (not dumped raw into the prompt) before being handed back to `GENERATE`.

### 4.3 Pattern Detection (context build, before generation)
Before the model writes anything, run `grep_codebase` against the existing project + your template scaffold to surface:
- Existing navigation setup (don't let the model reinvent a navigator that's already there)
- Existing wallet-connect boilerplate (don't regenerate MWA connection logic per-screen — reuse the one canonical hook)
- Existing theme/style tokens

This is the step most AI coding tools under-invest in and it's why generated code drifts into duplicated, inconsistent patterns across a session. Feed the top N matches into the generation context automatically — don't rely on the model choosing to search first.

### 4.4 Generation & Streaming
- Model streams a multi-file patch. Each file's `write_file` tool call fires as soon as that file's content is complete (not token-by-token — a half-parsed file breaks staging validation).
- Each `write_file` call is a discrete, logged event → pushed to frontend timeline immediately.
- Files land in `session.StagingPath`, mirrored in the IDE's file tree/code editor UI in real time (this is where the "watching it build" feeling comes from) — but the **live Redroid workspace is untouched** until §4.7 passes.

### 4.5 File Writing Strategy (staging → atomic promote)
This is the mechanism from our earlier discussion, made concrete:

```go
type Session struct {
    StagingPath   string // /workspaces/{id}/.staging/
    WorkspacePath string // /workspaces/{id}/  (Metro-watched, live)
}

func (o *Orchestrator) PromoteBatch(s *Session, patch Patch) error {
    // 1. All files in patch already written to StagingPath during GENERATE
    // 2. VERIFY_STATIC must have passed before this is called
    // 3. Atomic promote: rename, not copy — same volume, single fs op per file
    for _, f := range patch.Files {
        src := filepath.Join(s.StagingPath, f.Path)
        dst := filepath.Join(s.WorkspacePath, f.Path)
        if err := os.Rename(src, dst); err != nil {
            return fmt.Errorf("promote failed on %s, aborting batch: %w", f.Path, err)
        }
    }
    return nil // Metro fsnotify fires, Fast Refresh pushes to Redroid
}
```
If any single file in the batch fails to promote, abort the whole batch — never leave the live workspace in a half-patched state.

### 4.6 Package Installation
- `install_package` checked against two lists before execution:
  1. **Baked dev-client allowlist** (MWA, Seed Vault, common Expo modules — see the native-module doc from the encoder/native-modules discussion). If the package is already compiled into the session's dev client, this is a no-op — just add it to `package.json` for typecheck purposes.
  2. **Native-code detection**: if the package has a `android/` native folder or isn't in the allowlist, this flags a **rebuild-required** patch. The batch still promotes for JS purposes, but `VERIFY_RUNTIME` is deferred until an async `gradlew assembleDebug` completes — the user sees "adding native capability…" in the timeline, not a blocked chat.
- Installs run inside the session's sandboxed workspace container, never on the host, with a lockfile diff recorded for the build cache.

### 4.7 Verification Gates (VERIFY_STATIC)
Both must pass before `PROMOTE`:
- `tsc --noEmit` — zero errors. Not "warnings ok" — any error blocks promotion.
- `eslint` — configurable severity, but at minimum: no syntax errors, no undefined-import errors.

Failure → structured error (file, line, message) appended to context → back to `GENERATE` (heal attempt).

### 4.8 Runtime Verification (VERIFY_RUNTIME)
After promote:
- Tail `adb logcat` for `ReactNativeJS:E` / `AndroidRuntime:E` in a bounded window (e.g. 5s after Fast Refresh fires).
- Capture a screenshot, run it through a vision-check call: "does this match the intended screen from the plan?" — catches silent layout/logic failures that don't throw but also don't look right.
- Every generated app root includes the dev-only `<ErrorBoundary>` from the roadmap — its rendered error text is what the vision/OCR check reads when logcat is clean but the screen is broken.

### 4.9 Self-Healing Loop
- Max 3 attempts per patch, hard stop.
- Each attempt appends: previous patch, exact error (typecheck error, logcat stack, or vision-check mismatch description), and file tree — not just "it failed, try again."
- Watchdog timer per attempt (don't let a hung build stall the session indefinitely).
- On exhausting attempts: surface the failure and last diff to the user directly in chat. Never silently revert without saying so.

---

## 5. Solana Mobile / Seeker-Specific Guardrails

Enforce these at the **Go validation layer**, not just the system prompt — a prompt instruction is a suggestion, a Go check before `STAGE_WRITE` is a wall:

- **Reject non-React-Native/Expo output.** If a patch contains `.html`, raw `<div>`/`<style>` outside a `.tsx`/`.ts` file, reject before staging.
- **Pin RN/Expo SDK version** at the project-template level — the model doesn't get to pick or upgrade versions mid-session (this is also what keeps your Gradle build cache from the native-modules doc actually hitting).
- **MWA usage pattern check:** wallet connection code must go through the one canonical hook/module your template ships (from §4.3 pattern detection), not a freshly hand-rolled `transact()` call per screen — reduces both bugs and duplicate native bridge usage.
- **No hardcoded secrets/keys.** Static check on generated files for anything that looks like a private key, seed phrase, or hardcoded RPC API key — block promotion, don't just lint-warn.
- **Native module allowlist enforcement** ties directly into §4.6 — this is the single point where "serious native modules" stops being a planning problem and becomes an enforced runtime decision.

---

## 6. Concurrency & Session Isolation

- One `Session` = one staging dir + one workspace dir + one Redroid container + one Gradle/npm sandbox. No shared mutable state across sessions.
- The Go orchestrator must serialize `GENERATE → STAGE_WRITE → VERIFY → PROMOTE` per session (no two patches racing to promote into the same workspace), but different sessions run fully in parallel — this is a per-session state machine, not a global one.

---

## 7. Observability — Frontend Timeline Events

Every tool call and phase transition emits a structured event over the existing SSE/WS channel:

```json
{"type": "phase", "phase": "GENERATE", "session_id": "..."}
{"type": "tool_call", "tool": "write_file", "path": "app/HomeScreen.tsx", "status": "staged"}
{"type": "tool_call", "tool": "install_package", "name": "@solana-mobile/...", "status": "no-op-cached"}
{"type": "verify", "stage": "typecheck", "status": "pass"}
{"type": "phase", "phase": "PROMOTE", "status": "done"}
{"type": "verify", "stage": "runtime", "status": "fail", "error": "..."}
{"type": "phase", "phase": "HEAL", "attempt": 1}
```
This is what powers the "Creating file… Installing package… Running checks…" live action list users expect from this category of product — build the frontend component against this event schema early, don't bolt it on after the orchestrator is done.

---

## 8. Go Package Layout

```
backend/internal/
├─ orchestrator/       # state machine, phase transitions, HEAL loop
├─ agent/
│  ├─ planner.go        # PLAN phase, plan-mode LLM calls
│  ├─ generator.go       # GENERATE phase, streaming patch handling
│  └─ context.go         # pattern detection, research trigger logic (§4.2, §4.3)
├─ tools/               # every Tool from §3, one file per tool, allowlist enforcement
├─ staging/             # StagingPath ↔ WorkspacePath promote logic (§4.5)
├─ verify/
│  ├─ static.go          # tsc / eslint gate (§4.7)
│  └─ runtime.go         # logcat tail + vision check (§4.8)
├─ guardrails/           # Solana/Seeker-specific validators (§5) — run before STAGE_WRITE
└─ events/               # structured event emission (§7)
```

---

## 9. Testing & Definition of Done

- Unit: each `Tool` has an isolated test with a `FakeSession` (no real ADB/filesystem) — assert allowlist rejection, staging-only writes, promote atomicity.
- Integration: full loop test — "add a wallet-connect button" prompt → assert files land in staging → typecheck passes → promotes → Redroid Fast Refresh confirmed via screenshot diff.
- Fault injection: deliberately break a generated file → assert HEAL triggers, max-3 cutoff respected, user-facing failure message fires on exhaustion.
- Guardrail test: attempt to generate raw HTML output or a hardcoded key → assert rejection before staging, not after.

**DoD:** A prompt can go end-to-end — plan → approve → generate → install (cached or gated) → verify → promote → runtime-verify — with visible live-preview updates and a working self-heal path, entirely through the tool interface in §3, with zero direct model-to-filesystem access at any point.
