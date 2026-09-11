# PLAN_RENAME_TALON.md — SeekerStudio → Talon, everywhere, nothing left behind

Status: READY — awaiting go-ahead. One pass, ordered so the tree is never
broken mid-rename; every step ends green before the next begins.

## The one carve-out (read first)

**"Seeker" is also hardware.** The Solana **Seeker phone** is a real device
this product targets; `Solana Seeker`, `Seeker device`, `Seeker phone` in
prompts, marketing, and metadata describe the *phone*, not the product.
Those survive the rename. The final gate is `git grep -i seeker` returning
**only** those hardware references — everything else is gone.

## The substitution table

| Old | New | Where it lives |
|---|---|---|
| `SeekerStudio` | `Talon` | brand text: layout meta, login, marketing, docs, CI name, Makefile |
| `seekersstudio` | `talon` | go module, JWT issuer, db filenames, e2e scratch, comments |
| `SeekersStudio` | `Talon` | case-variant hits |
| `seeker_studio` / `seekerstudio` | `talon` | mobile applicationId, dir names, misc |
| `seekerstudiomobileapp` | `talonmobileapp` | mobile dir (git mv), CI paths |
| `SeekerStudioMobileApp` | `TalonMobileApp` | app.json name/slug |
| `com.seekerstudio.*` | `com.talon.*` | identity basePackage, template app.json, devclient, prompts, phase guard, maestro appId |
| `seeker-redroid` | `talon-redroid` | docker image tag, containerPrefix, preflight, reconcile |
| `seeker.jwt` / `seeker.session.*` | `talon.jwt` / `talon.session.*` | localStorage + SecureStore keys |
| `SEEKER_BENCH_WS` | `TALON_BENCH_WS` | Makefile bench |
| `github.com/seekersstudio/backend` | `github.com/luckyisrael/talon/backend` | go.mod + ~245 import lines |
| `e2e@seekerstudio.test` | `e2e@talon.test` | e2e driver stub profile |
| **KEEP** | — | `Solana Seeker`, `Seeker device/phone`, `com.solana.mobilewalletadapter.*` (Solana's own fake-wallet/dapp APKs — not ours) |

## Decisions you make (my recommendation first)

1. **GitHub repo name → `Talon`** (display) with go module `github.com/luckyisrael/talon/backend` (lowercase — Go convention; the current module path `github.com/seekersstudio/...` never matched the real remote anyway). You rename the repo in GitHub Settings; I run `git remote set-url`. GitHub redirects old URLs, so nothing breaks during the gap.
2. **Existing user projects (workspaces 30, 31) keep their generated `com.seekerstudio.*` package ids** — those are the user's already-built apps; their ids are data, not branding. New projects get `com.talon.*`. (Alternative: wipe them; say so and I will.)
3. **Rebuild the dev-client APK now** (its package `com.seekerstudio.devclient` is baked into the 271MB prebuilt binary). ~13 min cold gradle. Without it, "nothing left behind" is false inside that APK.

## Steps (each ends with its gate green)

### R1 — safety net
Commit everything; tag `pre-rename-seekerstudio`. Nothing runs on a dirty tree.

### R2 — brand text pass (tracked files, no code semantics)
All `SeekerStudio`-as-product text: `frontend/app/**` (layout meta, login
logo/headline, page, pricing, docs, terms, privacy, og copy), root docs
(PHASE_*.md ×17, PLAN_*.md, DESIGN*.md, GO_DESLOP, CONTENT_WEEK, AGENTS/CLAUDE
files), `backend/internal/bench/BASELINE.md`, `content/*.md`, `.env.example`,
`.github/workflows/ci.yml` (name + comments), `Makefile` (comments,
`TALON_BENCH_WS`).
Carve-out applied: hardware refs untouched. Two-step mechanical method:
first pin the hardware refs (`Solana Seeker`, `Seeker device`, `Seeker
phone`) with a sentinel marker, run the case-ordered substitutions
(`SeekerStudio` → `Talon`, then lowercase variants), then strip sentinels.
Gate: `git grep -in 'seekerstudio'` = 0 hits.

### R3 — Go module rename
`go.mod` module line → `github.com/luckyisrael/talon/backend`; every import
(~245 lines across all `backend/**.go`) sed'd to match; `backend/server`
binary is a stale artifact (gitignored) — deleted. `cd backend && GOCACHE=…
go build ./... && go vet ./... && go test ./...`.
Gate: 39/39 suites green.

### R4 — code-level identifiers (non-module)
JWT issuer const `seekersstudio` → `talon` (`internal/auth/auth.go`;
validation does not check issuer, so live tokens survive). Docker constants:
`containerPrefix = "talon-redroid-"` + reconcile comments (`internal/redroid/`).
e2e: driver stub email, `run-live.sh` scratch names (`talon-e2e.db`,
`workspaces-talon-e2e`), preflight image name `talon-redroid:latest`. `.gitignore`
db names → `talon.db` etc.
Gate: build + vet + affected suites.

### R5 — the package prefix (the deep cut)
`internal/identity/identity.go`: `basePackage = "com.talon"` + the Format
doc comment. `backend/template/app.json`: `com.talon.app`. Devclient package
check in `internal/devclient/manager.go`: `com.talon.devclient`. System
prompt's maestro `appId: com.talon.app` + `internal/ai/phase_guard_test.go`
pins the same. E2E driver's maestro/flow references.
Gate: `make evals` (the phase guard must pass with the new appId — it is
the rename's own regression test).

### R6 — rebuild the two binaries that carry the name
1. **`talon-redroid:latest` docker image** — rebuild from its Dockerfile
   with the new tag; `docker rmi seeker-redroid:latest` after the new one
   proves itself (E2E preflight inspects the tag).
2. **Dev-client APK** — rebuild the template as dev-client with package
   `com.talon.devclient` (~13 min gradle cold), drop it where
   `DevClientAPKPath()` expects. The Solana mock-wallet APK is **not** ours
   and is not rebuilt.
Gate: both artifacts exist under new names; old ones removed.

### R7 — mobile app
`git mv seekerstudiomobileapp talonmobileapp` (+ CI paths, yarn
install-state regenerated by the next yarn command). `app.json`: name
`TalonMobileApp`, slug, android package `com.talon.talonmobileapp` (was
`com.anonymous.seeker_studio_mobile_app`). Storage keys `talon.jwt` /
`talon.session.*` in `lib/auth.ts` / `lib/session.ts` (signs out every
existing install — accepted, pre-launch). Same for the web's token key in
`frontend/lib/auth.ts` if it carries the name.
Gate: `npx tsc --noEmit` + `npx eslint lib hooks app` = 0 problems.

### R8 — runtime surface reset
Stop + remove every `seeker-redroid-*` container and the warm-pool stray.
Wipe e2e scratch (`seekersstudio-e2e.db`, `workspaces-e2e/`). Local dev db
files under old names: removed (already archived in `backups/`).
`backups/` itself is history — the tarball keeps its old name
intentionally (it archives the pre-rename world; renaming inside it is
falsifying history, not cleanup).
Gate: `docker ps` shows no seeker-*; no `seekerstudio*.db` on disk.

### R9 — the directory itself
`mv ~/Desktop/Projects/SeekerStudio ~/Desktop/Projects/Talon` (untracked
name — a plain mv). Update `backend/.env` `SCRCPY_SERVER` to the new path.
Every future session, terminal, and the harness cwd point at `/Talon`.
Gate: `cd Talon && make evals` from the new path.

### R10 — GitHub + provider display names (your 3 clicks)
You: rename repo `Luckyisrael/SeekerStudio` → `Luckyisrael/Talon` (Settings →
rename). Me: `git remote set-url origin git@github.com:Luckyisrael/Talon.git`.
OAuth apps (GitHub + Google): edit the **Application name** to Talon — the
callback URLs contain no product name (`/v1/auth/{provider}/callback`) and
survive untouched. OneSignal: rename the app's display label.
Gate: `git push` lands on the new name; providers list unchanged.

### R11 — the end-to-end proof under the new name
`make e2e-live` (tonight after 00:00 UTC when the gateway quota resets —
this doubles as the pending one-continuous-full-run from PLAN_CORE_PRODUCT).
A green run under `talon-redroid` + `com.talon.app` + the OAuth stub is the
rename's final certificate: account → project → plan → build → device →
metro → kill → restart → relay → cleanup, all Talon.

### R12 — the nothing-left-behind gate
```
git grep -i seeker          # → ONLY Solana Seeker / Seeker device hardware refs
git grep -i seekerstudio    # → 0
ls ~/Desktop/Projects/      # → Talon (no SeekerStudio)
docker images | grep -i seeker   # → 0
```
A short `RENAME.md` records the substitution table and the hardware
carve-out so future greps have their answer key.

## What deliberately does NOT change

- `expokit://` scheme, `com.solana.mobilewalletadapter.*` APKs (Solana's),
  all env var names except `SEEKER_BENCH_WS`, `JWT_SECRET` (sessions
  survive), OAuth client IDs/secrets, OneSignal keys, the phone: Solana
  Seeker.
- Archived backups keep the old name — they are history.

## Risks, named

- **R5/R6 is the only step that can strand the device loop** — if the
  rebuilt devclient APK or image misbehaves, sessions fail honestly
  ("device unavailable"), never lie. The E2E resume-mode run is the fast
  probe (no model needed) before tonight's full run.
- **R7 signs out every mobile/web user** (storage keys renamed). Pre-launch:
  zero cost.
- **R3 touches every Go file** — mechanical, but the suite gate is the
  safety net; a missed import is a compile error, not a silent break.
