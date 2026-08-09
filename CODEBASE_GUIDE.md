# SeekerStudio — Codebase Guide

## What Is SeekerStudio?

A browser-based "Vibe Coding" IDE for building Solana Mobile apps. Users describe an app in natural language, an AI generates the code, and the app runs instantly in an Android emulator — all in the browser.

---

## High-Level Architecture

```
Browser (Next.js)  ←→  Go Backend  ←→  Redroid (Android in Docker)
      UI/IDE            API/Build         Emulator/Runner
```

- **Frontend** — React/Next.js, Material You design, Monaco editor
- **Backend** — Go server, AI integration, build pipeline, device management
- **Redroid** — Android running in Docker containers (no physical device needed)

---

## Tech Stack

| Layer | Technology |
|-------|------------|
| Frontend | Next.js 16, React 19, TypeScript, Tailwind CSS |
| Backend | Go 1.26, Chi router, Gorilla WebSocket |
| Database | SQLite (via `modernc.org/sqlite`) |
| AI | OpenRouter API (Claude, GPT, etc.) |
| Android | Redroid 14, ADB, scrcpy control |
| Design | Material You, Bento Grid, Swiss Minimalism |

---

## Directory Structure

```
SeekerStudio/
├── backend/                    # Go backend
│   ├── cmd/server/main.go      # Entry point — wires everything together
│   ├── internal/
│   │   ├── ai/                 # AI client (OpenRouter), prompt/response handling
│   │   ├── api/                # HTTP handlers (prompts, sessions, auth, projects, health)
│   │   ├── adb/                # Android Debug Bridge client (install, tap, swipe, screenshot)
│   │   ├── auth/               # JWT authentication (register, login, tokens)
│   │   ├── devclient/          # Dev-client lifecycle (allowlist, rebuild detection, async rebuild)
│   │   ├── events/             # Server-Sent Events for streaming AI responses
│   │   ├── guardrails/         # Input validation, injection detection
│   │   ├── memory/             # Project memory/context management
│   │   ├── orchestrator/       # Build orchestrator (AI loop, file staging, verification)
│   │   ├── redroid/            # Docker container management for Redroid instances
│   │   ├── scrcpy/             # scrcpy control client (binary protocol for input)
│   │   ├── store/              # SQLite database (users, sessions, prompts, projects)
│   │   ├── tools/              # AI-callable tools (read/write files, run commands, build APK, install packages)
│   │   ├── video/              # Video pipeline (JPEG encoder, frame differ)
│   │   └── ws/                 # WebSocket handlers (stream video, input, AI chat)
│   └── template/               # Expo project template (base for all generated apps)
│       ├── dev-client/         # Pre-built dev-client APK + allowlist
│       └── v1.0.0/             # Native modules baked into this version
├── frontend/                   # Next.js frontend
│   ├── app/                    # App Router pages
│   ├── components/             # React components
│   │   └── ide/                # IDE components (ChatPane, PhonePreview, CodeEditor, FileTree)
│   └── lib/                    # API client, auth, hooks
├── contracts/                  # API endpoint definitions
├── infra/                      # Infrastructure (Docker, Redroid setup)
└── docs/                       # Design docs, guides
```

---

## Key Concepts

### 1. Sessions
A session = one user + one Redroid container + one AI chat context.
- Container runs Android 14 with the dev-client pre-installed
- Session stores conversation history, project files, ADB connection info
- Sessions are cleaned up on timeout or explicit delete

### 2. Dev-Client
A pre-built Expo Dev Client APK containing 24 native modules (MWA, crypto, gestures, etc.).
- Built once per template version via `build-dev-client.sh`
- Installed into every container at session start
- Avoids per-session native builds — most changes are JS-only
- If a new native module is needed, async rebuild happens in background

