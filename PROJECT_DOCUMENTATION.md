# SeekerStudio — Complete Project Documentation (A-Z)

## Table of Contents

1. [Project Overview](#1-project-overview)
2. [Environment & Infrastructure](#2-environment--infrastructure)
3. [Backend (Go)](#3-backend-go)
4. [Frontend (Next.js)](#4-frontend-nextjs)
5. [Android / Redroid](#5-android--redroid)
6. [AI Integration](#6-ai-integration)
7. [Agentic Engine](#7-agentic-engine)
8. [Video Streaming & Preview](#8-video-streaming--preview)
9. [Build Pipeline](#9-build-pipeline)
10. [Metro Server](#10-metro-server)
11. [Design System](#11-design-system)
12. [API Contracts](#12-api-contracts)
13. [Git History](#13-git-history)
14. [Current State & Blockers](#14-current-state--blockers)
15. [Next Steps](#15-next-steps)

---

## 1. Project Overview

**SeekerStudio** is a browser-based "Vibe Coding" IDE for the Solana Mobile ecosystem (Seeker device). Users describe their app in natural language, AI plans screens/features, generates React Native/Expo code, builds APK, deploys to Redroid emulator, and streams live preview to the browser.

### Core Architecture

```
┌─────────────────────────────────────────────────────────┐
│                  1. WEB FRONTEND (Next.js)              │
│   • Monaco Code Editor                                  │
│   • AI Prompt Panel & Streaming Logs                    │
│   • HTML5 Canvas (screenshot-based streaming + touch)   │
│   • Web Terminal (xterm.js ↔ adb shell)                 │
│   • Timeline Component (orchestrator events)            │
└──────────────┬──────────────────────┬───────────────────┘
               │ REST / WebSockets    │ Touch / Video Stream
┌──────────────▼──────────────┐  ┌────▼────────────────────┐
│    2. AI ORCHESTRATOR       │  │   3. STREAMING GATEWAY  │
│     (Go API Server)         │  │ (Go proxy → ADB Screenshot)│
│ • AgentRouter integration   │  │ • JPEG Encoding + Frame Diff│
│ • Agentic tool-use loop     │  │ • Touch/Key Input via ADB│
│ • Typecheck + lint gates    │  │ • Backpressure queue     │
│ • Self-healing (max 3)      │  │ • Canvas rendering       │
│ • Vision verification       │  └────────────┬────────────┘
│ • Research + install tools  │               │ Controls Android
└──────────────┬──────────────┘  ┌────────────▼────────────┐
               │ Writes Code     │   4. REDROID CONTAINER   │
┌──────────────▼─────────────────│ (Docker on WSL)         │
│              5. METRO SERVER   │ • Android 14, 1080×2400  │
│   (per-session, auto port)    │ • Mock MWA Wallet (planned)│
│ • adb reverse port forwarding │ • expo-dev-client         │
│ • hot reload on file changes  └─────────────────────────┘
└───────────────────────────────┘
```

### Key Decisions

- **Go backend** (not Node.js) — chi router, modernc.org/sqlite, gorilla/websocket
- **AI backbone**: AgentRouter ($200 credit, Anthropic-compatible API)
- **Redroid** = pure Android device emulator (APK runner + screenshot-based streaming)
- **No smart contracts inside Redroid** — all build tooling runs on host
- **WSL = production mirror** — everything must work on Linux

### Locked Specifications

| Spec | Value |
|---|---|
| Backend port | `9191` |
| Frontend port | `3000` |
| ADB target | `localhost:5555` |
| Device resolution | 1080×2400 @ 420 DPI |
| Expo SDK | 55 |
| React Native | 0.83.6 |
| Node.js | 22.x |
| Go | 1.25.2 |
| AI model | `claude-opus-5` via AgentRouter |
| Freemium | Free (20 prompts, 60 device-min) / Pro $59/mo |

---

## 2. Environment & Infrastructure

### 2.1 Windows Host

- **OS**: Windows 10/11
- **Java**: OpenJDK 17.0.16 at `C:\Program Files\Microsoft\jdk-17.0.16.8-hotspot`
- **Node.js**: v22.23.1 at `C:\Program Files\nodejs\`
- **Android SDK**: `C:\Users\Lucky\AppData\Local\Android\Sdk` (build-tools 35.0.0/36.0.0/36.1.0, platforms android-36)

### 2.2 WSL (SeekerLinux)

- **Distro**: SeekerLinux (Ubuntu 24.04, systemd enabled)
- **Custom Kernel**: 6.6.87.2+ with `binderfs=y`, `memfd_create=y` (no ashmem)
- **Docker**: CE 29.1.3, daemon config: `iptables:false`, `bridge:none`, `ip-forward:true`, `storage-driver:overlay2`
- **Java**: OpenJDK 17.0.19 (installed via apt)
- **Node.js**: v22.23.2 (installed via nodesource)
- **Android SDK**: `/root/android-sdk` (platform-tools, platforms;android-34, build-tools;34.0.0)
- **scrcpy**: 4.1 at `/opt/scrcpy/scrcpy` (headless, for future use)

### 2.3 WSL Networking Fix

**Problem**: WSL had no outbound internet (no default gateway).

**Fix** in `C:\Users\Lucky\.wslconfig`:
```ini
[wsl2]
kernel=C:\\Users\\Lucky\\seekersstudio-kernel\\bzImage
networkingMode=NAT
```

**APT Sources Fix**: Changed from `old-releases.ubuntu.com` (404) to `archive.ubuntu.com`:
```bash
# /etc/apt/sources.list.d/noble.list
deb [check-valid-until=no] http://archive.ubuntu.com/ubuntu/ noble main universe restricted multiverse
deb [check-valid-until=no] http://archive.ubuntu.com/ubuntu/ noble-updates main universe restricted multiverse
deb [check-valid-until=no] http://archive.ubuntu.com/ubuntu/ noble-backports main universe restricted multiverse
deb [check-valid-until=no] http://archive.ubuntu.com/ubuntu/ noble-security main universe restricted multiverse
```

### 2.4 Docker

**Redroid Container**:
```bash
docker run -d --name redroid-seeker-dev \
  --privileged \
  -p 5555:5555 \
  -v redroid-data:/data \
  redroid/redroid:14.0.0_64only-latest \
  androidboot.use_memfd=1
```

**ADB Connection**:
```bash
wsl -d SeekerLinux -e adb connect localhost:5555
```

### 2.5 Infrastructure Scripts

| Script | Purpose |
|---|---|
| `infra/redroid/launch-dev.ps1` | Launch Redroid container |
| `infra/redroid/install-mock-wallet.ps1` | Install Mock MWA Wallet APK |
| `backend/scripts/install-sdk.sh` | Install Android SDK in WSL |
| `backend/scripts/verify-sdk.sh` | Verify SDK installation |

---

## 3. Backend (Go)

### 3.1 Module & Dependencies

```
github.com/seekersstudio/backend
Go 1.25.2
```

| Package | Version | Purpose |
|---|---|---|
| `github.com/go-chi/chi/v5` | v5.2.2 | HTTP router |
| `github.com/golang-jwt/jwt/v5` | v5.2.2 | JWT auth |
| `golang.org/x/crypto` | v0.39.0 | bcrypt password hashing |
| `modernc.org/sqlite` | v1.38.0 | Pure-Go SQLite driver |
| `github.com/gorilla/websocket` | v1.5.3 | WebSocket connections |

### 3.2 Directory Structure

```
backend/
├── cmd/server/main.go              # Entrypoint
├── internal/
│   ├── adb/client.go               # ADB command proxy (Tap, Swipe, Key, Screenshot)
│   ├── ai/
│   │   ├── client.go               # AgentRouter client, system prompts, SSE streaming
│   │   ├── agentic.go              # Tool_use API support (AgenticLoop, ChatWithTools)
│   │   └── vision.go               # VisionCheck for screenshot verification
│   ├── api/
│   │   ├── auth.go                 # Register/Login handlers
│   │   ├── context.go              # Request context (UserIDKey)
│   │   ├── projects.go             # Project CRUD, scaffold system, npm install
│   │   ├── prompts.go              # Prompt handlers, orchestrator integration, SSE
│   │   ├── sessions.go             # Session lifecycle, Metro start/stop
│   │   └── usage.go                # Usage metering
│   ├── auth/service.go             # JWT token generation/validation
│   ├── events/events.go            # SSE event emitter (phase, tool_call, verify, etc.)
│   ├── guardrails/guardrails.go    # ReactNativeOnly, NoSecrets, ExpoVersionPin
│   ├── metro/metro.go              # Metro bundler manager (start/stop/port/adb reverse)
│   ├── orchestrator/
│   │   ├── orchestrator.go         # State machine, RunBuildWithTools, verification gates
│   │   └── session.go              # Session state, phase tracking, heal attempts
│   ├── staging/staging.go          # Staging directory manager (atomic promote)
│   ├── store/store.go              # SQLite store (users, projects, sessions, usage, memory)
│   ├── tools/
│   │   ├── registry.go             # Tool interface, ToolResult, Registry
│   │   ├── list_files.go           # List directory contents
│   │   ├── read_file.go            # Read file content
│   │   ├── write_file.go           # Write to staging directory
│   │   ├── delete_file.go          # Delete from staging
│   │   ├── grep.go                 # Regex search across source files
│   │   ├── run_command.go          # Execute build commands (allowlisted)
│   │   ├── verify.go               # RunTypecheck, RunLint
│   │   ├── install.go              # InstallPackage with allowlist + rebuild detection
│   │   ├── research.go             # WebSearch, GitHubSearch (placeholder)
│   │   ├── runtime.go              # CaptureScreenshot, TailLogcat
│   │   └── build.go                # BuildAPK (expo prebuild + gradle + adb install)
│   └── ws/
│       ├── terminal.go             # ADB shell WebSocket proxy
│       └── stream.go               # Screenshot streaming (JPEG, frame-diff, touch input)
├── template/                       # Expo 55 scaffold
│   ├── package.json
│   ├── app.json
│   ├── tsconfig.json
│   ├── babel.config.js
│   ├── index.js
│   └── app/
│       └── _layout.tsx
├── go.mod
└── go.sum
```

### 3.3 Key Files Detail

#### `cmd/server/main.go`
- Initializes store, auth, AI client, ADB client, orchestrator
- Mounts routes: auth, projects, prompts, sessions, usage
- WebSocket endpoints: `/v1/sessions/{id}/terminal`, `/v1/sessions/{id}/stream`
- CORS middleware, request ID, logging, recoverer, 300s timeout

#### `internal/api/prompts.go`
- **`Create`**: Non-streaming prompt handler
- **`Stream`**: SSE streaming prompt handler with orchestrator integration
- **`buildToolList()`**: Creates all tools for AI (list_files, read_file, write_file, delete_file, grep, run_command, typecheck, lint, install, build_apk)
- **`makeToolExecutor()`**: Wraps tool registry for orchestrator
- **`buildSystemPrompt()`**: Combines base prompt with project file context
- **`isExecutionRequest()`**: Detects 30+ build-mode trigger phrases

#### `internal/orchestrator/orchestrator.go`
- **State machine**: INTAKE→PLAN→CONTEXT_BUILD→RESEARCH→GENERATE→STAGE_WRITE→INSTALL→VERIFY_STATIC→PROMOTE→VERIFY_RUNTIME→DONE
- **`RunBuildWithTools()`**: Agentic loop — sends prompt to AI, executes tool calls, verifies, self-heals (max 3 attempts)
- **Real verification gates**: `tsc --noEmit` and `eslint` via `exec.CommandContext`

#### `internal/tools/build.go`
- **`BuildAPK`**: Auto-detects Java + Android SDK, runs `expo prebuild -p android --no-install`, `gradlew assembleDebug`, `adb install`
- **`findJavaHome()`**: Checks JAVA_HOME env, common Windows locations, `where java`
- **`findAndroidHome()`**: Checks ANDROID_HOME env, LOCALAPPDATA
- **`findAPK()`**: Walks android/ directory for .apk files

### 3.4 API Endpoints

| Method | Path | Description |
|---|---|---|
| GET | `/healthz` | Liveness probe |
| POST | `/v1/auth/register` | Create user |
| POST | `/v1/auth/login` | Login → JWT |
| GET | `/v1/projects` | List projects |
| POST | `/v1/projects` | Create project (copies template, runs npm install) |
| GET | `/v1/projects/{id}` | Project detail |
| GET | `/v1/projects/{id}/files` | List files (scaffold filter) |
| PUT | `/v1/projects/{id}/files` | Write files |
| DELETE | `/v1/projects/{id}/files` | Delete file |
| PUT | `/v1/projects/{id}` | Update plan |
| DELETE | `/v1/projects/{id}` | Delete project |
| POST | `/v1/prompts` | Submit prompt (non-streaming) |
| POST | `/v1/prompts/stream` | Submit prompt (SSE streaming) |
| GET | `/v1/prompts/{id}` | Get prompt status |
| GET | `/v1/projects/{id}/memory` | Get project memory |
| POST | `/v1/sessions` | Create session (starts Metro) |
| GET | `/v1/sessions/{id}` | Session status + metro_port |
| POST | `/v1/sessions/{id}/pause` | Pause session |
| POST | `/v1/sessions/{id}/resume` | Resume session |
| DELETE | `/v1/sessions/{id}` | Destroy session (stops Metro) |
| GET | `/v1/usage` | Get usage stats |
| WS | `/v1/sessions/{id}/terminal` | ADB shell terminal |
| WS | `/v1/sessions/{id}/stream` | Screenshot stream + touch input |

---

## 4. Frontend (Next.js)

### 4.1 Stack

| Tool | Version |
|---|---|
| Next.js | 16.3.0 (Turbopack) |
| React | 19.2.0 |
| TypeScript | 5.9.3 |
| Tailwind CSS | Latest |
| Zustand | State management |
| Monaco Editor | `@monaco-editor/react` |
| xterm.js | `@xterm/xterm` |
| Icons | `iconsax-reactjs` |

### 4.2 Directory Structure

```
frontend/
├── app/
│   ├── layout.tsx                 # Root layout (fonts: Geist Sans + Mono)
│   ├── page.tsx                   # Landing page
│   ├── login/page.tsx             # Login form
│   ├── register/page.tsx          # Register form
│   ├── dashboard/page.tsx         # Project list
│   └── project/[id]/page.tsx      # IDE layout
├── components/
│   ├── ide/
│   │   ├── AIChat.tsx             # Streaming AI chat + timeline events
│   │   ├── CodeEditor.tsx         # Monaco editor (read-only)
│   │   ├── FileTree.tsx           # File browser
│   │   ├── PhonePreview.tsx       # Canvas + touch input
│   │   ├── PlanViewer.tsx         # Plan display
│   │   ├── StreamRenderer.tsx     # Streaming messages + timeline
│   │   ├── Terminal.tsx           # xterm.js terminal
│   │   └── Timeline.tsx           # Orchestrator event timeline
│   └── ui/                        # Shared UI components
├── lib/
│   ├── api.ts                     # API client, WebSocket helper, SSE streaming
│   ├── auth.ts                    # Token management
│   ├── store.ts                   # Zustand store
│   └── types.ts                   # Shared types
├── .env.local                     # NEXT_PUBLIC_API_URL=http://localhost:9191
├── package.json
├── tsconfig.json
└── tailwind.config.ts
```

### 4.3 Key Components

#### `PhonePreview.tsx`
- **Canvas rendering**: `<canvas>` with `createImageBitmap` for JPEG frames
- **Touch handling**: Click → device coordinates (1080×2400) → JSON via WebSocket
- **WebSocket**: `createAuthWebSocket(/v1/sessions/{id}/stream)`

#### `AIChat.tsx`
- **SSE streaming**: `streamPrompt()` with `ReadableStream`
- **Timeline events**: Parses `phase`, `tool_call`, `verify`, `guardrail_reject` events
- **Mode toggle**: Plan vs Build

#### `Timeline.tsx`
- Vertical timeline with status indicators for all event types
- Phase transitions, tool calls, verification results, errors

### 4.4 Design System

Follows `DESIGN_SYSTEM.md` — Material You + Bento Grid + Swiss Minimalism:

- **Background**: `#0A0A0A`
- **Accent**: `#F5A623` (amber/orange)
- **Fonts**: Geist Sans + Geist Mono
- **Icons**: `iconsax-reactjs`
- **44px touch targets**, pill controls, tonal surfaces

---

## 5. Android / Redroid

### 5.1 Container Specs

| Spec | Value |
|---|---|
| Image | `redroid/redroid:14.0.0_64only-latest` |
| Android | 14 |
| Resolution | 1080×2400 |
| DPI | 420 |
| ADB | TCP `localhost:5555` |
| Boot flag | `androidboot.use_memfd=1` |

### 5.2 Device Properties (Seeker-matching)

```
ro.product.model = Seeker
ro.product.brand = Solana
ro.build.display.id = SeekerOS 1.0
ro.build.version.sdk = 34
ro.build.version.release = 14
ro.product.first_api_level = 34
persist.sys.device_name = Seeker
```

### 5.3 Known Limitations

- **No media.swcodec** — Binder service never registers, scrcpy H.264 broken
- **No Mock MWA Wallet** — not yet installed (APK needs JDK 17 + Android SDK to build)
- **No touch input** — was unimplemented, now added via ADB `input tap/swipe/keyevent`

---

## 6. AI Integration

### 6.1 AgentRouter Configuration

```bash
AI_PROVIDER=agentrouter
ANTHROPIC_AUTH_TOKEN=sk-w8339so6GvBgjmhqpp8hrTeltunk1mUH2F8mz1PK2RbxLZMY
ANTHROPIC_BASE_URL=https://agentrouter.org
ANTHROPIC_MODEL=claude-opus-5
```

**Endpoint**: `POST /v1/messages` with `x-api-key` header, `anthropic-version: 2023-06-01`

### 6.2 System Prompts

#### Plan Mode (`systemPromptPlan`)
- STRICTLY no code generation
- Design screens, features, navigation, packages
- Responds in JSON: `{message, plan: {screens, features, navigation, packages}}`
- Detects 30+ execution trigger phrases → refuses in plan mode

#### Build Mode (`systemPromptBuild`)
- React Native / Expo only (no HTML/CSS/JS)
- 10 guardrails: read-before-edit, never delete, preserve patterns, validate paths, type safety, error boundaries, MWA wallet patterns, single responsibility, explain changes
- Pre-installed package list (matches template)
- Tool descriptions: write_file, read_file, list_files, grep, run_command, typecheck, lint, install_package, build_apk

### 6.3 Agentic Loop

```
1. User prompt → system prompt + project context + memory
2. AI returns tool_use blocks → Go executes tools
3. Tool results sent back to AI → more tool calls or final response
4. Verification gates: tsc --noEmit, eslint
5. Self-healing: on error, send error + stack + file to AI (max 3 attempts)
6. Vision check: screenshot → vision model → verify UI matches intent
7. Files staged → promoted atomically after all checks pass
```

### 6.4 Streaming

- Backend sends `stream: true` to AgentRouter
- Parses Anthropic SSE events: `content_block_start`, `content_block_delta`, `message_delta`, `message_stop`
- Frontend consumes via `ReadableStream` + SSE parsing
- Events: `thinking.start/delta/end`, `text.start/delta/end`, `phase`, `tool_call`, `verify`, `guardrail_reject`, `error`, `done`

---

## 7. Agentic Engine

### 7.1 Implemented Packages

| Package | Purpose |
|---|---|
| `internal/events/events.go` | SSE event emitter (typed events) |
| `internal/staging/staging.go` | Staging directory manager (atomic promote) |
| `internal/orchestrator/orchestrator.go` | State machine + verification gates |
| `internal/orchestrator/session.go` | Per-session state tracking |
| `internal/tools/registry.go` | Tool interface + registry |
| `internal/tools/list_files.go` | List directory (skips node_modules, .expo, .staging, .git) |
| `internal/tools/read_file.go` | Read file with path traversal protection |
| `internal/tools/write_file.go` | Write to staging only |
| `internal/tools/delete_file.go` | Delete from staging |
| `internal/tools/grep.go` | Regex search across .ts/.tsx/.js/.jsx/.json/.go |
| `internal/tools/run_command.go` | Execute commands (allowlisted: npm, npx, yarn, tsc, eslint, expo, gradlew) |
| `internal/tools/verify.go` | RunTypecheck (tsc --noEmit), RunLint (eslint) |
| `internal/tools/install.go` | InstallPackage with allowlist + rebuild detection |
| `internal/tools/research.go` | WebSearch, GitHubSearch (placeholder) |
| `internal/tools/runtime.go` | CaptureScreenshot, TailLogcat |
| `internal/tools/build.go` | BuildAPK (expo prebuild + gradle + adb install) |
| `internal/ai/agentic.go` | Anthropic tool_use support, AgenticLoop |
| `internal/ai/vision.go` | VisionCheck (screenshot → vision model) |
| `internal/guardrails/guardrails.go` | ReactNativeOnly, NoSecrets, ExpoVersionPin |

### 7.2 Tool Definitions

```go
type Tool interface {
    Name() string
    Description() string
    InputSchema() json.RawMessage
    Execute(ctx context.Context, args json.RawMessage) (ToolResult, error)
}
```

### 7.3 Event Types

```go
const (
    PhaseEvent       = "phase"           // Phase transition
    ToolCallEvent    = "tool_call"       // Tool execution started
    VerifyEvent      = "verify"          // Verification gate result
    GuardrailEvent   = "guardrail_reject" // Guardrail rejection
    ErrorEvent       = "error"           // Error occurred
    DoneEvent        = "done"            // Build completed
)
```

---

## 8. Video Streaming & Preview

### 8.1 Architecture

```
Browser (canvas)                    Go Backend                      WSL/ADB                    Redroid
      |                                |                               |                           |
      |-- WS connect ----------------->|                               |                           |
      |                                |-- exec-out screencap -p ----->|-------------------------->|
      |                                |<-- raw PNG stream ------------|                           |
      |                                |-- decode PNG → JPEG ---------|                           |
      |                                |-- frame diff (skip if same) -|                           |
      |                                |-- queue with backpressure ---|                           |
      |<-- WS binary (JPEG) -----------|                               |                           |
      |                                |                               |                           |
      |-- WS JSON {type:"tap", -------->|                               |                           |
      |    x: 540, y: 1200}           |-- adb shell input tap x y -->|-------------------------->|
```

### 8.2 Backend (`internal/ws/stream.go`)

- **Capture**: `adb exec-out screencap -p` (1 subprocess vs 3 before)
- **Encoding**: PNG → decode → JPEG (quality 80, ~10x smaller)
- **Frame-diffing**: SHA256 hash, skips unchanged frames
- **Backpressure**: Buffered channel (size 3), drops oldest on full
- **Touch input**: WebSocket read loop parses JSON `{type:"tap",x,y}` → `adb.Tap()`
- **10 FPS** target (was 3 FPS)

### 8.3 Frontend (`PhonePreview.tsx`)

- **Canvas**: `<canvas>` with `createImageBitmap` → `ctx.drawImage()`
- **Touch**: Click handler maps canvas coords → device coords (1080×2400)
- **WebSocket**: JSON messages `{type:"tap",x,y}`, `{type:"swipe",x1,y1,x2,y2,duration}`, `{type:"key",keycode}`

### 8.4 ADB Client (`internal/adb/client.go`)

```go
func (c *Client) Tap(x, y int) error          // adb shell input tap x y
func (c *Client) Swipe(x1, y1, x2, y2, ms int) // adb shell input swipe
func (c *Client) Key(keycode string) error      // adb shell input keyevent
func (c *Client) ScreenshotStream() ([]byte, error) // adb exec-out screencap -p
```

---

## 9. Build Pipeline

### 9.1 `build_apk` Tool

**Flow**:
1. Detect Java + Android SDK
2. `npx expo prebuild -p android --no-install` → generates `android/` directory
3. `gradlew assembleDebug` → builds APK
4. `adb install -r` → installs on Redroid

### 9.2 Package Allowlist

```go
var allowedPackages = map[string]bool{
    "@solana-mobile/mobile-wallet-adapter-protocol":      true,
    "@solana-mobile/mobile-wallet-adapter-protocol-web3js": true,
    "@solana-mobile/seed-vault-sdk":                     true,
    "@solana/web3.js":                                   true,
    "@solana/spl-token":                                 true,
    // ... 20+ packages
}
```

### 9.3 Rebuild Detection

```go
var nativePackages = map[string]bool{
    "@solana-mobile/mobile-wallet-adapter-protocol":      true,
    "react-native-gesture-handler":                      true,
    "react-native-reanimated":                           true,
    // ... checks for android/ or ios/ folder in node_modules
}
```

---

## 10. Metro Server

### 10.1 `internal/metro/metro.go`

- **Port allocation**: `net.Listen("tcp", "localhost:0")` for free port
- **Start**: `npx expo start --port <port> --non-interactive` via WSL
- **Ready detection**: Monitors stdout for "Metro waiting on"
- **ADB reverse**: `adb reverse tcp:<port> tcp:<port>`
- **Lifecycle**: Start on session create, stop on session destroy
- **Timeout**: 60s startup timeout

### 10.2 Session Integration

```go
// In sessions.go Create():
metroServer, err := metro.Start(session.ID, workspaceDir)
// Returns metro_port in response

// In sessions.go Destroy():
metro.Stop(id)
```

---

## 11. Design System

### 11.1 Colors

| Token | Value |
|---|---|
| Background | `#0A0A0A` |
| Surface 1 | `#111111` |
| Surface 2 | `#1A1A1A` |
| Surface 3 | `#222222` |
| Surface 4 | `#2A2A2A` |
| Accent | `#F5A623` |
| Accent Hover | `#E6951A` |
| Text Primary | `#FFFFFF` |
| Text Secondary | `#888888` |
| Success | `#22C55E` |
| Warning | `#F59E0B` |
| Error | `#EF4444` |

### 11.2 Typography

- **Sans**: Geist Sans (Inter fallback)
- **Mono**: Geist Mono (JetBrains Mono fallback)

### 11.3 Layout

- **Bento Grid** system
- **44px** minimum touch targets
- **Pill controls**, tonal surfaces
- **Corner nesting rule**: outer > inner corners

---

## 12. API Contracts

See `contracts/API.md` for full specification.

### Key Endpoints Summary

- Auth: register, login (JWT)
- Projects: CRUD, file management, plan updates
- Prompts: create, stream (SSE), get
- Sessions: create (starts Metro), pause, resume, destroy (stops Metro)
- Usage: metered (prompts + device-minutes)
- WebSocket: terminal (ADB shell), stream (JPEG + touch)

---

## 13. Git History

| Commit | Description |
|---|---|
| `b51e61e` | Phase 0 — Repo scaffold |

*(Limited git history — most work done in single session)*

---

## 14. Current State & Blockers

### 14.1 Completed

- ✅ Phase 0: Repo scaffold
- ✅ Phase 1: Custom kernel + Docker + Redroid
- ✅ Phase 2: Redroid container (1080×2400 @ 420 DPI)
- ✅ Phase 3: Go backend API (all endpoints)
- ✅ Phase 4: AI integration (AgentRouter, streaming, agentic loop)
- ✅ Phase 5: Frontend IDE (all components)
- ✅ Agentic engine (12+ tools, verification, self-healing)
- ✅ Video streaming (JPEG encoding, frame-diffing, touch input)
- ✅ Build pipeline (expo prebuild + gradle + adb install)
- ✅ Metro server (per-session, auto port, adb reverse)
- ✅ WSL internet fixed (networkingMode=NAT)
- ✅ WSL SDK installed (Java 17, Android SDK, Node.js 22)

### 14.2 Blocked / Incomplete

| Issue | Status |
|---|---|
| npm install in WSL | Slow (times out at 120s), needs investigation |
| Mock MWA Wallet | Not installed on Redroid |
| Vision verification | Code exists but not wired into VERIFY_RUNTIME phase |
| Gradle pipeline | Code exists but needs `expo prebuild` to generate android/ first |
| Redroid video encoding | `media.swcodec` broken permanently — screenshot-based is v1 |
| No touch forwarding to Metro | Touch goes to ADB, not Metro's dev tools |

### 14.3 Environment Verified

```
✅ Java 17 (WSL): openjdk 17.0.19
✅ Node.js 22 (WSL): v22.23.2
✅ npm (WSL): 10.9.8
✅ Android SDK (WSL): platforms;android-34, build-tools;34.0.0
✅ ADB: localhost:5555 connected
✅ Docker: CE 29.1.3
✅ Redroid: Android 14, 1080×2400
✅ WSL internet: ping 8.8.8.8 works
✅ WSL DNS: registry.npmjs.org resolves
```

---

## 15. Next Steps

### Immediate (Switching to Linux)

1. **Fresh Ubuntu install** — clean environment, no WSL quirks
2. **Install dependencies**: Java 17, Android SDK, Node.js 22, Go 1.25, Docker CE
3. **Clone repo** and run `npm install` (should work natively on Linux)
4. **Test full pipeline**: create project → AI prompt → code gen → build → deploy → preview

### After Environment is Ready

1. **Wire vision verification** into orchestrator VERIFY_RUNTIME phase
2. **Install Mock MWA Wallet** on Redroid
3. **Test end-to-end**: "build a connect-wallet screen" → appears in preview
4. **Performance testing**: measure actual FPS, optimize if needed
5. **Production deployment**: Docker Compose for backend + frontend

### Future Features

- Stripe billing integration
- Multi-user support with isolation
- APK export/download
- Custom model routing (flash for routine, Opus for complex)
- Frame-diffing optimization
- Touch gesture support (longpress, swipe, pinch)

---

## Appendix A: Environment Variables

### Backend

```bash
PORT=9191
DB_PATH=./seekersstudio.db
WORKSPACE_DIR=./workspaces
TEMPLATE_DIR=./backend/template
AI_PROVIDER=agentrouter
ANTHROPIC_AUTH_TOKEN=sk-w8339so6GvBgjmhqpp8hrTeltunk1mUH2F8mz1PK2RbxLZMY
ANTHROPIC_BASE_URL=https://agentrouter.org
ANTHROPIC_MODEL=claude-opus-5
```

### Frontend

```bash
NEXT_PUBLIC_API_URL=http://localhost:9191
```

## Appendix B: Key File Paths

| File | Purpose |
|---|---|
| `backend/cmd/server/main.go` | Backend entrypoint |
| `backend/internal/api/prompts.go` | AI prompt handlers + system prompts |
| `backend/internal/ai/client.go` | AgentRouter client + system prompts |
| `backend/internal/orchestrator/orchestrator.go` | Agentic state machine |
| `backend/internal/ws/stream.go` | Video streaming |
| `backend/internal/tools/build.go` | APK build pipeline |
| `backend/internal/metro/metro.go` | Metro bundler manager |
| `frontend/components/ide/PhonePreview.tsx` | Canvas + touch |
| `frontend/components/ide/AIChat.tsx` | Streaming AI chat |
| `frontend/lib/api.ts` | API client + WebSocket |
| `frontend/lib/store.ts` | Zustand state |
| `.wslconfig` | WSL2 kernel + NAT mode |
| `AGENTS.md` | Agent instructions |
| `DESIGN_SYSTEM.md` | UI design rules |
| `AGENT_ENGINE_SPEC.md` | Agentic engine specification |
| `IMPLEMENTATION_ROADMAP.md` | Phase-by-phase build plan |
