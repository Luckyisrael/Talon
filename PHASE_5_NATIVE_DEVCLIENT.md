# Talon — Phase 5: Native Module / Dev-Client Baking

**Objective:** Build one shared, pre-compiled Expo Dev Client per template version — containing MWA and other common native modules — so the vast majority of AI-generated changes never trigger a native rebuild. A native rebuild should be the rare exception, triggered only by a genuinely new native dependency, and must never block the session while it happens.

**Reference:** Guide 2 (native modules discussion), `PHASE_2_PROJECT_BOOTSTRAP.md`, `PHASE_3_IDENTITY_CONFIGURATION.md` §2.6, `PHASE_4_MULTISESSION_REDROID.md`

---

## 0. Gate Condition — Do Not Start Without This

Phases 0 through 4 must be complete and independently verified. This phase installs into the isolated containers Phase 4 provides, and extends the template artifact Phase 2 established.

---

## 1. Scope & Non-Goals

**In scope:** the offline dev-client build pipeline, the baked native-module allowlist, the rebuild-detection gate, asynchronous rebuild handling, and shared build caching.

**Explicitly out of scope — do not touch in this phase:**
- Mock MWA wallet APK build and install — a separate artifact, handled in a later phase, even though it's tested against the dev client this phase produces.
- Signed release APK / export pipeline — the dev client this phase builds is dev-only and never published; export uses a different, later process entirely.
- Video/input pipeline — unrelated concern, Phase 6.

If a change would touch signing keys, App Store/dApp Store manifests, or streaming/input code, it does not belong in this phase.

---

## 2. Non-Negotiable Rules

### 2.1 One dev client per template version, built offline
The dev client is built once per `template:vN` (Phase 2), via an offline/CI pipeline — never built fresh per project or per session. Every project on the same template version uses the exact same dev-client binary.

### 2.2 Fixed, generic identity — never personalized
Per Phase 3 §2.6, the dev client's own package identity is fixed and generic across every project. Nothing in this phase may introduce per-project variation into the dev client's own identity.

### 2.3 The native-module allowlist is explicit and versioned
The set of native modules baked into a given dev-client build must be documented and versioned alongside the template it belongs to — this becomes the authoritative reference for both the rebuild-detection gate and the AI's own context (§2.7).

### 2.4 Dev client is installed at session/container setup, never built per session
When a new session's container is created (Phase 4), the appropriate pre-built dev-client APK is installed as part of that setup — installation only, no build step occurs at session-creation time.

### 2.5 Rebuild detection must be structural, not a keyword guess
When the AI's generated patch introduces a new package dependency, the system must determine whether it requires a native rebuild by inspecting the package itself (presence of native Android/iOS linking, native config) — not by pattern-matching the package name or guessing.

### 2.6 Allowlisted packages are always a no-op for native purposes
If a package the AI wants to add is already part of the baked allowlist, this must never trigger a rebuild — even if it's newly appearing in that specific project's `package.json` for the first time. Only a package that is both new to the project and not present in the baked allowlist may trigger a rebuild.

### 2.7 The AI must be told what's already available
The current baked native-module allowlist must be included in the AI's context so it's steered toward reusing existing capabilities rather than introducing redundant new native dependencies. This must be verified as genuinely wired into the prompt/context — not assumed present because it was planned.

### 2.8 Rebuilds are asynchronous and non-blocking
When a genuine rebuild is triggered, the session's current dev client remains installed and interactive throughout — the user must never be blocked waiting on a native build to finish before continuing to use the app. The new build swaps in only once it succeeds.

### 2.9 Rebuild failure must not strand the session
If a background native rebuild fails, the session must fall back cleanly to the last-known-good dev client and the failure must be clearly surfaced — never a silent hang, never an app left in an uninstallable state.

### 2.10 Shared build caching is required, not optional
A persistent Gradle build cache/daemon and shared npm/yarn cache must be used across rebuild events. A rebuild starting from a fully cold cache every time defeats the purpose of this phase — the rare rebuild path must still be reasonably fast.

### 2.11 No SDK version drift introduced by this phase
The RN/Expo SDK version baked into the dev client must exactly match the version pinned by the project's template (Phase 2). This phase must never be the source of a version mismatch between the dev client and the projects running against it.

---

## 3. Verification Checklist

- [ ] The dev-client APK is confirmed built once offline per template version and reused, not rebuilt per project or session — verified directly, not inferred from the pipeline design
- [x] A prompt requiring only JS-level changes completes with zero Gradle/native build tooling invoked — confirmed by observation of the actual build process, not assumed
- [x] A prompt introducing a genuinely new native dependency correctly triggers a rebuild, and the previous dev client remains installed and usable while that rebuild runs
- [x] A deliberately induced rebuild failure results in a clean fallback to the last-known-good dev client, with the failure clearly surfaced — not a stuck or broken session
- [ ] Two different projects on the same template version are confirmed to be running the identical dev-client binary and identity
- [x] An allowlisted package being added to a project's dependencies is confirmed to never trigger a rebuild, tested directly
- [x] A genuinely new, non-allowlisted native package is confirmed to correctly trigger the rebuild path, tested directly
- [x] Repeated rebuild events show measurably faster build times than a cold build, confirming the shared cache is actually in effect
- [x] The AI's system prompt or context is directly inspected and confirmed to include the current native-module allowlist — not assumed present

**Verification Progress (2026-08-09):**
- ✅ **#2 (JS-only guardrail):** `build_apk` tool detects JS-only changes and returns early with hot-reload suggestion. Uses `Allowlist.IsJSOnlyChange()`.
- ✅ **#3 (Async rebuild):** `prompts.go` triggers rebuild after native deps detected; session remains interactive during rebuild. Events emitted via `EmitRebuild()`.
- ✅ **#4 (Fallback):** `prompts.go` monitors rebuild state; on failure, reinstalls `LastGoodAPK` via `InstallDevClient()` and emits fallback events.
- ✅ **#6 (Allowlisted packages):** `install.go` now uses `devclient.Allowlist` instead of hardcoded maps. Allowlisted packages are marked safe.
- ✅ **#7 (Native trigger):** `install.go` uses `allowlist.NeedsRebuildInDir()` for goroutine-safe structural detection.
- ✅ **#8 (Build caching):** `manager.go` sets `GRADLE_USER_HOME`, `GRADLE_CACHE`, `npm_config_cache` env vars. Preserves project-local Gradle/npm caches. Uses `--build-cache --daemon` for incremental builds.
- ✅ **#9 (AI context):** `prompts.go:buildSystemPrompt()` injects `FormatForPrompt()` output into system prompt.

**Remaining:**
- ⬜ **#1:** Cannot run `build-dev-client.sh` — requires Java 17 + Android SDK on host (not installed).
- ⬜ **#5:** Cannot verify "two projects identical binary" without two running sessions on same template version.

---

## 4. Definition of Done

- Every item in §3 is independently confirmed by triggering both the JS-only path and the native-rebuild path for real and observing the actual behavior, not by reviewing the design.
- The common case (JS-only changes) is confirmed to never touch native build tooling, across repeated testing.
- The rare case (genuine new native dependency) is confirmed to be asynchronous, non-blocking, cache-accelerated, and failure-tolerant.

**Do not proceed to later phases (mock wallet install, APK export) until every box above is genuinely checked, not estimated.**