### 3. Native Module Allowlist
A JSON file listing all native modules baked into the dev-client.
- Located at `backend/template/dev-client/v1.0.0/native-modules.json`
- Contains 24 modules (react-native-quick-crypto, expo-haptics, etc.)
- Used for:
  - Rebuild detection (is this package already baked in?)
  - AI context (don't suggest packages already available)
  - Guardrails (warn if adding redundant native deps)

### 4. Rebuild Detection
When the AI adds a new npm package, the system checks:
1. Is it in the allowlist? → No rebuild needed (already baked in)
2. Does it have native code (android/ or ios/ folder)? → Rebuild needed
3. Is it JS-only? → No rebuild needed

If rebuild is needed:
- Current dev-client stays installed (session stays interactive)
- Async rebuild runs in background
- New APK installs when ready
- On failure, falls back to last-known-good APK

### 5. Video Pipeline
Streams the Android screen to the browser:
1. **Raw Screenshot** — `adb screencap` (no PNG flag) → raw RGBA pixels
2. **Frame Differ** — Compares with previous frame, skips identical frames
3. **JPEG Encode** — Fast encoding from raw RGBA (not PNG→JPEG conversion)
4. **WebSocket** — Streams binary JPEG frames to browser at 30 FPS
5. **Backpressure** — Drops oldest frame if queue full (no unbounded memory)

### 6. Input Pipeline
Separate from video (decoupled — input works even if video stalls):
1. **Browser** — Captures touch/click events, sends as JSON over WebSocket
2. **scrcpy Control** — Binary protocol for low-latency input (if available)
3. **ADB Fallback** — `adb shell input tap/swipe/keyevent` (if scrcpy unavailable)
4. **Latency Measured** — Every input dispatch logs timing

### 7. Tools (AI-Callable)
The AI can call these tools during a build session:

| Tool | Purpose |
|------|---------|
| `list_files` | List files in workspace |
| `read_file` | Read file contents |
| `write_file` | Write/create files (staged) |
| `delete_file` | Delete files (staged) |
| `grep_codebase` | Search code |
| `run_command` | Run shell commands |
| `run_typecheck` | Run TypeScript type checking |
| `run_lint` | Run ESLint |
| `install_package` | npm install (checks allowlist, detects native deps) |
| `build_apk` | Build Android APK (guardrails: skips JS-only changes) |

### 8. Staging Area
Changes are staged before applying:
- Files are written to a staging directory first
- After AI confirms, changes are applied to workspace
- Prevents partial/broken states

---

## Request Flow

### AI Chat/Build Request
```
Browser → POST /v1/prompts
  → Auth middleware (JWT)
  → Session lookup
  → System prompt (includes allowlist, project context)
  → AI streaming (OpenRouter)
  → Tool calls (write files, install packages, build)
  → SSE events → Browser
```

### Video Stream
```
Browser ← WebSocket /v1/sessions/{id}/stream
  → adb screencap (raw RGBA)
  → Frame differ (skip identical)
  → JPEG encode
  → Binary frames → Browser
```

### Input
```
Browser → WebSocket /v1/sessions/{id}/input
  → JSON {type: "tap", x: 100, y: 200}
  → scrcpy.Tap(x, y) OR adb shell input tap
  → Device responds
```

---

## Key Files to Know

| File | What it does |
|------|--------------|
| `cmd/server/main.go` | Wires everything, starts server |
| `api/prompts.go` | AI chat/build handler, tool execution loop |
| `api/sessions.go` | Session CRUD, container lifecycle |
| `devclient/allowlist.go` | Native module allowlist, rebuild detection |
| `devclient/manager.go` | Dev-client install, async rebuild |
| `tools/install.go` | Package install with allowlist check |
| `tools/build.go` | APK build with JS-only guardrail |
| `ws/stream.go` | Video WebSocket (raw capture + frame diff + JPEG) |
| `ws/input.go` | Input WebSocket (scrcpy + ADB fallback) |
| `video/jpeg.go` | Pluggable JPEG encoder (pure-Go, TurboEncoder when available) |
| `video/diff.go` | Frame differ (block check + pixel compare) |
| `redroid/manager.go` | Docker container management |
| `scrcpy/control.go` | Persistent scrcpy input client |

---

## Database Schema

```
users        — id, email, password_hash, created_at
projects     — id, user_id, name, created_at
sessions     — id, user_id, project_id, adb_port, container_id, status, created_at, expires_at
prompts      — id, user_id, project_id, session_id, prompt, response, tokens_used, created_at
memories     — id, project_id, content, embedding, created_at
```

---

## How It All Fits Together

1. **User creates project** → Project record in DB
2. **User starts session** → Docker container boots Redroid, dev-client installed
3. **User sends prompt** → AI reads codebase, generates code, calls tools
4. **AI writes files** → Staged in temp dir, applied to workspace
5. **AI installs packages** → npm install, allowlist checked, rebuild if needed
6. **AI builds APK** → expo prebuild + gradle (or skips if JS-only)
7. **User sees app** → Video stream shows Android screen, input works via WebSocket
8. **Session ends** → Container destroyed, cleanup

---

## Deployment

- **Development** — Run `go run ./cmd/server` locally, Docker for Redroid
- **Production** — Go binary + SQLite DB + Docker for Redroid containers
- **Scaling** — Each session gets its own container, horizontally scalable

---

## What Makes This Different

1. **No physical device needed** — Redroid runs Android in Docker
2. **AI-native** — Code is generated by AI, not written by hand
3. **Instant preview** — See your app running in real-time
4. **Smart rebuilds** — Only rebuild when native code changes (rare)
5. **Secure** — Guardrails prevent injection, allowlist prevents bloat
