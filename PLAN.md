# Talon — Plan (live, reflects repo state 2026-09-04)

This plan supersedes earlier drafts. It is written against the **actual** state of the
repo at this commit, not against a prior session's memory.

## Authoritative inputs

- **Design system:** `DESIGN.md` — Linear midnight precision (void/carbon/obsidian/
  graphite/smoke/ash/fog/mist/bone/paper/acid-lime/pulse-green/coral-red/
  signal-teal/iris-violet/lavender, Inter Variable + Berkeley Mono, type scale
  caption→display, spacing 4..128, radius sm/md/xl/2xl/2xl-2/full/full-2). The
  old `DESIGN_SYSTEM.md` (Material You + Bento + Swiss) has been deleted.
- **Agent content:** `backend/internal/agent/content/`, embedded into the binary
  via `//go:embed content`. `rules/core.md` is the always-on discipline block.
- **Loop:** `backend/internal/orchestrator/` — `BuildOutcome.Paused/Exhausted/
  RemainingTasks/RoutedModel/RouteReason` are real, `latchedModel` is real, the
  `defer flush()` pattern is real, prompt-cache rebuild is gated on real changes.
- **Tools:** `backend/internal/tools/`, per-request `tools.Registry` is tenant-
  isolated with a `ReadOnly` marker for parallel execution, `validateArgs`
  pre-runs. `read_file`/`read_skill`/`write_file`/`edit_file`/`multi_edit`/
  `run_command`/`run_checks`/`run_typecheck`/`run_lint`/`capture_screenshot`/
  `tail_logcat`/`test_wallet_roundtrip` are real and named.

## What is already shipped (do not re-do)

- **Skill bundle (9 skills):** `talon-mobile` (SKILL.md, seed-vault, scl,
  dapp-store, app-linking, mobile-defaults), `wallet-mwa`, `talon-screen-design`,
  `rn-best-practices`, `expo-router-nav`, `expo-ui`, `expo-ui-jetpack-compose`,
  `reanimated-v4`, `failure-patterns`. Curated, version-pinned, MIT-licensed
  where applicable (Vercel Labs, Expo), with ATTRIBUTION.md.
- **Triggers (A2):** `triggers:` front-matter is in `agent/frontmatter.go` with
  `import:`, `content:`, `path:`, `pkg:`, `symptom:`, `phase:`. Symptom matching
  feeds `failure-patterns` on `TS[0-9]+`, `Cannot find module`, `ENOSPC`,
  `ENOENT`, `adb: device not found`, `Hermes`, `Gradle`, etc. Phase matching
  feeds the same on `PhaseHeal`.
- **Failure-patterns index (A3-curation):** the curated markdown
  `failure-patterns/SKILL.md` is shipped and auto-attaches via triggers. The
  **runtime `(tool, error_signature) → {cause, fix, skill_to_load, cooldown}`**
  table that the orchestrator consults *before* re-asking the model is **not**
  in code — only the doc side is.
- **Root `AGENTS.md` and `PROJECT_DOCUMENTATION.md`:** both deleted from the repo
  (in the session-start `D` set). No replacement is required unless we want one.
- **`tools/runtime.go`:** `capture_screenshot` and `tail_logcat` are real;
  `wallet_roundtrip` runs a full connect/sign flow on the mock wallet.
- **`tools/verify.go`:** `run_checks` / `run_typecheck` / `run_lint` are real;
  the orchestrator's `enforcePeriodicVerify` already injects a verify-now nudge
  after 8 unverified writes, and a stronger "stop writing this file" nudge after
  3 writes to the same path.
- **`tools/projectmap.go`:** a capped, deterministic project tree is generated
  and used by the prompt-cache rebuild guard (fingerprint-gated, so the cache
  prefix only changes when the tree actually changes).
- **`agent/agent.go`:** the `Knowledge` registry loads the embedded skills and
  rules, exposes `List()`, `Get(name)`, `AutoAttachFor(tool, input)`,
  `SkillsForSymptom(msg)`, `SkillsForPhase(phase)`. Tests in
  `agent/agent_test.go` pin the curated list and the auto-attach / symptom /
  phase behaviour.

## What is genuinely missing (the real backlog)

Ordered by leverage, smallest first. Each item is a single PR, additive, behind
existing patterns, and verifiable by `go build ./... && go vet ./... && go test
./...` (and the matching `npm run build && npm run lint` for any frontend touch).

