# SeekerStudio — Agent Guide

## UI / Frontend work
When building or editing ANY UI (frontend/), you MUST follow the rules in
`DESIGN_SYSTEM.md` (Material You + Bento Grid + Swiss Minimalism). Read it before
starting UI work and obey its constraints (44px targets, pill controls, tonal
surfaces, spacing scales, corner nesting rule).

## Architecture
- **Redroid** = pure Android device emulator (APK runner + screenshot-based streaming). No Solana inside.
- **Mock MWA Wallet** = pre-installed on Redroid for testing wallet connection, tx signing, etc.
- **Host (Ubuntu)** = Solana CLI, build tools, Go backend, all compile/deploy work.
- **Generated apps** = React Native apps that connect to wallet via MWA protocol.

## Project docs
- `Solana_Mobile_Vibe_Coding_Guide.md.txt` — product blueprint & system spec.
- `IMPLEMENTATION_ROADMAP.md` — phase-by-phase build plan with Definition-of-Done gates. Follow it in order.
- `contracts/` — shared API endpoint definitions.

## Backend (Go)
- Module lives in `backend/`. Run `go build ./...` and `go vet ./...` after changes.
- Layout: `cmd/server/` (entrypoint), `internal/` (packages). See spec §4 for package map.

## Frontend (Next.js)
- Lives in `frontend/`. App Router + TypeScript + Tailwind. Run `npm run build` (and lint) after changes.

## Android / Redroid
- Redroid boots with `androidboot.use_memfd=1` (no ashmem in kernel).
- ADB connects on `localhost:5555` directly (native Linux, no WSL bridge).
- Mock wallet install: `infra/redroid/install-mock-wallet.sh` (requires JDK 17 + Android SDK).

## Docker / Redroid networking rule
NEVER run redroid (or any Docker container) with `--network host`. This breaks the host
machine's routing table because Android's netd inside the container fights NetworkManager
for control of the default route, causing intermittent internet loss on the host.

Instead, use Docker's default bridge network and expose only the required ports explicitly, e.g.:
```
docker run -d --name redroid-seeker-dev --privileged -p 5555:5555 redroid/redroid:14.0.0_64only-latest androidboot.use_memfd=1 androidboot.redroid_width=1080 androidboot.redroid_height=2400 androidboot.redroid_dpi=420
```

Also always clean up old/orphaned redroid containers before starting a new one:
```
docker ps -a | grep redroid
docker rm -f <old_container_name_or_id>
```
