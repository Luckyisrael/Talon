# SeekerStudio — Agent Guide

## UI / Frontend work
When building or editing ANY UI (frontend/), you MUST follow the rules in
`DESIGN_SYSTEM.md` (Material You + Bento Grid + Swiss Minimalism). Read it before
starting UI work and obey its constraints (44px targets, pill controls, tonal
surfaces, spacing scales, corner nesting rule).

## Project docs
- `Solana_Mobile_Vibe_Coding_Guide.md.txt` — product blueprint & system spec (Go backend, ws-scrcpy streaming, Redroid, freemium).
- `IMPLEMENTATION_ROADMAP.md` — phase-by-phase build plan with Definition-of-Done gates. Follow it in order.
- `contracts/` — shared API endpoint definitions.

## Backend (Go)
- Module lives in `backend/`. Run `go build ./...` and `go vet ./...` after changes.
- Layout: `cmd/server/` (entrypoint), `internal/` (packages). See spec §4 for package map.

## Frontend (Next.js)
- Lives in `frontend/`. App Router + TypeScript + Tailwind. Run `npm run build` (and lint) after changes.