1. **R2 — Add the missing agent surface the model can call** (1 day).
   The user listed `read_file`, `list_files`, `explore`, `search_files`,
   `find_files`, `remember`, `recall`, `read_skill`. Of these:
   - `read_file`, `list_files` exist and are wired. Confirm clean schema in a
     short `tools/explore_test.go`.
   - `search_files`, `find_files` are missing as named tools. The content-
     search is currently a `grep`-style tool. Add `find_files` (path glob,
     returns paths, no body) and `search_files` (regex over file contents,
     capped, with `dir` / `include` / `exclude` filters) as two new tools.
   - `explore` is missing. A composite tool that takes a single intent string
     and dispatches to the right combination of `list_files` / `find_files` /
     `search_files` in one round-trip, with capped output. Removes a class of
     `read_file` storms.
   - `remember`, `recall` exist as the orchestrator's `loadProjectContext`
     (ADRs + memory) but are **not** callable as tools. Add `memory.append`
     and `memory.read` thin wrappers that the model can call explicitly. The
     existing `write_adr` / `write_memory` already cover the write side.
   - `read_skill` is referenced as a tool in the live-trace spec; the loader
     exists in `agent/agent.go`. Confirm the tool form is wired and emits
     `sess.MarkSkillSeen` on success (the orchestrator already handles that
     at `orchestrator.go:993`).
   **DoD:** the 7 tool names are all callable from a model round; `explore`
   returns in one call what previously took three; `memory.read` does not
   double-write to the cache prefix.
   **Files:** new `backend/internal/tools/explore.go`, `find_files.go`,
   `search_files.go`, `memory_read.go`; tests `tools/explore_test.go` etc.;
   `tools/registry.go` (registration only).

2. **A3 — Wire the runtime failure-pattern lookup** (1–2 days).
   The curated index in `failure-patterns/SKILL.md` is loaded on symptom; the
   **runtime** `(tool, error_signature) → fix line + cooldown` table is not.
   Add:
   - `agent/failure_index.go` — a small, deterministic index compiled from the
     skill body (parse the `## …` sections at startup) plus a hand-curated
     seed. Keys: `(tool, normalized_error_signature)`. Values: `{cause, fix,
     skill_to_load, cooldown_rounds}`.
   - `orchestrator/orchestrator.go` `executeTools` `run` body — after a
     `tool_result` carries `IsError: true`, *before* the next round's
     `ChatWithTools`, look up the signature; if hit and not on cooldown,
     prepend a one-line system reminder. Respect cooldown per session via
     `sess.NoteFixSeen(signature, round)`.
   - `record_failure_pattern` tool — the model can append a new pattern after
     two consecutive rounds hit the same error (capped at 5 patterns per
     session to prevent runaway).
   **DoD:** a deliberate `write_file` to a non-existent dir produces a known
   signature, the orchestrator logs the hit, the model receives a one-line
   "known cause: …; known fix: …" before re-asking; the cooldown prevents
   re-injection on the next retry.
   **Files:** `backend/internal/agent/failure_index.go`,
   `backend/internal/orchestrator/orchestrator.go` (one block in `run`),
   `tools/registry.go` (registration of `record_failure_pattern`); tests in
   `agent/failure_index_test.go` and `orchestrator/failure_index_test.go`.

3. **G4 — Build gate pipeline** (3–4 days, biggest reliability gain).
   No `backend/internal/build/` package exists. Verification today is a single
   `run_checks` call. Replace it with a chain:
   - `internal/build/gates.go` — `types` → `bundle` (new) → `gradle` (new,
     async) → `boot` (new) → `vision` (already in `ai/vision.go`) → `interact`
     (new, per-route screenshot + vision check).
   - Soft time budgets: 60 / 90 / 300 / 60 / 30 / 120 s. Failure short-circuits
     to the previous gate and a `heal` round. `gradle` and `interact` run in a
     goroutine with `events/` progress so the user sees a live card, not a
     frozen screen.
   - New `run_gate` tool — model can ask "did the bundle step succeed?" without
     re-running.
   - `verify_runtime` (existing) becomes the orchestrator that calls the gates
     in order.
   **DoD:** a real end-to-end build goes `types → bundle → gradle → boot →
   vision → interact` and only marks verified if all six pass; a failed route
   in `interact` produces a visible "interact failed: route `/mint` returned
   error state"; the model gets a clean failure → fix → retry loop.
   **Files:** new `backend/internal/build/{gates.go,gradle.go,interact.go,
   vision_gate.go,events.go}`; `tools/runtime.go` (gate hooks); `tools/
   verify.go` (orchestrator rewrite); tests in `build/gates_test.go` and an
   end-to-end `build/e2e_test.go` against a fixture workspace.

