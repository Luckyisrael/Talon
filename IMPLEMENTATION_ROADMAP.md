# SeekerStudio — Implementation Roadmap (Start → Production)

Phase-by-phase guide to build and test SeekerStudio until it is production-ready.
Each phase has a **Definition of Done (DoD)** — do not start the next phase until the DoD is met.

**Reference spec:** `Solana_Mobile_Vibe_Coding_Guide.md.txt`
**Locked decisions:** Go backend · ws-scrcpy H.264 streaming · Redroid on own hosts · Freemium with $59 Pro + metered overage.

---

## Environment Baseline (verified)

| Tool | Version | Notes |
|---|---|---|
| Docker CE | 29.1.3 | Running inside SeekerLinux WSL distro (Docker Desktop is unused — broken with custom kernel) |
| WSL2 | 2.x | `SeekerLinux` distro (Ubuntu 24.04, systemd enabled) with custom kernel 6.6.87.2+ |
| Custom Kernel | 6.6.87.2+ | `binderfs=y` (built-in), `memfd_create=y`. Ashmem not available — use `androidboot.use_memfd=1`. |
| Go | 1.25.2 | windows/amd64 |
| Node | 22.23.1 | |
| Git | 2.55.0 | |

**Dev platform:** Redroid runs inside SeekerLinux (WSL2) via Docker CE with `--privileged --network host`. Android's `/init` boots with `androidboot.use_memfd=1` (no ashmem). ADB connects over TCP on `localhost:5555`.

---

## Phase 0 — Repo Bootstrap & Foundations

**Objective:** Create the monorepo skeleton so every later phase lands in a known place.

- Layout:
  ```
  SeekerStudio/
  ├─ backend/         # Go API server (orchestrator, container mgr, streaming+terminal proxy, billing)
  ├─ frontend/        # Next.js IDE (Monaco + canvas + xterm.js terminal + prompt panel)
  ├─ infra/           # Dockerfiles, compose, WSL kernel build scripts, host provisioning
  ├─ contracts/       # shared API types / OpenAPI spec
  ├─ IMPLEMENTATION_ROADMAP.md
  └─ Solana_Mobile_Vibe_Coding_Guide.md.txt
  ```
- `git init`, `.gitignore` (Go, Node, Android, .env), initial commit.
- `backend/`: `go mod init github.com/<you>/seekersstudio/backend`, empty `cmd/server/main.go` that serves `GET /healthz`.
- `frontend/`: `npx create-next-app@latest frontend` (App Router, TS, Tailwind).
- Contracts: define `POST /v1/prompts`, `POST /v1/sessions`, `POST /v1/sessions/{id}/pause|resume`, `GET /v1/sessions/{id}/stream` (WS), `GET /v1/sessions/{id}/terminal` (WS), `GET /v1/usage`, `POST /v1/builds` (APK).

**Testing / Verification:**
- `go build ./...` and `go vet ./...` pass in `backend/`.
- `npm run build` passes in `frontend/`.
- `curl localhost:8080/healthz` returns 200.
- `git log` shows clean initial commit.

**DoD:** Repo builds cleanly; HTTP health endpoint responds; contracts doc exists.

---

## Phase 1 — Local Runtime: WSL2 Kernel + Redroid

**Objective:** Boot a Redroid Android 14 container at Seeker resolution on the local machine.

**Status:** ✅ COMPLETE

- Created `SeekerLinux` WSL2 distro (Ubuntu 24.04, systemd enabled via `/etc/wsl.conf`).
- Built custom WSL2 kernel `6.6.87.2-microsoft-standard-WSL2+` with:
  - `CONFIG_ANDROID_BINDER_IPC=y` (built-in)
  - `CONFIG_ANDROID_BINDERFS=y` (built-in)
  - `CONFIG_MEMFD_CREATE=y` (built-in)
  - Ashmem (`CONFIG_ASHMEM`) is **not available** in this kernel version — Redroid uses `androidboot.use_memfd=1` instead.
- Docker CE 29.1.3 installed inside SeekerLinux with daemon config: `iptables:false`, `bridge:none`, `ip-forward:true`, `storage-driver:overlay2`.
- Redroid container boots at Seeker resolution: **1080×2400 @ 420 DPI**.
- ADB connects over TCP: `localhost:5555`.
- VHDX compacted: 13.79 GB → 3.51 GB. Free disk: ~22 GB.

**Redroid is used purely as an Android device emulator** — it runs APKs and streams via ws-scrcpy. No Solana CLI, test validators, or smart contract tooling inside the container.

