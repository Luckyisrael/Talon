# SeekerStudio — Phase 0: OS Migration & Environment Parity

**Objective:** Establish native Ubuntu as the permanent development environment, identical in distro and kernel family to the production VPS target. Remove every WSL2-specific accommodation from both the infrastructure and the codebase. This phase produces zero new features — it produces a clean, honest foundation everything else gets built on.

**Reference:** `PROJECT_DOCUMENTATION.md` §2 (Environment & Infrastructure), §14 (Current State & Blockers)

---

## 0. Gate Condition — Do Not Start Without This

Before any work in this phase begins, confirm:
- The previously exposed `ANTHROPIC_AUTH_TOKEN` has been rotated.
- `.env` (and any file containing secrets) is listed in `.gitignore` before the first commit is made from the new environment.

This is not the full security phase (that's Phase 1) — it's a non-negotiable precondition so a fresh environment doesn't inherit a leaked credential on day one.

---

## 1. Scope & Non-Goals

**In scope:** operating system, kernel, Docker, Redroid container launch, SDK/toolchain installation, and any code that currently branches on "am I on Windows/WSL" behavior.

**Explicitly out of scope — do not touch in this phase:**
- Orchestrator logic, agentic tools, staging/promote mechanics
- Template baking / OverlayFS (Phase 2)
- Per-project identity personalization (Phase 3)
- Multi-session Redroid orchestration (Phase 4)
- Any AI system prompt or guardrail content

If a change would require editing files under `internal/orchestrator`, `internal/tools`, `internal/staging`, or `internal/guardrails`, it does not belong in this phase — flag it for the relevant later phase instead of folding it in here.

---

## 2. Non-Negotiable Rules

### 2.1 No WSL-specific code paths may remain
Search the entire codebase for anything that assumes a Windows host talking into a Linux VM, and remove it — not comment it out, remove it:
- Any invocation pattern that shells out through `wsl -d SeekerLinux -e ...` before running a Linux command. On native Ubuntu, the Go backend runs on the same OS as Redroid and Docker — there is no cross-boundary to bridge anymore. `adb`, `docker`, `npm`, `expo`, `gradlew` must be invoked directly.
- Any Windows-specific filesystem lookups: `LOCALAPPDATA` environment variable checks, hardcoded `C:\` paths, `where java`-style Windows command discovery.
- Any dual-path logic that checks "if Windows, do X, else do Y" — collapse to the Linux-only path and delete the other branch entirely.

### 2.2 No custom kernel build unless proven necessary
The WSL2 custom kernel (`binderfs=y`, `memfd_create=y`, ashmem patched out) existed to work around WSL2's default kernel lacking Android binder support. A modern Ubuntu 24.04 kernel typically has binder support already, either built in or as a loadable module.

**Rule:** do not rebuild a custom kernel as a first step. First verify what the stock Ubuntu kernel already provides. Only pursue custom kernel measures if verification proves something is genuinely missing — and if so, document exactly what's missing and why, rather than porting the WSL2 kernel config wholesale on the assumption it's still needed.

### 2.3 No NAT networking workaround
The `.wslconfig` NAT fix existed because WSL2 had no default gateway. This does not apply to a native Ubuntu install with normal networking. Do not carry this configuration forward in any form — Docker's standard bridge/host networking should work without any equivalent patch. If networking issues appear, diagnose them as native Ubuntu/Docker networking issues, not as a variant of the old WSL problem.

### 2.4 No apt mirror patching unless the same failure reproduces
The `old-releases.ubuntu.com` → `archive.ubuntu.com` fix was specific to a particular WSL image's default sources. A fresh Ubuntu 24.04 install should have working default sources. Do not pre-emptively apply this patch — only apply it if the identical 404 failure is actually observed.

### 2.5 SDK and toolchain discovery must be Linux-native only
Any code (e.g. `findJavaHome()`, `findAndroidHome()`) that currently checks Windows-style locations must be reduced to Linux-only discovery: standard environment variables (`JAVA_HOME`, `ANDROID_HOME`/`ANDROID_SDK_ROOT`), and standard Linux install paths. No Windows fallback branches should remain in this logic at all.

### 2.6 Infra scripts must match the target shell environment
Any existing PowerShell scripts (`.ps1` files under `infra/redroid/`) must be rewritten as POSIX shell scripts. This is a full rewrite in the native tool for the new environment, not a translation layer or wrapper that shells out to PowerShell equivalents.

### 2.7 Docker must be the standard Linux installation
Docker CE installed the standard native Linux way — no Docker Desktop, no WSL integration settings, no VM-backed Docker of any kind. Daemon configuration should be re-derived for a native host, not copied verbatim from the WSL daemon config — re-verify each setting (`iptables`, `bridge`, `storage-driver`) is still the correct choice on bare Ubuntu rather than assuming the WSL-era config is still appropriate.

### 2.8 Fresh installs only — no carried-over artifacts
Re-clone the repository fresh on the new OS. Do not copy `node_modules`, build caches, or any generated artifacts from the WSL environment over to the new one. The entire point of this phase is a clean environment with no inherited state — installing from a stale WSL-built artifact defeats that.

### 2.9 This environment is now permanent, not disposable
Once Phase 0 is complete, this native Ubuntu install is the standing development environment — not a temporary test that gets torn down. Its distro version must match the intended production VPS distro exactly. If the VPS target changes later, this phase must be re-run against the new target rather than assumed still valid.

---

## 3. Environment Verification Checklist

Every item below must be independently re-verified on the native Ubuntu install — do not assume parity, confirm it:

- [ ] Ubuntu version matches the intended VPS distro/version exactly (not just "a recent Ubuntu")
- [ ] Docker CE installed natively, daemon running without any WSL-era config assumptions
- [ ] Android binder support confirmed present in the stock kernel (via the standard filesystem/module check) — no custom kernel built unless this check fails
- [ ] Java (matching version used previously — 17.x) installed and discoverable via `JAVA_HOME`
- [ ] Node.js (matching version — 22.x) installed via the standard Linux method
- [ ] Go (matching version — 1.25.x) installed
- [ ] Android SDK installed at a standard Linux location, with the same build-tools/platform versions previously in use, discoverable via `ANDROID_HOME`
- [ ] Redroid container launches successfully with the same image (`redroid/redroid:14.0.0_64only-latest`) and boots at the same resolution/DPI (1080×2400 @ 420)
- [ ] ADB connects directly (`localhost:5555`) with no WSL bridging step in between
- [ ] Outbound internet and DNS resolution confirmed from the host directly (no equivalent of the old WSL networking fix needed)
- [ ] Repository cloned fresh; `npm install` and `go build ./...` complete without the previous 120-second timeout symptom (this alone strongly suggests the WSL I/O layer was the actual cause, not npm or network conditions — confirm this theory holds true)

---

## 4. Codebase Audit Requirement

Before marking this phase complete, produce a list (not a fix — an audit list first) of every file that contains Windows-specific or WSL-invocation logic. For each one, confirm it has been reduced to a single Linux-only path per §2.1 and §2.5. This audit list itself is a deliverable of this phase — it's how you confirm nothing was missed, and it becomes a reference if any of this logic is accidentally reintroduced later.

---

## 5. Definition of Done

- Every item in §3's checklist is independently confirmed, not assumed.
- The §4 audit list exists and every flagged file has had its WSL/Windows-specific branches removed.
- No `.ps1`, `.wslconfig`, WSL NAT config, or `wsl -d` invocation remains anywhere in the repository.
- A fresh `git clone` on this machine, followed by dependency install and a full build, succeeds with no manual path correction, no timeout workaround, and no environment-specific special-casing.
- This environment is confirmed to be the same distro/kernel family as the intended VPS target — stated explicitly, not inferred.

**Do not proceed to Phase 1 until every box above is genuinely checked, not estimated.**