4. **G5 — Close the logcat self-debug loop** (1 day, depends on G4).
   `tools/runtime.go` has `TailLogcat`; the orchestrator does not call it
   automatically on a failed `boot` gate. Add:
   - `runtime/boot_check.go` (new) — wraps `boot` so a missing `pidof`
     triggers a 10-line `adb logcat -d *:E` capture and feeds it back to the
     next round's prompt as a `ai.UserText` reminder.
   - `runtime/crash_index.go` (new) — small matcher for `FATAL EXCEPTION`,
     `ANR in`, `RuntimeException`, `TypeError: undefined is not a function`
     (Hermes), which selects `mobile-defaults` as the next round's auto-attach
     hint.
   **DoD:** a deliberately-broken APK (empty `<View>` with raw text child)
   produces a Hermes error in logcat, the orchestrator feeds it back, the
   model fixes it on the next round; the `mobile-defaults` skill lands when
   the matcher fires.
   **Files:** new `backend/internal/runtime/boot_check.go`, `crash_index.go`;
   `build/gates.go` (`boot` gate hook); tests.

5. **G6 — Sandboxed destructive edits with a patch buffer** (2–3 days,
   **land behind a feature flag**). This is the only phase that changes
   round semantics.
   - `internal/build/patch_buffer.go` — content-hash-keyed pre-image store,
     50 entries per session, FIFO eviction with a notice.
   - Hooks in `tools/edit.go` (`edit_file`, `multi_edit`) and
     `tools/fs.go` (`write_file`, `delete_file`) — capture pre-image before
     any destructive call.
   - New `try_edit` tool — apply → `verify_runtime` (gate 1 minimum) → if
     it fails, auto-revert to pre-image and emit `Reverted: <reason>` on the
     tool_result so the model sees the rollback.
   - New `keep_edit` tool — explicitly accept and clear.
   - Feature flag `TALONSTUDIO_PATCH_BUFFER=off` (default off, opt-in per
     session via `RunOptions.PatchBufferEnabled`).
   **DoD (off):** all existing tests pass; a session with the flag off
   behaves identically to today. **(on):** a model attempt that breaks types
   is auto-reverted; a model attempt that fixes types is kept; a buffer
   overflow reverts the oldest entry with a clear notice.
   **Files:** new `backend/internal/build/patch_buffer.go`,
   `tools/try_edit.go`, `tools/keep_edit.go`; `tools/edit.go`,
   `tools/fs.go` (pre-image hooks); tests.

6. **S7 — `DESIGN.md` enforcement on generated apps** (2–3 days, depends on G4).
   The model can produce any visual. Add:
   - `tools/validate_visual.go` — scans the generated workspace for
     hardcoded colors, hex values (`#[0-9a-f]{3,8}`), raw pixel values,
     off-scale radii/weights, off-token font families. Returns a list of
     `violation{path, line, column, rule, fix_hint}`.
   - Acid-lime discipline: at most one filled acid-lime button per view, no
     acid-lime text, no acid-lime on non-action elements. Purple / green /
     teal / violet / lavender allowed **only** in a Solana context (wallet,
     network, transaction, NFT, token) — `solana_context` predicate on the
     screen path / hook names / imports.
   - `tools/design_audit.go` — visual regression: a `design_audit` tool takes
     a screenshot, compares to `docs/design-references/<screen-kind>.png`,
     flags drift in padding rhythm, type scale, radius, accent use. The
     references are versioned with the design system.
   - `build/gates.go` `bundle` gate — runs `validate_visual` after a clean
     bundle; violations short-circuit to a `heal` round.
   **DoD:** a screen with hardcoded `#ff00aa` is rejected at the `bundle`
   gate with a clear violation message; a token-correct screen passes; the
   acid-lime rule is enforced.
   **Files:** new `tools/validate_visual.go`, `tools/design_audit.go`,
   `build/visual_gate.go`; `docs/design-references/*.png` checked in;
   `tools/registry.go`; tests.