**DoD:** ✅ Redroid boots and is ADB-controllable at Seeker resolution on the dev machine.

---

## Phase 2 — Container Image & Device Setup

**Objective:** Prepare the Redroid container for running Seeker mobile apps.

**Status:** ✅ COMPLETE

Redroid is a pure Android device — it runs APKs and streams via ws-scrcpy. No Solana tooling inside.

- Container launched with `androidboot.use_memfd=1` (replaces deprecated ashmem).
- ADB TCP enabled on port 5555.
- Device PIN set programmatically via `locksettings set-pin` (required for MWA wallet sessions).
- Container persists app data via Docker volume mounts.

**Testing / Verification:**
- `adb devices` shows `localhost:5555  device`.
- `getprop` resolution/DPI match Seeker (1080×2400 @ 420).
- `screencap` produces a valid PNG.

**DoD:** ✅ Container boots, ADB works, ready for APK deployment.

---

## Phase 3 — Go Backend: Core API

**Objective:** Backend that serves the frontend, manages AI prompts, and orchestrates the dev environment.

- `backend/internal/api` — HTTP routes: sessions CRUD, prompts, builds, usage.
- `backend/internal/auth` — bearer/session tokens; `backend/internal/store` — SQLite (users, projects, sessions, usage).
- `backend/internal/billing` — device-minute + prompt metering records (Stripe wiring deferred to Phase 6/8).
- `backend/internal/adb` — ADB command proxy (connect, install APK, shell, screenshot, logcat).
- HTTP routes: sessions create/pause/resume/destroy, projects CRUD, usage.

**Testing / Verification:**
- `go test ./...` green (manager lifecycle tests via FakeManager; handler tests via httptest).
- Manual: create session → pause → resume → destroy via curl against DockerManager.

**DoD:** API + lifecycle fully tested; real container management works locally.

---

## Phase 4 — Streaming Gateway (Go → ws-scrcpy) + Web Terminal

**Objective:** Live preview in the browser with authentication, plus an in-browser terminal.

- Run `ws-scrcpy` per user session (proxy over ADB to the container) with `INCLUDE_ADB_SHELL` enabled (bundles an xterm.js ADB shell).
- `backend/internal/streaming` — Go terminates the user-facing WSS (TLS + per-session bearer token), proxies frames/input to the per-user ws-scrcpy instance. One container/port per session; kill on session end.
- `backend/internal/terminal` — authenticated terminal socket: xterm.js ↔ Go proxy ↔ `adb shell` (or `docker exec` into the container shell). Supports copy/paste and multi-session.
- `frontend` — phone-frame `<canvas>`/`<video>` component wired to the streaming WS; use ws-scrcpy's input pipeline (touch, keyboard, clipboard) via `postMessage`.
- `frontend` — terminal panel (xterm.js) in the IDE layout for running `adb shell`, `solana` CLI, Metro, and Gradle commands.

**Testing / Verification:**
- Browser shows the live Redroid screen at 30 FPS (free) / 60 FPS (pro) in the phone frame.
- Tap/drag/keyboard from the browser reaches the container (assert via UI state + screencap).
- Terminal renders in the IDE; `adb shell getprop` and `solana --version` run inside the container from the browser.
- Unauthenticated WS/token connections are rejected.

**DoD:** Authenticated, interactive live preview + working web terminal stream to the frontend.

---

## Phase 5 — AI Orchestrator & Self-Healing Loop

**Objective:** The core "vibe coding" loop: prompt → code → verify → self-heal.

- `backend/internal/prompts` — system prompt constants (from spec §5 Phase 2).
- `backend/internal/orchestrator` — pipeline:
  1. Capture prompt (WS/REST) with project tree context.
  2. LLM returns structured **JSON patch**; route ~80% of routine requests to a cheap flash-tier model, Sonnet-class for complex rewrites.
  3. **Typecheck gate:** run `tsc --noEmit` on patched files before writing.
  4. Write to mounted workspace volume; `fsnotify` + Metro push Fast Refresh.
  5. **Verify:** tail `adb logcat` (`ReactNativeJS:E`, `AndroidRuntime:E`) + `adb exec-out screencap`.
  6. **Heal:** on error, send message+stack+file+tree back to LLM; max 3 attempts with watchdog timer.
  7. **Vision check:** post-bundle screenshot to a vision model to confirm UI matches intent.
- Generated app root must include a dev-only `<ErrorBoundary>` rendering error text (OCR-able).

**Testing / Verification:**
- End-to-end demo: "build a connect-wallet screen" appears in preview and functions.
- Deliberately break generated code → loop self-heals within 3 attempts (log asserts).
- Typecheck gate blocks invalid patches before disk write (unit test).

