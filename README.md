# Talon

> Previously named **SeekerStudio** — renamed to Talon over a naming and keyword conflict.

A browser IDE, a companion mobile app, and an AI build agent for the Solana Mobile ecosystem. Describe an app in plain language and the agent plans the screens, writes React Native/Expo code, verifies it through typecheck, lint, device boot, vision, and interaction gates, and ships it to a live device or your phone over Metro.

![Status](https://img.shields.io/badge/status-Alpha-orange)
![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go)
![Next.js](https://img.shields.io/badge/Next.js-16.3-black?logo=next.js)
![Expo](https://img.shields.io/badge/Expo_SDK-55-000020?logo=expo)

---

## What it is

- **Web IDE** (`frontend/`) — chat-driven agent workspace: conversation with live tool trace, plan approval cards, changed-files diffs, code editor, terminal, command palette, and a device panel with emulator stream or Metro relay to a real phone.
- **Companion app** (`talonmobileapp/`) — the same agent in your pocket: Build with streaming trace and attachments, per-file Diff, Metro-connected Device tab, project dashboard, history, settings, help, and docs. Android only.
- **Build agent** (`backend/`) — Go orchestrator running plan and build turns: tool calling with guardrails, verify ladder (static, runtime, vision, interact), self-healing, checkpoints and undo, conversation persistence with resume, steer, and ask-user pauses.
- **Auth** — GitHub/Google OAuth plus Solana wallet sign-in, short-lived access tokens with rotating refresh tokens, per-device sessions with revocation.
- **Attachments** — images and markdown docs ride turns end to end: client validation and downscale, hash-pinned storage (Cloudflare R2 for images, local disk fallback), provider-native image parts in build turns.
- **History** — server-owned transcripts on every device: paged conversation API, turn index, full-text search, jump-to-turn, offline snapshots on mobile. Conversations are never deleted; removing a project removes its transcript with it.
- **Plugins** — public packs the agent fetches at build time ([talon-plugin-directory](https://github.com/Luckyisrael/talon-plugin-directory)): web3 components today, more Solana packs as they land. Documented under Docs, Plugins in the web app.

## Architecture

```
┌──────────────────────────────────────────────────────────┐
│              WEB FRONTEND (Next.js 16)                   │
│  AI chat · trace · plan cards · diffs · editor · terminal│
│  PhonePreview (emulator stream / Metro relay + QR pair)  │
└──────────────┬───────────────────────┬───────────────────┘
               │ REST / SSE            │ WebSocket
┌──────────────▼──────────────┐  ┌─────▼───────────────────┐
│    AI ORCHESTRATOR (Go)     │  │   STREAMING GATEWAY     │
│  plan/build loops · tools   │  │  emulator frames · adb  │
│  guardrails · verify ladder │  │  input · terminal       │
│  attachments · history      │  └────────────┬────────────┘
└──────────────┬──────────────┘               │
               │ writes code      ┌───────────▼───────────┐
┌──────────────▼─────────┐       │   REDROID CONTAINER   │
│   METRO (per session)  │       │  Android 14 · Docker  │
│   bundler · HMR relay ─┼──────►│                       │
│   phone deep links     │       └───────────────────────┘
└────────────────────────┘
         ▲                              ▲
         └────── COMPANION APP ──────────┘
         (Expo 55 · same API · SecureStore sessions)
```

## Project structure

```
Talon/
├── backend/               # Go API server
│   ├── cmd/server/        # Entrypoint, route wiring
│   ├── internal/
│   │   ├── ai/            # Provider clients, message blocks, serializers, compaction
│   │   ├── api/           # Handlers: auth, prompts/stream, conversation, history,
│   │   │                  #   attachments, sessions, metro, test-info, export, memory
│   │   ├── auth/          # JWT access + rotating refresh tokens
│   │   ├── blobstore/     # Attachment bytes: Cloudflare R2, local fallback
│   │   ├── harness/       # Agent loop, skills catalog, instructions, guardrails,
│   │   │                  #   session log, sandbox, workflow, compaction
│   │   ├── knowledge/     # Embedded skills (mobile-craft/motion, wallet-mwa,
│   │   │                  #   solana-dev, motion-review, …) with attribution
│   │   ├── orchestrator/  # Build sessions, verify gates, self-healing, checkpoints
│   │   ├── store/         # SQLite: projects, prompts, transcripts, sessions,
│   │   │                  #   attachments, refresh tokens, billing, memory
│   │   ├── tools/         # read/write/edit, shell, git, metro, wallet, skills…
│   │   └── ws/            # Terminal, device stream, input sockets
│   ├── template/          # Expo 55 project scaffold provisioned per project
│   └── e2e/               # End-to-end harness
├── frontend/              # Next.js web app (landing, auth, dashboard, IDE, docs)
├── talonmobileapp/        # Expo companion app (build, diff, device, more, menu)
├── infra/                 # Infrastructure (Redroid, Docker)
├── content/               # Marketing/changelog content
└── ROADMAP.md             # Deferred work with reasons
```

## Prerequisites

- Docker CE (native Linux installation, for Redroid)
- [Android SDK](https://developer.android.com/studio) platform-tools (for ADB)
- [Node.js 22](https://nodejs.org/)
- [Go 1.25+](https://go.dev/dl/)
- [Expo](https://expo.dev/) tooling for the companion app (`npx expo`)

## Quick start

### 1. Clone and configure

```bash
git clone git@github.com:Luckyisrael/Talon.git
cd Talon
cp backend/.env.example backend/.env
```

Edit `backend/.env` — the model, your LAN IP (phones and LAN browsers reach
the studio here), and secrets:

```bash
AI_MODEL=nvidia/nemotron-3.5-lightning:free
AI_API_KEY=sk-or-v1-...
PUBLIC_HOST=192.168.1.6
JWT_SECRET=openssl-rand-hex-32-output-here
# Attachments: images to Cloudflare R2 when all four are set, else local disk
# R2_ACCOUNT_ID=... R2_ACCESS_KEY_ID=... R2_SECRET_ACCESS_KEY=... R2_BUCKET=...
```

### 2. Start the stack

```bash
./dev.sh
```

```
▸ backend  :8080
▸ frontend :3000  → API at http://192.168.1.6:8080

Talon is up:
  web     http://localhost:3000
  web LAN http://192.168.1.6:3000
  api     http://localhost:8080
```

`./dev.sh stop` stops both; logs land in `.dev-logs/`.

### 3. Open the IDE

Sign in (GitHub, Google, or Solana wallet), create a project, describe the
app. Approve the plan, watch the build stream, answer the agent when it
pauses with a question. Attach a photo when words fall short, a markdown
file when the spec already exists.

### 4. Companion app

```bash
cd talonmobileapp
cp .env.example .env   # point EXPO_PUBLIC_API_URL at your LAN backend
npx expo run:android   # dev build (OAuth needs a real build, not Expo Go)
```

## Key API surfaces

| Area | Endpoints |
|---|---|
| Auth | `POST /v1/auth/{github,google}/start`, `GET */callback`, `POST /v1/auth/exchange`, `POST /v1/auth/refresh`, `POST /v1/auth/logout`, `GET/DELETE /v1/auth/sessions`, wallet challenge/verify |
| Turns | `POST /v1/prompts`, `/stream`, `/{id}/answer`, `/{id}/resume`, `/{id}/steer`, `/{id}/discard` |
| History | `GET /v1/projects/{id}/conversation` (paged), `/conversation/search`, `GET /v1/projects/{id}/prompts` |
| Attachments | `POST/GET /v1/projects/{id}/attachments[/{attID}]` |
| Devices | `POST/GET/DELETE /v1/sessions`, Metro SSE, `/test-info`, pairing QR, dev-client APK |
| Projects | CRUD plus files, git status/diff/push, checkpoints, memory, export/assets, triage |

## Testing

```bash
cd backend && go build ./... && go test ./...
cd ../frontend && ./node_modules/.bin/tsc --noEmit && ./node_modules/.bin/vitest run
cd ../talonmobileapp && ./node_modules/.bin/tsc --noEmit   # plus eslint; no test runner yet (see ROADMAP.md)
```

CI gates pushes and pull requests (backend vet plus race-detector tests and guards).

## Known limitations

- Billing is a test-mode mock behind a deliberate provider swap.
- Mobile checkout UI is an honest stub until billing lands.
- Plan-mode turns see attachment names and docs; pixels replay in build turns.
- No mobile test runner yet.

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit changes (`git commit -m 'Add amazing feature'`)
4. Push to branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

Private — All rights reserved.

## Acknowledgments

- [Solana Mobile](https://solanamobile.com/) for the Seeker device ecosystem
- [Expo](https://expo.dev/) for React Native tooling
- [Redroid](https://github.com/remote-android/redroid-doc) for Android emulation
- Appllama and Emil Kowalski for MIT-licensed design knowledge adapted into the agent harness (see each skill's `ATTRIBUTION.md`)