7. **S8 — First-class `ask_user` triage at session start** (1 day).
   `ask_user` is in the spec; the loop supports it. What's missing is the
   proactive 4-question form at session start that saves ten rounds later.
   - 4 questions, ~30 s, single-screen, surfaced from
     `CreateProject.onCreated` → `router.push('/project/[id]')`:
     1. **Goal** (single): wallet / NFT / token / DeFi / utility / other.
     2. **Audience** (single): personal / public / team.
     3. **Visual feel** (single): dense (Linear-ish) / spacious
        (Apple-ish) / bold (Bento-ish).
     4. **Must-have features** (multi): MWA, deep links, push, biometrics,
        in-app browser, file upload, camera, location.
   - Persist answers as the first ADR (`store.ADR`); the model's round-1
     prompt references them by name.
   - The "ambiguity" trigger for `ask_user` during generation still fires
     when needed.
   **DoD:** a fresh project shows the form; after submit, the ADR contains
   the answers; the first round prompt references them.
   **Files:** new `backend/internal/api/triage.go`; new
   `frontend/components/ide/TriageForm.tsx`; new ADR type; tests.

8. **S9 — Export made visible** (2 days, builds on `internal/release/`).
   `release/` already does the work (per-project keystore, scratch-copy
   `assembleRelease`, asset validation, dApp Store manifest). What's missing
   is the surface in the IDE.
   - The `ExportPanel` in the IDE is wired to `POST /v1/projects/{id}/export`.
   - Three artifacts surfaced: signed APK download, manifest, public preview
     link.
   - Pre-export checklist UI: bundle ID, app name, icon (512×512), banner
     (1200×600), screenshots (≥1080, single orientation), SOL cost estimate.
   **DoD:** clicking Export on a real project produces a downloadable signed
   APK, a manifest with no placeholders, a working preview link; the dev
   loop is unaffected while the export runs.
   **Files:** `frontend/components/ide/ExportPanel.tsx` (wiring only);
   `backend/internal/api/export.go`; tests.

9. **P16 — Evals in CI** (3 days, on top of the existing testing docs).
   20-case hand-curated eval set: "build a counter", "build a tip jar",
   "build an NFT viewer with mock fetch", "build a wallet-connect flow",
   etc. Each has an expected end-state (specific files exist, specific gate
   passes, specific vision check matches). CI runs with mocked AI responses
   (recorded from real runs) so it's fast and deterministic. `make evals`
   is the only merge path.
   **DoD:** 20/20 evals pass on `main`; breaking any phase above makes the
   corresponding eval fail.
   **Files:** new `backend/internal/evals/cases/*.yaml`; new
   `backend/internal/evals/runner.go`; `Makefile` target; CI workflow.

10. **P17 — Promote P16's fault-injection tests** (1 day, depends on P16).
    For each phase above, add a fault-injection eval that deliberately breaks
    the feature and confirms the test fails. World-class is "we ship because
    evals passed, not because nobody caught the regression".

## Out of scope (deferred by the user)

- **T — Solana program scaffold** (Anchor / IDL / devnet deploy / IDL client
  bridge). Deferred, together with MCP.
- **MCP / plugin seam.** Scaffold only when T returns; today the per-request
  `tools.Registry` is the right isolation boundary to build on.

## Order of work

```
R2 → A3 → G4 → G5 → G6 → S7 → S8 → S9 → P16 → P17
```

Every phase is a single PR, lands, `go build ./... && go vet ./... && go
test ./...` stays green, `npm run build && npm run lint` stays green for
any frontend touch, and the eval set (P16) grows one case per phase.

## Honest caveats

- The phases I marked "shipped" are based on the `list_files` of
  `backend/internal/agent/content/skills/` (9 dirs) and the text of the
  front-matter that `search_files` returned. I did not re-read
  `agent/agent.go` end-to-end this session. If `SkillsForSymptom` or
  `SkillsForPhase` are stub functions, the A2 / A3-curation row above
  drops back to "in progress" and the real R2 / A3 phases have to cover
  the loader too.
- I did not read `tools/registry.go`, `tools/skill.go`, `tools/runtime.go`,
  or the `api/*` handlers end-to-end this session. The DoD on R2 (clean
  `read_file` / `list_files` schema) assumes both are already correct; if
  not, R2 expands.
- I did not read the frontend source. S8 / S9 names `CreateProject` and
  `ExportPanel` from search hits; the actual component APIs may need
  adjustment when the phase lands.