**DoD:** Prompt→working-feature-in-preview loop with proven self-healing.

---

## Phase 6 — Freemium Gating & Metering

**Objective:** Enforce tiers, limits, and overage.

- Tier resolution in `internal/auth` + `internal/billing`.
- Meter device-minutes and prompts against budgets (Free: 60 min / 20 prompts; Pro $59: 1,500 min / 500 prompts).
- Idle pause (5 min → `docker pause`) + destroy at 15 min; **warm paused pool** (3–5 pre-booted per host) for ~2s resume.
- Pro overage: $0.02/device-min, $0.01/prompt (Stripe test-mode metering).
- APK export route gated on `is_pro`.
- Usage metering UI in frontend.

**Testing / Verification:**
- Free user: pauses at 5-min idle, blocked at 20 prompts / 60 min, overlay shows "Session Paused… Resume".
- Pro user: overage increments recorded; resume instant from warm pool.
- APK route returns 403 for free, 200 for pro.

**DoD:** All tier limits enforced end-to-end; metering recorded and visible in UI.

---

## Phase 7 — Pro Features

**Objective:** APK export, dApp store bundle, broader networks.

- APK build pipeline: `cd android && ./gradlew assembleRelease`, sign with per-user keystore, serve download.
- `dapp-store-manifest.json` 1-click generation & packaging.
- Per-tier RPC config: Free=devnet/localhost; Pro adds testnet + mainnet-beta.

**Testing / Verification:**
- Signed APK downloads and installs on a device/emulator.
- Generated manifest passes the Solana dApp Store validator (if available) / matches schema.
- Network selector appears only for Pro.

**DoD:** A user can go from generated app → downloadable signed APK + store bundle.

---

## Phase 8 — Production Hardening & Deployment

**Objective:** Deployable, secure, observable.

- **Containerize**: `infra/` Dockerfiles for backend + frontend; compose for local prod-like run; staging on the target host.
- **Hosts**: dedicated x86_64 Ubuntu 24.04 (Hetzner/OVH). Provision script: pin kernel, load binder modules at boot (`/etc/modules-load.d/redroid.conf`), **module probe gate** (`grep binder /proc/filesystems`) before serving.
- **Security**: TLS/WSS everywhere, secrets via env/secret manager, rate limits, per-session isolation, no unencrypted ws-scrcpy exposure.
- **Observability**: structured logs, Prometheus metrics (containers, device-minutes, AI spend), cost/burn-rate dashboard per user.
- **Reliability**: backups of workspace volumes, container restart policies, load test (concurrent sessions per host), failover/restore runbook.
- **CI**: GitHub Actions — Go tests, `tsc` typecheck, frontend build, image build.

**Testing / Verification:**
- Deploy to a staging dedicated host; full smoke test (login → create project → AI prompt → preview → APK).
- Load test: N concurrent sessions on one host stays under CPU/RAM budget; warm pool replenishes.
- Burn-rate dashboard shows per-user AI + host-minute cost.

**DoD:** Staging deployment passes smoke + load tests with monitoring live; runbook documented.

---

## Cross-cutting Risks & Mitigations

| Risk | Mitigation |
|---|---|
| WSL2 kernel lacks binder | Custom kernel with `binderfs=y` built-in; verified via `/proc/filesystems` |
| Ashmem not available | Use `androidboot.use_memfd=1` — memfd_create is built into the kernel |
| H.264 encoding at 60 FPS is CPU-heavy | Hardware encode on hosts (NVIDIA T400/NVENC or iGPU QuickSync); free tier capped at 30 FPS |
| AI token cost erodes margin | Model routing (flash for routine), per-prompt output caps, burn-rate dashboard |
| Concurrent session saturation | Warm paused pool + concurrency capacity model (≈8 active/host on 16vCPU/64GB) |
| ws-scrcpy unauthenticated by default | Go WSS termination + per-session tokens; never expose ws-scrcpy directly |
| Metro Fast Refresh state loss | Generated code enforces named default exports; ErrorBoundary in root |

---

## Suggested Next Steps

1. ✅ **Phase 0** — repo scaffolded.
2. ✅ **Phase 1** — custom kernel + Redroid boot at Seeker resolution.
3. ✅ **Phase 2** — container ready for APK deployment.
4. **Phase 3** — Go backend API (serves frontend, manages prompts, proxies ADB).
5. **Phase 4** — ws-scrcpy streaming + web terminal (live preview in browser).
6. **Phase 5** — AI orchestrator (prompt → code → build → deploy → preview loop).
