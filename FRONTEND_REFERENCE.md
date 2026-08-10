# SeekerStudio Frontend — Complete Reference

> **Last updated:** 2026-08-10
> **Stack:** Next.js 16.3.0 · React 19.2.8 · TypeScript 5 · Tailwind CSS v4 · Zustand 5
> **Scope:** Everything built, every pattern used, every decision made, and what still needs work.

---

## Table of Contents

1. [Technology Stack](#1-technology-stack)
2. [Configuration](#2-configuration)
3. [Directory Structure](#3-directory-structure)
4. [Design System](#4-design-system)
5. [App Router Architecture](#5-app-router-architecture)
6. [Library Layer](#6-library-layer)
7. [Component Reference](#7-component-reference)
8. [Real-Time Connections](#8-real-time-connections)
9. [Generated App Architecture](#9-generated-app-architecture)
10. [Known Issues & Technical Debt](#10-known-issues--technical-debt)
11. [Potential Improvements](#11-potential-improvements)

---

## 1. Technology Stack

| Layer | Technology | Version | Purpose |
|---|---|---|---|
| Framework | Next.js (App Router) | 16.3.0 | Server/client rendering, routing |
| UI Library | React | 19.2.8 | Component model |
| Language | TypeScript | ^5 | Type safety |
| Styling | Tailwind CSS v4 | ^4 | Utility-first CSS (CSS-first config) |
| State | Zustand | ^5.0.14 | Global IDE state |
| Editor | Monaco Editor | ^4.7.0 | VS Code-grade code editor |
| Terminal | xterm.js | ^6.0.0 | Terminal emulator |
| Icons | iconsax-reactjs | ^0.0.8 | Icon library |
| Build | Turbopack | built-in | Fast dev/build |
| Fonts | Geist Sans + Geist Mono | next/font | Typography |

### Dependencies Not Used
- No React Query / SWR (data fetching is manual)
- No form library (React Hook Form, Formik)
- No animation library (Framer Motion)
- No testing framework (Jest, Vitest, Playwright)
- No i18n library

---

## 2. Configuration

### `.env.local`
```
NEXT_PUBLIC_API_URL=http://localhost:8080
```
Single env var. Exposed to browser via `NEXT_PUBLIC_` prefix.

### `next.config.ts`
Empty — no custom Next.js configuration.

### `tsconfig.json`
```json
{
  "compilerOptions": {
    "target": "ES2017",
    "module": "esnext",
    "moduleResolution": "bundler",
    "strict": true,
    "jsx": "react-jsx",
    "paths": { "@/*": ["./*"] }
  }
}
```
- Path alias: `@/*` → project root
- Strict mode enabled
- No path aliases beyond `@/*`

### `postcss.config.mjs`
```js
const config = { plugins: { "@tailwindcss/postcss": {} } };
```
Tailwind v4 via PostCSS. No `tailwind.config.ts` — configuration lives in `globals.css`.

### `eslint.config.mjs`
Uses `eslint-config-next/core-web-vitals` + `eslint-config-next/typescript`.

### No Files That Exist Elsewhere
- No `middleware.ts` — no route protection
- No `tailwind.config.ts` — CSS-first config
- No `jest.config` / `vitest.config` — no tests
- No `.prettierrc` — no formatter config
- No `Dockerfile` for frontend

---

## 3. Directory Structure

```
frontend/
├── app/                              # App Router pages
│   ├── layout.tsx                    # Root layout (fonts, metadata)
│   ├── page.tsx                      # Landing page (server component)
│   ├── globals.css                   # Tailwind v4 design tokens
│   ├── login/page.tsx                # Login form
│   ├── register/page.tsx             # Registration form
│   ├── dashboard/page.tsx            # Project dashboard
│   └── project/[id]/page.tsx         # IDE workspace
│
├── lib/                              # Shared libraries
│   ├── api.ts                        # API client + SSE + WebSocket
│   ├── auth.ts                       # JWT token management
│   ├── store.ts                      # Zustand state store
│   └── types.ts                      # TypeScript type definitions
│
├── components/
│   ├── ui/                           # Design system primitives
│   │   ├── PillButton.tsx            # Pill-shaped button
│   │   ├── Spinner.tsx               # Loading spinner
│   │   ├── StatusBadge.tsx           # Status indicator
│   │   ├── TonalCard.tsx             # Material You card
│   │   └── ResizablePanel.tsx        # Draggable panel
│   │
│   ├── layout/                       # App shell
│   │   ├── AppShell.tsx              # TopBar + Sidebar wrapper
│   │   ├── TopBar.tsx                # Top navigation
│   │   └── Sidebar.tsx               # Collapsible sidebar
│   │
│   ├── dashboard/                    # Dashboard
│   │   ├── ProjectCard.tsx           # Project card
│   │   ├── ProjectGrid.tsx           # Responsive grid
│   │   ├── CreateProject.tsx         # Create modal
│   │   └── DeleteProjectModal.tsx    # Delete confirmation
│   │
│   ├── ide/                          # IDE workspace
│   │   ├── IDELayout.tsx             # 3-panel layout
│   │   ├── AIChat.tsx                # AI chat with streaming
│   │   ├── CodeEditor.tsx            # Monaco editor
│   │   ├── FileTree.tsx              # File tree navigator
│   │   ├── FileContextMenu.tsx       # Right-click menu
│   │   ├── StreamRenderer.tsx        # SSE event renderer
│   │   ├── PlanViewer.tsx            # AI plan display
│   │   ├── PhonePreview.tsx          # Redroid device preview
│   │   ├── Terminal.tsx              # xterm.js terminal
│   │   └── Timeline.tsx              # Build activity timeline
│   │
│   └── landing/                      # Landing page
│       ├── Hero.tsx                  # Hero section
│       ├── Features.tsx              # Features grid
│       ├── PhoneMockup.tsx           # Phone mockup
│       └── Footer.tsx                # Footer
│
├── public/                           # Static assets
│   ├── file.svg, globe.svg, next.svg, vercel.svg, window.svg
│
├── .env.local
├── next.config.ts
├── tsconfig.json
├── postcss.config.mjs
├── eslint.config.mjs
├── package.json
└── yarn.lock
```

**Total source files:** 37 (excluding build output, node_modules, public assets)

**Server components:** `app/layout.tsx`, `app/page.tsx`
**Client components:** Everything else (login, register, dashboard, IDE, all components)

---

## 4. Design System

### 4.1 Three-Pillar Architecture (DESIGN_SYSTEM.md)

1. **Material You (Material 3)** — Tonal color blending, pill-shaped controls, dynamic surface elevations
2. **Bento Grid** — Asymmetric modular card layouts with varied aspect ratios
3. **Swiss Minimalism** — Geometric grid, high-contrast typography, mathematical spacing

### 4.2 Color Palette (Dark Theme)

| Token | Hex | Usage |
|---|---|---|
| `--background` | `#0A0A0A` | Screen backdrop |
| `--surface-1` | `#111111` | Lowest surface |
| `--surface-2` | `#1A1A1A` | Card surface |
| `--surface-3` | `#222222` | Elevated surface |
| `--surface-4` | `#282828` | Higher elevation |
| `--surface-5` | `#2A2A2A` | Highest surface |
| `--text-primary` | `#FFFFFF` | Headings, primary text |
| `--text-secondary` | `#9CA3AF` | Body text |
| `--text-muted` | `#6B7280` | Labels, metadata |
| `--accent` | `#F5A623` | Primary action (amber/gold) |
| `--status-success` | `#22C55E` | Success |
| `--status-error` | `#EF4444` | Error |
| `--status-warning` | `#F59E0B` | Warning |
| `--solana-purple` | `#9945FF` | Solana brand |
| `--solana-green` | `#14F195` | Solana brand |

### 4.3 Spacing Scale

4px → 8px → 12px → 16px → 20px → 24px → 32px → 40px → 48px → 64px

### 4.4 Border Radius

| Token | Value | Usage |
|---|---|---|
| `--radius-sm` | 8px | Small elements |
| `--radius-md` | 12px | Cards, buttons |
| `--radius-lg` | 16px | Large cards |
| `--radius-xl` | 20px | Outer containers |
| `--radius-full` | 9999px | Pill shapes |

### 4.5 Typography

- **Primary:** Geist Sans (headings, body)
- **Mono:** Geist Mono (code, metadata)
- **Tracking:** Uppercase labels use `letterSpacing: 1.2px`

### 4.6 Critical Constraints

- **44×44px minimum** pointer targets for all interactive elements
- **Corner nesting:** `Inner Radius = Outer Radius - Container Padding`
- **1px hairline borders** with `rgba(150, 150, 150, 0.18)`
- **Mobile-first** responsive design

---

## 5. App Router Architecture

### 5.1 Root Layout (`app/layout.tsx`)

- **Server component** (no "use client")
- Loads Geist + Geist_Mono via `next/font/google`
- CSS variables: `--font-geist-sans`, `--font-geist-mono`
- Page title: "SeekerStudio — Build Solana Mobile Apps with AI"
- Uses Next.js 16 typed layout props: `LayoutProps<"/">`

### 5.2 Landing Page (`app/page.tsx`)

- **Server component** — full marketing page
- Sections:
  - **Navbar:** Brand, navigation, social links (Twitter, YouTube, Discord, GitHub), Sign in
  - **Hero:** "Build Solana Mobile Apps" heading, prompt input, suggestion chips
  - **Features:** 6-item bento grid (AI Generation, Live Preview, One-Click Deploy, Solana Native, Instant Iteration, Full IDE)
  - **CTA:** "Start building today" + "Get Started Free"
  - **Footer:** 4-column links, copyright 2026

### 5.3 Login Page (`app/login/page.tsx`)

- Client component
- Email/password form
- Flow: `auth.login()` → `setToken()` → redirect to `/dashboard`
- Uses `PillButton` + `Spinner`
- Shows error messages inline

### 5.4 Register Page (`app/register/page.tsx`)

- Client component
- Email/password form (auto-generates username from email prefix)
- Flow: `auth.register()` → `setToken()` → redirect to `/dashboard`

### 5.5 Dashboard Page (`app/dashboard/page.tsx`)

- Client component, wrapped in `AppShell`
- Fetches `projects.list()` + `usage.get()` on mount
- Shows `ProjectGrid`, `CreateProject`, `DeleteProjectModal`
- Displays user email + tier badge
- Redirects to `/login` if not authenticated

### 5.6 IDE Page (`app/project/[id]/page.tsx`)

- Client component, dynamic route `[id]`
- Loads project via `projects.get(id)`
- Renders `IDELayout` (full 3-panel IDE)
- Redirects to `/login` if not authenticated, `/dashboard` on error

---

## 6. Library Layer

### 6.1 `lib/auth.ts` — Token Management

```typescript
getToken(): string | null      // localStorage read
setToken(token: string): void  // localStorage write
clearToken(): void            // localStorage remove
isAuthenticated(): boolean    // token exists check
```

- SSR-safe (`typeof window` check)
- No refresh token mechanism
- No token expiry handling

### 6.2 `lib/types.ts` — Type Definitions

| Type | Fields |
|---|---|
| `User` | id, email, tier (free/pro), created_at |
| `Project` | id, user_id, name, status, plan, created_at |
| `Usage` | prompts_used/limit, device_minutes_used/limit, tier |
| `FileEntry` | path, content, scaffold |
| `FileNode` | name, type (file/folder), children (recursive) |
| `PlanScreen` | name, description |
| `PlanData` | screens, features, navigation, packages |
| `PromptResponse` | id, mode, response (message, files, plan, build_result, commands) |
| `ChatMessage` | id, role, content, mode, plan, files, buildResult, timestamp |
| `AuthResponse` | token, user |

### 6.3 `lib/api.ts` — API Client

**Core:**
- `request<T>(method, path, body?)` — Generic HTTP request with auth header
- Base URL: `NEXT_PUBLIC_API_URL` (defaults to `http://localhost:9191`)
- Throws `APIError` with status code

**Modules:**

| Module | Methods |
|---|---|
| `auth` | `login(email, password)`, `register(email, password, name)` |
| `projects` | `list()`, `get(id)`, `create(name)`, `getFiles(id, includeScaffold)`, `writeFiles(id, files)`, `deleteFile(id, path)`, `delete(id)` |
| `prompts` | `send(projectId, prompt, mode)` |
| `sessions` | `create(projectId)` |
| `usage` | `get()` |

**SSE Streaming:**
- `streamPrompt(projectId, prompt, mode, onEvent, onError)` → `AbortController`
- Event types: `thinking.start/delta/end`, `text.start/delta/end`, `tool.start/end`, `build.result`, `phase`, `tool_call`, `verify`, `guardrail_reject`, `error`, `done`

**WebSocket:**
- `createAuthWebSocket(path)` → WebSocket
- Converts `http://` → `ws://`, appends `?token=<jwt>`

### 6.4 `lib/store.ts` — Zustand Store

**State Shape:**

| Field | Type | Purpose |
|---|---|---|
| `files` | `FileNode[]` | File tree |
| `fileContents` | `Record<string, string>` | Path → content cache |
| `selectedFile` | `string \| null` | Current open file |
| `currentPlan` | `PlanState \| null` | AI-generated plan |
| `sessionId` | `number \| null` | Active device session |
| `previewStatus` | `"idle" \| "connecting" \| "streaming" \| "error"` | Preview state |
| `buildOutput` | `string` | Build log text |
| `terminalOpen` | `boolean` | Terminal visibility |

**Actions:**

| Action | Description |
|---|---|
| `setFilesFromEntries(entries)` | Build file tree from flat entries |
| `addFiles(entries)` | Merge new files into tree |
| `removeFileByPath(path)` | Remove file from tree |
| `selectFile(path)` | Select file for editing |
| `setCurrentPlan(plan)` | Set AI plan |
| `setSessionId(id)` | Set device session |
| `setPreviewStatus(status)` | Update preview state |
| `appendBuildOutput(text)` | Append to build log |
| `setTerminalOpen(open)` | Toggle terminal |

**Helpers:**
- `buildFileTree(entries)` — Flat `FileEntry[]` → nested `FileNode[]`
- `mergeFileTrees(existing, newNodes)` — Deep merge
- `removeNode(nodes, path)` — Remove by path

---

## 7. Component Reference

### 7.1 UI Primitives (`components/ui/`)

| Component | Props | Description |
|---|---|---|
| `PillButton` | variant (primary/secondary/ghost), size (sm/md/lg), disabled, onClick, children | Pill-shaped button, forwardRef |
| `Spinner` | size (sm/md/lg), color (primary/currentColor) | CSS animate-spin |
| `StatusBadge` | variant (free/pro/active/paused/error), size (sm/md) | Status pill |
| `TonalCard` | variant (default/elevated/outlined), padding (none/sm/md/lg), children | Material You tonal surface |
| `ResizablePanel` | direction (horizontal/vertical), defaultSize, minSize, maxSize, handleSide (start/end), children | Draggable resize handle |

### 7.2 Layout (`components/layout/`)

| Component | Description |
|---|---|
| `AppShell` | TopBar + optional Sidebar + `<main>` content |
| `TopBar` | Brand logo, project name, usage counter, StatusBadge, dashboard link |
| `Sidebar` | Collapsible nav (mobile: overlay + backdrop, desktop: static) |

### 7.3 Dashboard (`components/dashboard/`)

| Component | Description |
|---|---|
| `ProjectCard` | Project initial, name, StatusBadge, created date, hover delete button |
| `ProjectGrid` | 1/2/3 column responsive grid, empty state |
| `CreateProject` | Overlay modal, name input, creates via API, redirects |
| `DeleteProjectModal` | Confirmation modal, requires typing project name, red delete button |

### 7.4 IDE (`components/ide/`)

| Component | Lines | Description |
|---|---|---|
| `IDELayout` | ~300 | 3-panel layout: left (file tree + chat), center (editor + terminal), right (phone preview) |
| `AIChat` | ~400 | Plan/build mode toggle, SSE streaming, message list, tool call display |
| `CodeEditor` | ~60 | Monaco wrapper, read-only, VS Dark theme, Geist Mono font |
| `FileTree` | ~200 | Recursive tree, expand/collapse, context menu, inline rename |
| `FileContextMenu` | ~80 | Right-click: new file/folder, rename, delete |
| `StreamRenderer` | ~150 | SSE event renderer: thinking blocks, text, timeline, tool calls |
| `PlanViewer` | ~100 | Plan display: screens, features, navigation, "Approve & Build" |
| `PhonePreview` | ~250 | Canvas JPEG rendering, WebSocket video + input, touch handling |
| `Terminal` | ~200 | xterm.js, 3 tabs (Terminal/Build/Logs), WebSocket PTY |
| `Timeline` | ~100 | Phase/tool events, status icons, auto-scroll |

---

## 8. Real-Time Connections

### 8.1 SSE (Server-Sent Events)

- **Endpoint:** `POST /v1/prompts/stream`
- **Used by:** `AIChat.tsx` via `streamPrompt()`
- **Event flow:**
  ```
  thinking.start → thinking.delta → thinking.end
  text.start → text.delta → text.end
  tool.start → tool.end
  phase
  tool_call
  verify
  build.result
  guardrail_reject
  error
  done
  ```
- **Abort:** Returns `AbortController` for cancellation

### 8.2 WebSocket

- **Auth:** `?token=<jwt>` query parameter
- **Three endpoints:**

| Endpoint | Used By | Purpose |
|---|---|---|
| `/v1/sessions/{id}/stream` | `PhonePreview.tsx` | JPEG video frames |
| `/v1/sessions/{id}/input` | `PhonePreview.tsx` | Touch events (tap, swipe) |
| `/v1/sessions/{id}/terminal` | `Terminal.tsx` | Bidirectional PTY |

### 8.3 Touch Input Protocol

```typescript
// Tap
{ type: "tap", x: number, y: number }

// Swipe
{ type: "swipe", x1: number, y1: number, x2: number, y2: number, duration: number }
```

Device dimensions: 1080×2400

---

## 9. Generated App Architecture

When a user sends a prompt, the AI generates a **React Native + Expo Router** app with:

### 9.1 File Structure (typical)
```
app/
├── _layout.tsx          # Root layout (providers, theme)
└── index.tsx            # Entry screen
src/
├── components/          # Reusable UI components
├── screens/             # Screen components
├── hooks/               # Custom hooks
├── providers/           # Context providers (wallet, theme)
├── solana/              # Solana RPC, connection, utilities
├── theme/               # Design tokens (colors, spacing)
└── utils/               # Helper functions
tsconfig.json
package.json
```

### 9.2 Common Patterns

**Wallet Integration:**
```typescript
import { transact } from '@solana-mobile/mobile-wallet-adapter-protocol-web3js';

const result = await transact(async (mwaWallet) => {
  const auth = await mwaWallet.authorize({ cluster: 'devnet', identity: APP_IDENTITY });
  return auth.accounts[0];
});
```

**Solana RPC:**
```typescript
import { Connection, clusterApiUrl } from '@solana/web3.js';

const connection = new Connection(clusterApiUrl('devnet'), 'confirmed');
const balance = await connection.getBalance(publicKey, 'confirmed');
```

**State Management:**
- React Context + useState/useCallback (not Zustand)
- Custom hooks for business logic (useBalance, useWallet)

### 9.3 Design Tokens (Generated)
```typescript
export const colors = {
  background: '#0B0D12',
  surface: '#151922',
  accent: '#14F195',   // Solana green
  purple: '#9945FF',   // Solana purple
};
```

---

## 10. Known Issues & Technical Debt

### 10.1 Bugs

| Issue | Location | Severity |
|---|---|---|
| **API base URL mismatch** — `.env.local` says 8080, `api.ts` defaults to 9191 | `lib/api.ts:8` | High |
| **No route protection** — client-side auth check only, no middleware | Missing `middleware.ts` | Medium |
| **No token refresh** — JWT expires, user silently logged out | `lib/auth.ts` | Medium |
| **Emitter lifecycle** — build goroutine emitter not closed properly | `backend/internal/api/prompts.go` | High (fixed in backend) |

### 10.2 Unused Code

| Item | Location | Issue |
|---|---|---|
| Landing components | `components/landing/` | Exported but `app/page.tsx` has inline implementation |
| Sidebar | `components/layout/Sidebar.tsx` | Placeholder ("No files yet"), never fully implemented |

### 10.3 Missing Features

| Feature | Status |
|---|---|
| No middleware.ts | No route protection |
| No loading skeletons | No skeleton UI during data fetch |
| No error boundaries | No React error boundaries |
| No file search | No Cmd+P file search in IDE |
| No keyboard shortcuts | No Cmd+S, Cmd+P, etc. |
| No theme toggle | Dark-only, no light mode |
| No tests | Zero test files |
| No i18n | English only |
| No analytics | No tracking |
| No onboarding | No first-run experience |

---

## 11. Potential Improvements

### 11.1 High Priority

1. **Add `middleware.ts`** — Protect `/dashboard` and `/project/*` routes
2. **Fix API base URL** — Change default in `lib/api.ts` from 9191 to 8080
3. **Add error boundaries** — Catch rendering errors gracefully
4. **Add loading skeletons** — Better perceived performance
5. **Implement file search** — Cmd+P to search files in IDE

### 11.2 Medium Priority

6. **Add keyboard shortcuts** — Cmd+S save, Cmd+P search, Cmd+B sidebar toggle
7. **Add token refresh** — Silent refresh before expiry
8. **Implement file search** — Fuzzy search across file names
9. **Add project settings** — Rename, change template, delete
10. **Add usage alerts** — Warn when approaching limits

### 11.3 Low Priority

11. **Dark/light theme toggle** — Follow system preference
12. **Collaborative editing** — Real-time multi-user (future)
13. **Export/download project** — Download as ZIP
14. **Onboarding flow** — First-run tutorial
15. **Virtual scrolling** — For large file trees (100+ files)
16. **Accessibility audit** — Full ARIA compliance
17. **Performance monitoring** — Web Vitals tracking
18. **Internationalization** — i18n support

### 11.4 Architecture Improvements

19. **Server components** — Move more logic to server components for better SSR
20. **React Query / SWR** — Replace manual fetch with caching + deduplication
21. **Zustand middleware** — Add persist, devtools, immer middleware
22. **Component storybook** — Document all components visually
23. **E2E tests** — Playwright tests for critical flows

---

## Appendix A: File Sizes

| File | Lines | Purpose |
|---|---|---|
| `IDELayout.tsx` | ~300 | Main IDE layout |
| `AIChat.tsx` | ~400 | AI chat with streaming |
| `PhonePreview.tsx` | ~250 | Device preview |
| `Terminal.tsx` | ~200 | Terminal emulator |
| `FileTree.tsx` | ~200 | File navigator |
| `StreamRenderer.tsx` | ~150 | SSE renderer |
| `Timeline.tsx` | ~100 | Build timeline |
| `PlanViewer.tsx` | ~100 | Plan display |
| `FileContextMenu.tsx` | ~80 | Context menu |
| `CodeEditor.tsx` | ~60 | Monaco wrapper |

## Appendix B: API Endpoints Used

| Method | Path | Used By |
|---|---|---|
| POST | `/v1/auth/login` | Login page |
| POST | `/v1/auth/register` | Register page |
| GET | `/v1/projects` | Dashboard |
| POST | `/v1/projects` | CreateProject |
| GET | `/v1/projects/:id` | IDE page |
| GET | `/v1/projects/:id/files` | IDE layout |
| PUT | `/v1/projects/:id/files` | File operations |
| DELETE | `/v1/projects/:id/files` | File delete |
| DELETE | `/v1/projects/:id` | DeleteProjectModal |
| POST | `/v1/prompts` | AIChat (non-streaming) |
| POST | `/v1/prompts/stream` | AIChat (streaming) |
| POST | `/v1/sessions` | IDELayout |
| GET | `/v1/usage` | Dashboard |
| WS | `/v1/sessions/:id/stream` | PhonePreview |
| WS | `/v1/sessions/:id/input` | PhonePreview |
| WS | `/v1/sessions/:id/terminal` | Terminal |

## Appendix C: Component Dependency Graph

```
app/page.tsx → Hero, Features, PhoneMockup, Footer
app/login/page.tsx → PillButton, Spinner
app/register/page.tsx → PillButton, Spinner
app/dashboard/page.tsx → AppShell, ProjectGrid, CreateProject, DeleteProjectModal
app/project/[id]/page.tsx → IDELayout

IDELayout → FileTree, FileContextMenu, AIChat, CodeEditor, PlanViewer, PhonePreview, Terminal, Timeline, ResizablePanel
AIChat → StreamRenderer, PillButton, Spinner
StreamRenderer → Timeline
PhonePreview → (canvas, WebSocket)
Terminal → (xterm.js, WebSocket)
FileTree → FileContextMenu
```
