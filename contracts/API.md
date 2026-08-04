# SeekerStudio — API Contracts (v1)

Base URL (dev): `http://localhost:8080`. All endpoints under `/v1` require
`Authorization: Bearer <token>` except `/healthz` and `/v1/auth/*`.

## Health
| Method | Path | Description |
|---|---|---|
| GET | `/healthz` | Liveness probe. Returns `{ "status": "ok", "time": "<RFC3339>" }` |

## Auth (Phase 3)
| Method | Path | Description |
|---|---|---|
| POST | `/v1/auth/register` | Create user. Body: `{ "email", "password" }` → `{ "token", "user" }` |
| POST | `/v1/auth/login` | Login → `{ "token", "user" }` |

## Projects (Phase 3)
| Method | Path | Description |
|---|---|---|
| GET | `/v1/projects` | List projects for the user |
| POST | `/v1/projects` | Create project. Body: `{ "name" }` |
| GET | `/v1/projects/{id}` | Project detail (tree, tier entitlements) |
| DELETE | `/v1/projects/{id}` | Archive project |

## Sessions (Phase 3/4)
| Method | Path | Description |
|---|---|---|
| POST | `/v1/sessions` | Create/start a container session. Body: `{ "project_id" }` |
| GET | `/v1/sessions/{id}` | Session status (state: `starting`/`running`/`paused`/`destroyed`) |
| POST | `/v1/sessions/{id}/pause` | Pause container (idle/over-limit enforcement) |
| POST | `/v1/sessions/{id}/resume` | Resume from warm pool or cold start |
| DELETE | `/v1/sessions/{id}` | Destroy session |
| GET | `/v1/sessions/{id}/stream` | **WS** — H.264 video + touch/keyboard input (ws-scrcpy proxy) |
| GET | `/v1/sessions/{id}/terminal` | **WS** — xterm.js PTY (adb shell / docker exec) |

## AI Orchestrator (Phase 5)
| Method | Path | Description |
|---|---|---|
| POST | `/v1/prompts` | Submit generation prompt. Body: `{ "project_id", "prompt" }` → async job |
| GET | `/v1/prompts/{id}` | Poll job status (`queued`/`running`/`done`/`healing`/`failed`) + diff log |

## Billing & Usage (Phase 6)
| Method | Path | Description |
|---|---|---|
| GET | `/v1/usage` | Metered usage: `{ "device_minutes", "prompts", "limits", "tier" }` |
| POST | `/v1/billing/checkout` | Stripe checkout (free → pro) |
| POST | `/v1/billing/webhook` | Stripe webhook (subscription/overage events) |

## Builds (Phase 7)
| Method | Path | Description |
|---|---|---|
| POST | `/v1/builds` | Kick off APK build. Body: `{ "project_id" }` (PRO only) |
| GET | `/v1/builds/{id}` | Build status + download URL |
| GET | `/v1/builds/{id}/download` | Download signed APK |
| POST | `/v1/builds/{id}/dapp-manifest` | Generate `dapp-store-manifest.json` (PRO only) |

## Session State Machine
```
starting → running ⇄ paused → destroyed
```
- Free tier: idle 5 min → `paused`; 15 min → `destroyed` (state saved).
- Resume: `docker unpause` from warm pool (~2s) or cold boot (~20–30s).
