# SeekerStudio — Phase 2: Project Bootstrap Overhaul

**Objective:** Eliminate per-project `npm install`/clone latency entirely by replacing it with a baked, versioned template mounted per session. Project creation should be near-instant, dependency state should be shared and consistent across every project, and the scaffold layer should be structurally invisible to the file tree until the AI actually generates something.

**Reference:** `PROJECT_DOCUMENTATION.md` §14.2 (current npm install blocker), `AGENT_ENGINE_SPEC_ADDENDUM.md` §10

---

## 0. Gate Condition — Do Not Start Without This

Phase 0 and Phase 1 must both be fully complete and independently verified, not just claimed complete. This phase assumes a clean, secured, native-Ubuntu environment.

---

## 1. Scope & Non-Goals

**In scope:** template repository structure, the offline/CI build pipeline that produces a ready-to-mount artifact, the mount mechanism itself, template versioning discipline, and the file-tree visibility contract.

**Explicitly out of scope — do not touch in this phase:**
- Per-project identity personalization (app name, slug, bundle id) — that's Phase 3. This phase must leave a clean extension point for it, but does not implement it.
- Multi-session Redroid container orchestration — that's Phase 4. This phase is about the workspace/dependency layer, not the emulator/device layer.
- Baking the native dev-client APK (MWA, Seed Vault) into the template — that's Phase 5. This phase prepares the template structure that Phase 5 will extend, but does not build the dev client itself.

If a change would touch `android.package`, `expo.scheme`, container lifecycle, or native module linking, it does not belong in this phase.

---

## 2. Non-Negotiable Rules

### 2.1 Source of truth is a separate, versioned template repository
The Seeker/Expo template lives in its own git repository, independently versioned — not a folder shuffled around inside the main monorepo, and not something that gets hand-edited in place per project.

### 2.2 All expensive work happens offline, never at request time
Cloning, `npm install`, and any dependency resolution happen in a CI/offline build step triggered by changes to the template repository — never triggered by a user creating a project. If any code path still runs `npm install` in response to a project-creation request, this phase is not done, regardless of anything else that's working.

### 2.3 Template artifacts are immutable once published
The output of the build step (`template:vN`) is never mutated in place. A needed change means cutting a new version, not patching an old one live underneath sessions that are already running against it.

### 2.4 Per-session workspace creation is a mount, not a copy
Creating a new project's workspace must be a filesystem mount/link operation against the shared template artifact — not a recursive file copy, and never a fresh `npm install`. `node_modules` and other shared template content must not be duplicated per project on disk.

### 2.5 A plain recursive copy is not an acceptable fallback
If OverlayFS (or an equivalent copy-on-write mount) isn't viable in the current container setup, the fallback must still preserve the core property this phase exists to guarantee: no per-project install, no per-project duplication of `node_modules`. Hardlink-based copy-on-write or a read-only bind mount are acceptable fallbacks. `cp -r` disguised as "good enough for now" is not — if this shortcut is taken even temporarily, it must be explicitly flagged as temporary, not left silently in place as the permanent behavior.

### 2.6 The scaffold layer is structurally invisible, not flag-gated
The file-tree API must only ever read from the session's own writable layer — never from the shared template layer, regardless of any query parameter. There must be no `includeScaffold`-style flag whose default determines visibility; the correct layer to read is a structural decision in the endpoint itself, not a caller-supplied option.

### 2.7 Template version is pinned per project at creation
Each project records which `template:vN` it was created against, at creation time, and this must never change silently underneath a running project. Migrating a project to a newer template version, if ever needed, is a deliberate, explicit action — never an automatic side effect of a template update.

### 2.8 This phase must not make Phase 3 harder to build
The mount/session-creation flow must leave a clean point immediately after mounting where a personalization pass (renaming, bundle ID generation) can be inserted later, without requiring this phase's design to be reworked. Don't hardcode assumptions — such as a single shared `app.json` being directly served to the client — that would make per-project personalization awkward to bolt on afterward.

### 2.9 No dead code left behind
Once the new bootstrap flow is working, the old per-project clone/install path must be fully removed from the codebase, not left in place unused "just in case." A grep for `npm install` or `git clone` triggered from request-handling code should return nothing.

---

## 3. Verification Checklist

- [ ] Creating a new project completes in near-instant time (order of seconds at most) with zero network calls, zero `npm install`, and zero `git clone` at request time
- [ ] Actual disk usage confirms `node_modules` is not duplicated per project — checked directly, not inferred from speed alone
- [ ] Multiple projects can be created concurrently without corrupting or blocking access to the shared template layer
- [ ] `GET` file-tree endpoint on a freshly created project, before any prompt has been sent, returns an empty result — tested directly, not assumed from the code
- [ ] Each project has a recorded template version at creation, and that value does not change on its own afterward
- [ ] Publishing a new template version does not alter the behavior of any project already created against an older version
- [ ] A repository-wide search confirms no request-handling code path still triggers `npm install` or `git clone`
- [ ] The chosen mount mechanism (OverlayFS or a documented fallback) is confirmed working specifically on the native Ubuntu environment from Phase 0 — not assumed from general documentation about how it should work

---

## 4. Definition of Done

- Every item in §3 is independently confirmed by actually performing the check (creating a project and timing it, inspecting real disk usage, hitting the file-tree endpoint directly) — not by reading the code and assuming it behaves as designed.
- The old per-project install/clone path is fully removed, not merely unused.
- A fresh project can be created, its file tree confirmed empty, and its template version confirmed recorded — all without a single network call beyond what the mount mechanism itself requires.

**Do not proceed to Phase 3 until every box above is genuinely checked, not estimated.**
