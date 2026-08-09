# SeekerStudio

A browser-based "Vibe Coding" IDE for the Solana Mobile ecosystem. Users describe their app in natural language, AI plans screens/features, generates React Native/Expo code, builds APK, deploys to an Android emulator, and streams live preview to the browser.

![Status](https://img.shields.io/badge/status-Alpha-orange)
![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go)
![Next.js](https://img.shields.io/badge/Next.js-16.3-black?logo=next.js)
![React Native](https://img.shields.io/badge/React_Native-0.83.6-61DAFB?logo=react)
![Expo](https://img.shields.io/badge/Expo_SDK-55-000020?logo=expo)

---

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                  WEB FRONTEND (Next.js 16)              │
│   Monaco Editor · AI Chat · Canvas Streaming · xterm.js│
└──────────────┬──────────────────────┬───────────────────┘
               │ REST / SSE           │ WebSocket
┌──────────────▼──────────────┐  ┌────▼────────────────────┐
│    AI ORCHESTRATOR (Go)     │  │   STREAMING GATEWAY     │
│  AgentRouter · tool_use     │  │  JPEG · frame-diff      │
│  typecheck/lint gates       │  │  touch input via ADB    │
│  self-healing (max 3)      │  └────────────┬────────────┘
│  vision verification       │               │
└──────────────┬──────────────┘  ┌────────────▼────────────┐
               │ writes code     │   REDROID CONTAINER      │
┌──────────────▼─────────────────│   Android 14 · 1080×2400│
│         METRO SERVER          │   Docker on Linux       │
│  per-session · auto port      └─────────────────────────┘
│  adb reverse · hot reload     │
└───────────────────────────────┘
```

## Features

- **AI-Powered Code Generation** — Describe your app in plain English, AI generates React Native/Expo code
- **Plan Mode** — AI designs screens, features, navigation, and packages before writing code
- **Build Mode** — Full agentic coding with tool-use: write files, install packages, typecheck, lint, build APK
- **Live Preview** — Screenshot-based streaming from Android emulator to browser canvas (10 FPS)
- **Touch Input** — Click on the preview to tap the emulated device screen
- **Terminal Access** — Full ADB shell access via web terminal (xterm.js)
- **Self-Healing** — AI automatically fixes typecheck/lint errors (up to 3 attempts)
- **Vision Verification** — AI screenshots the emulator and verifies the UI matches the user's intent
- **Atomic File Changes** — Files staged in temp directory, only promoted after all checks pass

## Tech Stack

| Layer | Technology |
|---|---|
| Backend | Go 1.25, chi router, modernc.org/sqlite, gorilla/websocket |
| Frontend | Next.js 16, React 19, TypeScript, Tailwind CSS, Zustand |
| Editor | Monaco Editor (`@monaco-editor/react`) |
| AI | AgentRouter (Anthropic-compatible, `claude-opus-5`) |
| Android | Redroid 14 (Docker), ADB, Expo SDK 55 |
| Terminal | xterm.js |

## Project Structure

```
SeekerStudio/
├── backend/                     # Go API server
│   ├── cmd/server/              # Entrypoint
│   ├── internal/
│   │   ├── adb/                 # ADB command proxy (tap, swipe, key, screenshot)
│   │   ├── ai/                  # AgentRouter client, agentic loop, vision
│   │   ├── api/                 # HTTP handlers (auth, projects, prompts, sessions)
│   │   ├── auth/                # JWT authentication
│   │   ├── events/              # SSE event emitter
│   │   ├── guardrails/          # ReactNativeOnly, NoSecrets, ExpoVersionPin
│   │   ├── metro/               # Metro bundler lifecycle manager
│   │   ├── orchestrator/        # State machine, verification gates, self-healing
│   │   ├── staging/             # Atomic file staging
│   │   ├── store/               # SQLite database
│   │   ├── tools/               # 15+ tools (read, write, grep, build, etc.)
│   │   └── ws/                  # WebSocket handlers (terminal, stream)
│   ├── template/                # Expo 55 project scaffold
│   └── scripts/                 # SDK install/verify scripts
├── frontend/                    # Next.js web app
│   ├── app/                     # Pages (landing, auth, dashboard, IDE)
│   ├── components/
│   │   ├── dashboard/           # Project management
│   │   ├── ide/                 # IDE components (chat, editor, preview, terminal)
│   │   └── ui/                  # Shared UI components
│   └── lib/                     # API client, store, types
├── contracts/                   # API endpoint definitions
├── docs/                        # Guides and specs
├── infra/                       # Infrastructure (Redroid, Docker)
└── seeker-studio-example/       # Reference Expo project
```

## Prerequisites

- Docker CE (native Linux installation)
- [Android SDK](https://developer.android.com/studio) (for ADB)
- [JDK 17](https://adoptium.net/)
- [Node.js 22](https://nodejs.org/)
- [Go 1.25+](https://go.dev/dl/)
- [ADB](https://developer.android.com/tools/adb) (platform-tools)

## Quick Start

### 1. Clone the repository

```bash
git clone git@github.com:Luckyisrael/SeekerStudio.git
cd SeekerStudio
```

### 2. Start Redroid (Android emulator)

```bash
docker run -d --name redroid-seeker-dev \
  --privileged \
  -p 5555:5555 \
  -v redroid-data:/data \
  redroid/redroid:14.0.0_64only-latest \
  androidboot.use_memfd=1

adb connect localhost:5555
```

### 3. Install backend dependencies & run

```bash
cd backend
go mod download
go run ./cmd/server
```

Backend starts on `http://localhost:9191`.

### 4. Install frontend dependencies & run

```bash
cd frontend
npm install
npm run dev
```

Frontend starts on `http://localhost:3000`.

### 5. Open the IDE

Navigate to `http://localhost:3000`, create an account, create a project, and start building.

## Environment Variables

### Backend

```bash
# Required
AI_PROVIDER=agentrouter
ANTHROPIC_AUTH_TOKEN=sk-your-api-key
ANTHROPIC_BASE_URL=https://agentrouter.org
ANTHROPIC_MODEL=claude-opus-5

# Optional
PORT=9191                    # Default: 9191
DB_PATH=./seekersstudio.db   # SQLite database path
WORKSPACE_DIR=./workspaces   # Project workspaces
TEMPLATE_DIR=./backend/template
```

### Frontend

```bash
NEXT_PUBLIC_API_URL=http://localhost:9191
```

## API Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/healthz` | Health check |
| `POST` | `/v1/auth/register` | Create account |
| `POST` | `/v1/auth/login` | Login (returns JWT) |
| `POST` | `/v1/projects` | Create project |
| `GET` | `/v1/projects` | List projects |
| `GET` | `/v1/projects/:id` | Project details |
| `GET` | `/v1/projects/:id/files` | List project files |
| `PUT` | `/v1/projects/:id/files` | Write files |
| `DELETE` | `/v1/projects/:id/files` | Delete file |
| `DELETE` | `/v1/projects/:id` | Delete project |
| `POST` | `/v1/prompts/stream` | Submit prompt (SSE streaming) |
| `POST` | `/v1/sessions` | Create session (starts Metro) |
| `DELETE` | `/v1/sessions/:id` | Destroy session |
| `WS` | `/v1/sessions/:id/terminal` | ADB shell terminal |
| `WS` | `/v1/sessions/:id/stream` | Screen stream + touch input |

## How It Works

### 1. User describes app
> "Build a wallet connection screen with a Solana logo and connect button"

### 2. AI plans the app (Plan Mode)
AI designs screens, features, navigation, and required packages.

### 3. AI builds the app (Build Mode)
AI generates code through tool calls:
- `write_file` — Create/modify source files
- `read_file` / `grep` — Inspect existing code
- `install_package` — Add dependencies
- `typecheck` / `lint` — Verify code quality
- `build_apk` — Build and install APK

### 4. Verification gates
- TypeScript type checking (`tsc --noEmit`)
- ESLint linting
- Vision verification (screenshot → AI check)

### 5. Live preview
- Backend screenshots the emulator every ~100ms
- PNG → JPEG encoding with frame-diffing
- Streams to browser canvas via WebSocket
- Click on canvas → tap on device

## Freemium Model

| Feature | Free | Pro ($59/mo) |
|---|---|---|
| AI Prompts | 20 | 500 |
| Device Minutes | 60 | 1,500 |
| Projects | 3 | Unlimited |

## Development

### Run tests

```bash
cd backend
go test ./...
```

### Lint

```bash
cd frontend
npm run lint
```

### Build for production

```bash
cd frontend
npm run build
```

## Known Limitations

- **Redroid video encoding** — Hardware codec (`media.swcodec`) unavailable; uses screenshot-based streaming
- **No Mock MWA Wallet** — Wallet testing requires manual APK installation

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
- [AgentRouter](https://agentrouter.org/) for AI infrastructure
- [Redroid](https://github.com/remote-android/redroid-doc) for Android emulation
- [Expo](https://expo.dev/) for React Native tooling



echo 'OPENROUTER_API_KEY=your-key-here' >> /home/lucky-israel/Desktop/Projects/SeekerStudio/backend/.env