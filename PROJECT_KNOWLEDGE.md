# Talon — Project Knowledge Base

**Compiled:** 2026-10-04 · **HEAD:** `f0d50cb` (350 commits, first commit `b51e61e` 2026-08-04)
**Purpose:** a single, verifiable reference for how the backend and the mobile app actually
work, written for the **Clock In — A Solana Mobile Hackathon** submission.

Everything below is traced to a file path. Where a claim is inferred or unverified it is
marked **(uncertain)**.

---

## 1. What Talon is

> A browser IDE, a companion mobile app, and an AI build agent for the Solana Mobile
> ecosystem. Describe an app in plain language — the agent plans the screens, writes
> React Native/Expo code, verifies it through typecheck, lint, device boot, vision and
> interaction gates, and ships a signed Android APK plus a dApp Store submission bundle.
> — `README.md:5`

Three surfaces over **one** agent:

| Surface | Path | Stack | Role |
|---|---|---|---|
| Build agent (authority) | `backend/` | Go 1.25, chi, SQLite | The agent loop, tools, verify ladder, device fleet, export pipeline, all APIs |
| Web IDE | `frontend/` | Next.js 16.3, React 19, Tailwind v4, Monaco, xterm | Full-power workspace: chat, trace, editor, terminal, device stream, diffs |
| Companion app | `talonmobileapp/` | Expo SDK 55, RN 0.83, React 19 | The agent in your pocket: chat, plan approval, status, diff, device, export |

**Product loop:** describe → plan (read-only, with eyes) → approve → build (agent loop +
tools) → verify ladder → run on Redroid container / Metro / real phone → export signed APK
+ dApp Store bundle.

**Honest framing:** it is an *AI build agent for Solana Mobile apps*, not a dApp itself.
Its Solana surface is real (wallet auth, on-chain SKR payment verification, MWA template,
dApp Store bundle) — see §6.

---

## 2. Repo map and measured statistics

```
Talon/
├── backend/            Go API server + agent harness
│   ├── cmd/server/     main.go (838 lines) — boot + all 91 routes
│   ├── cmd/talon-headless/  real composition under a scripted provider → eval scorecard
│   ├── internal/       45 top-level packages + 17 harness/* subpackages
│   └── template/       Expo scaffold provisioned into every new project
├── frontend/           Next.js web IDE + marketing
├── talonmobileapp/     Expo companion app (Android only)
├── infra/redroid/      Android 14 Docker image + mock MWA wallet installer
├── e2e/                live end-to-end harness (shell + python driver + scorecards)
└── *.md                PLAN_*, DESIGN_*, IMPLEMENTATION_NOTES, ENGINEERING_STANDARDS
```

### Measured 2026-10-04

| Metric | Value | How measured |
|---|---|---|
| Git commits | **350** | `git rev-list --count HEAD` |
| Backend Go LOC | **106,156** | `find backend -name '*.go' \| xargs wc -l` |
| Frontend TS/TSX LOC | **18,924** | `frontend/{app,components,lib}` |
| Mobile TS/TSX LOC | **12,454** | `talonmobileapp/{app,components,lib,hooks,features,stores,constants}` |
| **Combined LOC** | **~137,500** | three surfaces |
| Go packages | **66** | 4 `cmd/` + 62 `internal/` |
| HTTP/WS routes | **91** | 2 health + 89 `/v1`, all in `cmd/server/main.go` |
| SQLite tables | **36** | `store/store.go` `CREATE TABLE IF NOT EXISTS` |
| Agent tools | **61 distinct names** + `mcp__*` / `plugin__*` | `grep` over `internal/tools/` |
| Embedded skills | **15** | `internal/knowledge/content/skills/*/SKILL.md` |
| Go test files | **230** | `grep -l 'func Test'` |
| Go test functions | **912** | `grep -h 'func Test'` |
| Mobile test files | **26** (jest-expo) | |
| Frontend test files | **21** (vitest) | |
| Golden eval tasks | **6** | `internal/evals/golden/tasks` |
| `TALONSTUDIO_*` flags | **28** | documented in `.env.example`; `make docs` asserts each is referenced *and* documented |

### Largest files (the load-bearing ones)

| File | Lines |
|---|---|
| `backend/internal/orchestrator/orchestrator.go` | 4,275 |
| `frontend/components/ide/AIChat.tsx` | 2,939 |
| `backend/internal/api/prompts.go` | 2,888 |
| `backend/internal/store/store.go` | 2,545 |
| `frontend/lib/api.ts` | 1,279 |
| `backend/internal/ai/client.go` | 1,861 |
| `talonmobileapp/lib/api.ts` | 1,103 |
| `talonmobileapp/app/(tabs)/build.tsx` | 1,052 |
| `backend/internal/api/buildsession.go` | 1,038 |
| `talonmobileapp/hooks/index.ts` | 946 |

---

## 3. Backend architecture

### 3.1 Boot — `backend/cmd/server/main.go` (838 lines)

godotenv → `store.New(sqlite)` → `ai.New` (**fatal if unconfigured**) → `knowledge.Load`
→ `orchestrator.New` → composer/settings → skill catalog → instructions chain → spill
store → redroid container manager + warm pool → OAuth + notify handlers → SKR verifier +
mint probe (`ProbeMint`, warns if `SKR_MINT` is not owned by the SPL token program,
`:374`) → git remote → chi router → 91 routes → server with 60s graceful shutdown.

Other binaries: `cmd/talon-headless` (headless profile + eval scorecard), `cmd/e2e`,
`cmd/breaktest`.

### 3.2 Route surface (91)

| Area | Representative routes |
|---|---|
| Health | `GET /healthz`, `GET /readyz` |
| Auth | `POST /v1/auth/{exchange,refresh,logout,logout/all}`, `GET /v1/auth/{provider}/{start,callback}`, `GET/DELETE /v1/auth/sessions[/{id}]` |
| **Wallet auth** | `POST /v1/auth/wallet/{challenge,verify}`, `POST /v1/me/wallet/{challenge,verify}` |
| Projects | CRUD `/v1/projects[/{id}]`, files, memory, audit, conversation (+`/search`), verify-state, prompts, feedback, attachments, checkpoints (restore), diff |
| Turns | `POST /v1/prompts`, `/prompts/stream`, `/{id}/resume`, `/{id}/answer`, `/{id}/steer`, `/{id}/discard`, `GET /v1/prompts/{id}` |
| Device / Metro | `GET /v1/projects/{id}/metro` (SSE), `/test-info`, `/dev-client.apk` |
| Sessions | `POST/GET/DELETE /v1/sessions[/{id}]`, `/screenshot`, `/pause`, `/resume` |
| WebSockets | `/v1/sessions/{id}/{terminal,stream,input}` |
| Export | `POST /v1/projects/{id}/export`, `/assets`, `GET /v1/exports/{id}`, `/download`, `/bundle` |
| **SKR** | `POST /v1/skr/quote`, `POST /v1/skr/verify` |
| Billing | `/v1/billing/{upgrade,cancel,test/complete,webhook}` |
| Git | `/v1/git/status`, `/v1/git/github/{connect,callback}`, per-project `remote/status/branches/log/push` |
| Misc | `/v1/usage`, `/v1/notifications`, `/v1/users/push-token`, `/v1/metrics`, `/v1/errors`, `/v1/me`, `/v1/triage` |

### 3.3 Persistence — SQLite

- Driver **`modernc.org/sqlite`** (pure Go, **no cgo**) at `DB_PATH` (default `./talon.db`).
- Schema + all queries: `backend/internal/store/store.go` (2,545 lines) plus 14 sibling
  files (`conversation.go`, `sessionlog.go`, `codeindex.go`, `vectorsearch.go`, `git.go`,
  `billing.go`, `release.go`, …).
- **36 tables** including `users`, `projects`, `conversations`, `prompts`, `sessions`,
  `session_events`, `checkpoints`, `turn_approvals`, `build_receipts`, `plan_task_status`,
  `project_memory`, `architecture_decisions`, `code_files`/`code_chunks`/`code_symbols`/
  `code_edges` (the index), `project_keystores`, `export_jobs`, `export_credits`,
  `project_verify_state`, `wallet_challenges`, `audit_log`.

### 3.4 Streaming

- **SSE is the primary agent transport** (`text/event-stream` + heartbeats).
  Event vocabulary: `backend/internal/events/events.go` — `phase`, `tool_call`,
  `tool_input.delta`, `verify`, `guardrail_reject`, `error`, `done`, `text`, `thinking`,
  `rebuild`, `task_update`, `usage`, `command_output`, `question`, `decision`,
  `skills_group`.
- Session-log event kinds: `backend/internal/harness/events/events.go`.
- Metro log SSE: `backend/internal/metro/events.go` → `/v1/projects/{id}/metro`.
- **WebSockets** (`gorilla/websocket`) for terminal / video stream / input —
  `backend/internal/ws/{auth,terminal,stream,input}.go`; auth is a query-string token with
  an origin allowlist (this is the *only* URL-token pattern in the repo, because
  `EventSource`/`WebSocket` cannot set headers).

### 3.5 Auth

- JWT access token + **rotating refresh tokens with reuse-kill** (`store/refresh_tokens.go`),
  per-device session list and revoke, `logout/all`.
- GitHub + Google OAuth (`internal/auth/oauth.go`, `api/auth_oauth.go`).
- **Solana wallet sign-in** — `backend/internal/api/walletauth.go` (301+ lines): server
  issues a nonce, client signs with the wallet via MWA, server verifies with
  `crypto/ed25519.Verify` and a minimal in-repo base58 decoder, identity stored as
  `provider='solana'` via `store.UpsertOAuthUser("solana", …)`.
  **The server never holds a private key** — it only verifies.
- CORS allowlist, `AuthRateLimiter`/`PromptRateLimiter` (`api/ratelimit.go`), secrets in
  `internal/secrets/cipher.go`.

---

## 4. The agent harness (the crown jewel)

### 4.1 The loop — `orchestrator/orchestrator.go`

`RunAgentLoop` (lines 1144–1583) is a round-based loop:

- **Round loop with a steer lane** — a user message typed mid-build lands in the next
  model request (`harness/agent/agent.go`: "a live registry of running agents, each with
  a steer lane").
- **Prompt rebuild on fingerprint change** (plan / checklist / compaction / skills).
- **Context compaction ~200K tokens** with a model checkpoint (`harness/compaction`).
- **Gate ticker** during long rounds; **budget stop**; **hook stop**;
  **latched fallback model/provider** when a provider dies mid-turn.
- **`maxVerifyAttempts = 6`** — the verify/repair ladder may retry six times.
- Pauses: `ask_user` (one question), resume/answer/steer/discard, checkpoints + undo.

### 4.2 Plan phase — "plan with eyes"

`backend/internal/api/planloop.go` — `maxPlanRounds = 8`, a **read-only tool registry**
(mutation is impossible by tool *absence*, not by promise), project map + AGENTS.md chain
injected through `harness/promptsec` (sections fingerprinted for prompt-cache discipline),
a cheap-model survey call when the map fingerprint is stale. Behind `TALONSTUDIO_PLAN_TOOLS=v1`.
Approved plans are written to the workspace as `.talon/plan.md` (durable, editable,
checkpointed) — `internal/planfile`.

### 4.3 The verify ladder

`verifyGates()` order, in `orchestrator.go`:

```
lsp_diagnostics (only if TALONSTUDIO_LSP) → run_typecheck → run_lint → run_bundle
→ run_gradle → run_boot → run_vision → run_interact
```

- **Policy matrix** (`orchestrator/verify_policy.go`): each stage is
  `required | best-effort | off`. Defaults: typecheck/lint/bundle/gradle **required**;
  boot/vision/interact **required iff a device is attached**, else best-effort; lsp
  best-effort. Overridable per project in `.talon/config.json` `verify_gates`.
- Resolution: project `verify_policy` → env `TALONSTUDIO_VERIFY_POLICY` → `strict`;
  unknown value → strict + logged (a typo in a safety flag must not mean permissive).
- Outcome gains `VerifiedBestEffort` + `VerifyPolicy`; **bare `Verified` is unreachable**
  without policy evaluation.
- **Shipping rule** `orchestrator.VerifyReleaseRefusal`: export/release refuses on a
  recorded `refused` status; `override:true` ships and writes one `verify_override` audit
  row per attempt.
- Durable shadow: `store/project_verify_state` (one row per project, newest sweep wins).
- Failure index: `orchestrator/failurefeed.go` feeds failed verdicts into recorded
  patterns so the model gets a targeted nudge instead of re-discovering the bug.

### 4.4 Tools — 61 distinct + 2 dynamic patterns

`backend/internal/tools/` (author's grouping in `tools/README.md`).

**Read:** `list_files`, `read_file`, `grep_codebase`, `search_files`, `find_files`,
`explore`, `project_map`, `inspect_package`, `codebase_search`, `codebase_graph`,
`lsp_references`, `lsp_definition`, `lsp_symbol`, `lsp_diagnostics`, `read_skill`,
`read_plugin_file`.

**Write:** `write_file`, `edit_file`, `multi_edit`, `delete_file`, `try_edit` (edit then
typecheck, auto-rollback), `keep_edit`, `lsp_organize_imports`, `write_adr`.

**Shell / verify:** `run_command` (allow-listed: npm/yarn/npx/tsc/eslint/expo/node/git),
`run_shell` (tiered approval + secret-file denylist), `run_typecheck`, `run_lint`,
`run_prettier`, `run_checks`, `run_bundle`, `run_gradle`, `run_boot`, `run_vision`,
`run_interact`, `run_gate`, `run_e2e` (Maestro flows), `capture_screenshot`,
`tail_logcat`, `metro_status`, `build_apk`, **`test_wallet_roundtrip`**, `install_package`.

**Git:** `git_status`, `git_diff`, `git_log`, `git_commit`, `git_branch`.

**Delegation / memory / knowledge:** `delegate`, `delegate_fork`, `list_children`,
`run_workflow`, `mark_task_done`, `ask_user`, `write_memory`, `read_memory`,
`record_failure_pattern`, `web_search`, `github_search`, `search_plugins`.

**Dynamic:** `mcp__<server>__<tool>` (`internal/mcp`, `TALONSTUDIO_MCP`),
`plugin__<name>__<tool>` (pinned plugin packs).

### 4.5 Safety layers (why the agent is trustworthy)

- **Guardrails** (`internal/guardrails/guardrails.go`): read-before-write, install
  denylist, `node-modules-survey`, path sandboxing, secret-file denylist.
- **Approval seam** (`harness/approval`, `harness/sandbox/escalation.go`): tier-2 shell,
  keystore, export and git-push escalate through one `approval.Request` → one audit event →
  `turn_approvals` as the binding record.
- **Hooks** (`hooks/hooks.go`, `TALONSTUDIO_HOOKS`): deterministic lifecycle callbacks —
  a hook can *block* an action rather than merely ask.
- **Sandbox posture** (`harness/sandbox`): tier classification + workspace cwd.
- **Budgets**: `maxRounds`, quota, per-turn cost (`ROUTING_PRICING_JSON`), child-agent
  spend cap + wall clock (`harness/subagent`).
- **Worktree isolation** for mutating children (`harness/worktree`, `TALONSTUDIO_CHILD_ISOLATION`).

### 4.6 Senses (retrieval, index, LSP)

- **`internal/index`** — pure-Go parser → content-addressed chunks/symbols/edges stored in
  `code_files`/`code_chunks`/`code_symbols`/`code_edges` with FTS5/bm25 lexical layer.
  Reconcile-on-touch: only changed paths re-parse (state hash-equals a fresh full build).
  Behind `TALONSTUDIO_INDEX`.
- **`internal/embed`** — `Provider` interface; API default speaks OpenAI-compatible
  `/v1/embeddings`; `FromEnv()` returns **nil when unconfigured** and every caller treats
  nil as "the vector layer does not exist". Vectors cached keyed by chunk content hash.
- **`internal/recall`** — `Fuse()` = reciprocal rank fusion (K=60) over ranked ID lists;
  used by both `read_memory` and `codebase_search` (FTS ∪ cosine).
- **`internal/lsp`** — typescript-language-server / gopls per workspace behind
  `TALONSTUDIO_LSP`; device-reaper pattern for process lifetimes (LRU cap 4, idle reaper,
  restart-on-crash); unavailable → structured `LSP_UNAVAILABLE` → `SkippedGates` → ladder
  continues (**fail-open on availability, fail-closed on truth**).
- **Project map as a view** over the index, never a one-shot snapshot.

### 4.7 Knowledge: 15 embedded skills

`app-linking`, `dapp-store`, `device-session`, `expo-router-nav`, `expo-ui`,
`failure-patterns`, `mobile-craft`, `mobile-defaults`, `mobile-motion`, `motion-review`,
`rn-best-practices`, **`seed-vault`**, `solana-dev`, `talon-mobile`, **`wallet-mwa`**.

`solana-dev` is pinned to upstream `solana-foundation/solana-dev-skill`
(`talonmobileapp/skills-lock.json`). `knowledge/agent.go` *suggests* skills from code — an
`@solana/kit` import pulls `solana-dev`, a wallet prompt suggests `wallet-mwa`
(`knowledge/agent_test.go`).

### 4.8 AI provider layer — `internal/ai`

- `client.go` (1,861 lines): SSE streaming sinks, retry/backoff, rate-limit backoff,
  auth retry, quota, pacer, ledger, health (`readyz`), prompt diet/contract.
- **Routing** `ai/routing.go`: tiers `strong|medium|weak`; `ClassifyTurn` maps a turn to
  `plan` / `build` / `small_edit` / `heal`; `ROUTING_POLICY_JSON`, `ROUTING_PRICING_JSON`;
  fallback model and separate vision model.
- **Multi-provider router** `ai/router.go` (W7) — **inert unless `TALONSTUDIO_ROUTER`**:
  N provider configs in a hot-reloaded `settings.json` beside the DB, health registry,
  **cross-provider failover**, circuit breaker with half-open probes, `VisionModel`,
  `MaxTurnCostUSD`.
- Config: `AI_PROTOCOL` (`openai`|`anthropic`), `AI_BASE_URL` (default OpenRouter),
  `AI_API_KEY`, `AI_MODEL`, `AI_MODEL_CHEAP`, `AI_MODEL_TIER`.
- **CI is deliberately secret-free** — no AI keys in `.github/workflows/ci.yml`; the
  headless job uses a *scripted* provider.

---

## 5. Solana integration (the hackathon-critical surface)

**Bottom line:** real but deliberately **keyless**. The backend never signs anything
on-chain and holds no keys.

### 5.1 On-chain SKR payment verification (Go) — `backend/internal/skr/`

The only chain access in the backend, and it is **raw JSON-RPC over `net/http`** — no SDK,
no dependency (`go.mod` has zero Solana modules).

- `config.go` (94 lines): `SKR_RPC_URL`, `SKR_MINT`, `SKR_TREASURY`, `SKR_PRICE_AMOUNT`,
  `SKR_PRICE_CREDITS`, `SKR_DECIMALS`; `TokenProgram =
  "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"`; base58 signature shape-checked
  **before** any RPC call.
- `verify.go` (306 lines): `getTransaction` (`encoding: jsonParsed`, `commitment:
  confirmed`) + `getAccountInfo` (mint probe — SPL Mint layout, decimals byte at offset
  44). The verdict is derived only from `meta.err`, `pre/postTokenBalances` mint+owner
  matching the configured mint/treasury, and an **exact raw-amount delta = price ×
  10^decimals, where decimals come from the chain, never from env config**.
  Three-way: `verified` / `pending` / `rejected` with reasons `tx_failed`, `incomplete`,
  `wrong_mint`, `wrong_recipient`, `wrong_amount`, `pending`. Timeout enforced twice,
  8 MiB response cap, **fail-closed**.
- `api/skr.go` (144 lines): `POST /v1/skr/quote` — the **server** states amount, pack size,
  mint and treasury (a price the client names is a price the client chooses);
  `POST /v1/skr/verify` → 400 junk / 200 verified|pending / 422 rejected / 502 RPC down.
  Credits granted via `store.GrantCredits(userID, credits, "skr", signature)` —
  `tx_signature UNIQUE` makes **replay a no-op**.

### 5.2 Wallet authentication (Go) — `backend/internal/api/walletauth.go`

ed25519 verify-only over a server-issued nonce; own minimal base58 decoder; identity
upserted as `provider='solana'`. Tests cover the round-trip, forged signatures, base58
round-trip, and one-wallet-per-account replacement.

### 5.3 Client-side SPL payment (mobile) — `talonmobileapp/lib/skr-transfer.ts`

Hand-built SPL instructions with `@solana/kit` ("no dependency added", patterned on Solana
Mobile's own skr-staking sample):

- `findAssociatedTokenAddress(owner, mint)` — PDA over seeds `[owner, tokenProgram, mint]`
- `createAssociatedTokenAccountIdempotentInstruction` (discriminant 1)
- `transferCheckedInstruction` (discriminant 12), layout `[u8][u64 LE]` over
  `source, mint, owner, destination`
- `packAmountInRawUnits(...)` — decimals fetched from `getTokenSupply` on the quoted mint,
  never a compiled constant
- Mint and treasury are **absent from the binary** — they come from `POST /v1/skr/quote`.

Driven from `components/build/ExportPanel.tsx`: quote → (spare ≥ 1 skips) → pay via MWA
`useMobileWallet().sendTransactions` → keep signature → verify (retry `pending`, **never
re-send**) → start export → poll.

### 5.4 Solana Mobile development surface

- **Generated-app template** (`backend/template/`): `@solana/kit ^6.1.0`,
  `@solana-program/memo`, `@solana-program/system`, `@wallet-ui/react-native-kit`;
  `constants/app-config.ts` preconfigured for devnet/testnet. `TEMPLATE.md` **mandates**
  `useMobileWallet()` + `sendTransactions()` with Kit program clients and **forbids**
  `@solana/web3.js` and `...-protocol-web3js`.
- **Rules** (`knowledge/content/rules/core.md`): MWA-only wallet stack.
- **Skills**: `solana-dev`, `wallet-mwa`, `seed-vault`, `dapp-store`.
- **Device**: `infra/redroid/install-mock-wallet.sh` installs Solana Mobile's
  `mock-mwa-wallet` (`com.solana.mobile.mockwallet`) on the emulator.
- **Test tool**: `test_wallet_roundtrip` drives `com.solana.mobilewalletadapter.fakedapp`
  ↔ `fakewallet` and greps logcat for `MWAProtocol:*`, `MobileWalletAdapter:*`,
  `SolanaMobileWalletAdapter:*`.
- **Guardrail**: rejects `@solana/wallet-adapter-react` — *"cannot run on the device — use
  MWA transact() instead"* (`api/guardrail_stream_test.go:19`).
- **Eval grading**: `evals.go` `UsesMWA` checks the marker
  `@solana-mobile/mobile-wallet-adapter-protocol` and **disqualifies `@phantom/`**.
- **Release**: `internal/release/manifest.go` targets
  `https://publish.solanamobile.com`, notes dApp Store cost ("costs real SOL: keep ~0.2
  SOL"), `@solana-mobile/dapp-store-cli` steps — *"Talon still never touches a publisher
  keypair"*.
- **CI guards**: `Kit guard` (no `@solana/web3.js` in `backend/template/**` or skills) and
  `Rename guard` (demowallet/demodapp are Solana-upstream-owned names).

### 5.5 Explicit negatives (say these honestly)

- No keypair generation, signing, or custody anywhere in Go — ed25519 is **verify only**.
- No Solana crate/SDK in `go.mod`.
- No Anchor/program code written by the backend.
- The web frontend has **zero** web3 code — Solana there is copy, CSS tokens and one
  functional touch (`IconRail.tsx` reads `wallet_linked` from `/v1/me` and nags you to
  open the mobile app).

---

## 6. The companion mobile app — `talonmobileapp/`

### 6.1 Identity

`package.json`: name `TalonMobileApp`, displayName **Talon**, description *"Talon
companion app — build Solana apps by talking"*, keywords
`["expo", "mobile-wallet-adapter", "react-native", "solana-kit"]`.
`app.json`: slug `talon`, scheme **`talon`**, Android package **`com.talon.app`**,
portrait, dark UI, permission `REQUEST_INSTALL_PACKAGES`, plugins `expo-font`,
`expo-router`, `expo-splash-screen`, `expo-web-browser`, `expo-secure-store`,
`onesignal-expo-plugin` (production), `expo-sharing`; experiments `reactCompiler: true`,
`typedRoutes: true`.

*(Note: `talonmobileapp/README.md` is a 3-line upstream stub — do not cite it.)*

### 6.2 Stack

| Area | Choice |
|---|---|
| Expo / RN / React | SDK **55**, RN **0.83.10**, React **19.2.0** |
| Navigation | **expo-router ~55.0.7** (typed routes, scheme `talon`) — but the shell is a custom `TopNav` + animated half-width `Drawer`, **not** a bottom tab bar |
| Server state | TanStack Query 5.90.7 (`hooks/index.ts`, 946 lines) |
| Client state | Zustand 5.0.15 (`stores/app.ts`) |
| Secrets | **`expo-secure-store`** (Keystore-backed) for JWT + refresh; AsyncStorage for non-secret UI state |
| Streaming | hand-rolled SSE reader over **`expo/fetch`** so headers settle first |
| Motion | react-native-reanimated 4.2.1, gesture-handler, worklets |
| Push | `react-native-onesignal` + `onesignal-expo-plugin` |
| Crypto | `react-native-quick-crypto` (installed in `polyfill.js`), `react-native-get-random-values` |
| Solana | `@solana/kit ^6.1.0`, `@solana-program/memo`, `@wallet-ui/react-native-kit ^4.0.1` → pulls `@solana-mobile/mobile-wallet-adapter-protocol{,-kit}` |
| Tests | jest-expo 55 + Testing Library; `npm run ci` = tsc + lint + format + android build |
| Deps | 56 runtime + 11 dev = 67 |

### 6.3 Screen map (22 route files; repo claims "25 screens")

| Route | What it does |
|---|---|
| `app/_layout.tsx` | Root stack, `initPush()`, single `auth:expired` listener → `router.replace('/auth')` |
| `app/index.tsx` | Splash: first launch → `/onboarding/welcome`, token → `/(tabs)/build`, else `/auth` |
| `app/onboarding/{welcome,features,connect}` | Promise → features → **MWA wallet handshake** ("Keys never leave the wallet") |
| `app/auth/index.tsx` | OAuth via `expo-web-browser.openAuthSessionAsync` + `makeRedirectUri({scheme:'talon', path:'auth/callback'})`; server-driven provider list; guest mode; Expo Go dead-end warning |
| `app/auth/callback.tsx` | Receives `talon://auth/callback?code=…`, exchanges single-use code for JWT, **consumes the pending wallet proof** |
| **`app/(tabs)/build.tsx` (1,052 lines)** | The agent chat: PLAN/BUILD switch, sub-tabs **Chat / Files / Export**, live SSE turn, tool headlines (`TOOL_LABELS`), `ThinkingRow`, `TraceRow`, `PlanCard` + approval, `QuestionCard`, `VerifyBadge`, resume/discard/steer/queue, attachments, offline snapshot, `FileBrowser`, `ExportPanel` |
| `app/(tabs)/diff.tsx` | Per-file working-tree diff vs template baseline, +/− counts, expandable hunks |
| `app/(tabs)/device.tsx` | Metro-first device tab: `useMetro` SSE, session relay, deep link, **in-app APK download/install**, QR pairing, recent Metro events |
| `app/(tabs)/more.tsx` | Account hub: usage in real cents, tier, billing triage, sign-out |
| `app/menu/*` | projects, history, profile (per-device revoke), notifications, settings, docs, help, upgrade |
| `app/notification-setup.tsx` | Push permission as a first-run moment |

Build tab sub-views: `chat | files | export`.

### 6.4 Push notifications — OneSignal

- Init once in `app/_layout.tsx` (`EXPO_PUBLIC_ONESIGNAL_APP_ID`); permission requested
  **only** from `notification-setup.tsx`, never at cold open.
- Device binding: `OneSignal.User.getOnesignalId()` → `POST /v1/users/push-token`.
- Server send: `backend/internal/notify/notify.go` → OneSignal REST, **always writes the
  in-app `notifications` row first**, honest `Noop` when unconfigured.
- **Trigger vocabulary** (`notify.go:31-38`): `input_needed` (ask_user pause),
  `plan_ready`, `build_success`, `build_failed`, `build_paused`, `build_destroy`,
  `sign_in` ("fresh login on a device — theft visibility").
- No tap handler — receipt is passive by design: **reconcile-on-open** (PHASE_17 §2.6,
  *never trust a held socket*). `menu/notifications.tsx` re-reads on focus.

### 6.5 Auth on mobile

GitHub/Google OAuth via `talon://` deep link → single-use exchange code → single-flight
refresh → JWT in `expo-secure-store` (`talon.jwt` / `talon.refresh`, with read-back
verification and `secureStoreFault()`). Plus **separate wallet sign-in**: MWA connect →
sign the server challenge → proof held in memory → consumed by `/v1/auth/wallet/verify`
(`lib/walletlink.ts`, payload law: message ‖ 64-byte ed25519 sig, `slice(len-64)`; pure
RFC-4648 base64 because Hermes has no `btoa`/`Buffer`).

### 6.6 Design law — and where it is honestly violated

Documented at `IMPLEMENTATION_NOTES.md:18-22`:

> the companion app never shows code, file trees, diffs, or terminal — chat, plan
> approval, status headlines, notifications, handoff only.

**Holds:** no Monaco/editor, no xterm/terminal, no full execution trace on mobile.
**Violated:** `diff.tsx` renders per-file diffs with raw hunks, and `FileBrowser.tsx`
renders **full file contents** in the Build → Files sub-view. `CLOCKIN_SUBMISSION.md:63`
and `README.md:17` both advertise "per-file Diff" as a feature.
→ For the pitch, say **"code viewing, full execution trace and terminal are web-only"**
(accurate) rather than "mobile never shows code" (false as shipped).

---

## 7. The web IDE — `frontend/`

- **Next.js 16.3.0** App Router, React 19.2.8, Tailwind v4, Zustand, Vitest 3.
  Editor = **Monaco**, terminal = **xterm** over WebSocket.
- 18 route files: `/`, `/dashboard`, `/project/[id]`, `/login`, `/auth/callback`,
  `/settings`, `/docs`, `/plugins`, `/pricing`, `/checkout`, `/waitlist`, `/terms`,
  `/privacy`, `not-found`, `global-error`.
- 38 IDE components. Key ones: `IDELayout.tsx` (721 — grid shell, ⌘K/⌘J, resizable rail,
  dock of files/terminal/memory/triage/device), `AIChat.tsx` (2,939),
  `AgentTrace.tsx` (830 — verb mapping, progress distillation),
  `PhonePreview.tsx` (741 — emulator video over WS + WebCodecs, Metro relay),
  `ChangedFiles.tsx`, `FileTree.tsx`, `CodeEditor.tsx`, `Terminal.tsx`, `ExportPanel.tsx`,
  `SubmissionAssist.tsx` (dApp Store bundle), `TestOnPhone.tsx` (deep link + APK + QR).
- Transport: `streamPrompt` = POST + fetch body reader parsing `event:`/`data:` frames
  with silent 401→refresh→replay; `openMetroStream` = classic `EventSource` with
  `?token=`; `createAuthWebSocket` for terminal/stream/input. JWT in `localStorage`.

---

## 8. Device & release pipeline

- **Redroid** (`backend/internal/redroid/`) — Android 14 Docker image
  (`infra/redroid/`) with a **warm container pool**, orphan reconciliation each cleanup
  tick. *No Solana CLI or validators in the image — "pure Android device".*
- **Streaming gateway** (`internal/ws`, `internal/scrcpy`, `internal/video`) — emulator
  frames, adb input, terminal.
- **Metro relay** (`internal/relay`) — token-gated so a real phone loads the dev build
  from the agent's workspace; QR pairing on the Device tab.
- **Release pipeline** (`internal/release/`) — Expo prebuild → `assembleRelease` →
  per-project keystore generation (keytool, password encrypted) → `zipalign`/`apksigner`
  → **re-verified before delivery** (aapt package identity must match the project;
  `apksigner verify` cert must match the project keystore) → downloadable signed APK
  + `SUBMIT.md` + `dapp-store.json` for the Solana Mobile dApp Store Portal.

### Verified artifact (evidence for the submission)

| | |
|---|---|
| File | `backend/exports/project-1-1791012964.apk` |
| What | "Neighborhood Rewards" — generated end-to-end by Talon |
| Package | `com.talon.neighborhoodrewards` (versionName 1.0.0, minSdk 24) |
| Size | 73,227,310 bytes (**73.2 MB**) |
| Signature | `apksigner verify` passes; DN `CN=Talon Project 1, OU=Talon, O=Talon` |
| SHA-256 | `9825a369c04e3a521a46ff9fb451f4a347b1f0fb016221fd0619b1d09f7899eb` |
| Also | `backend/exports/SUBMIT.md`, `dapp-store.json` |

---

## 9. Build, test, CI

### `Makefile`

| Target | Runs |
|---|---|
| `evals` (default gate) | `vet scorecard golden dumpconfig docs test` |
| `vet` | `cd backend && go vet ./...` |
| `scorecard` | `TestFixtureScorecard\|TestGrade` over `internal/evals` |
| `golden` | `internal/evals/golden` — recorded-provider replay gate |
| `dumpconfig` | `--dump-config` diffed against `talon.composition.golden.json` |
| `docs` | `TestModelExperienceDocs` + `TestWithModelCommentContract` |
| `test` | `go test ./...` |
| `e2e-live` / `nightly` | real stack + real gateway + real redroid device + real template |

`./dev.sh` runs backend :8080 + frontend :3000 from one `.env`.

### CI — `.github/workflows/ci.yml` (3 parallel jobs, secret-free)

1. **backend** — build, `go vet`, golden transcripts, **composition golden diff**,
   `go test -race ./...`, **tree guard** (`git diff --exit-code`), **Rename guard**,
   **Kit guard** (no `@solana/web3.js`).
2. **frontend** — `npm ci`, `tsc --noEmit`, lint, vitest, build.
3. **headless** — installs `backend/template` deps so the ladder runs for real, then
   `go run ./cmd/talon-headless --profile headless`; fails if the ladder didn't pass or a
   wallet/secret marker appeared.

Note: push triggers are currently commented out — runs on `pull_request`.

---

## 10. Clock In hackathon — requirements and fit

Source: `https://solanamobile.radiant.nexus/` (Brief / Rules / Prizes / FAQ / Judges),
read 2026-10-04.

### The event

- **Clock In** — the third Solana Mobile hackathon, presented by Radiants.
- **Prize pool $135k USDC**: #1 $30k, #2 $25k, #3 $20k, #4 $15k, #5 $10k, 6th–10th $5k
  each. Plus a **$10,000 SKR Integration Prize**, Featured dApp Store Placement, marketing
  support, and a call with Anatoly Yakovenko.
- Optional **ORE matched prize** (one, top-10 only, doubles your placement prize) — Talon
  has **no ORE integration**, so ignore it.
- **Judges**: Anatoly Yakovenko, Mert (Helius), Chase (Solana Foundation), Akshay &
  Beeman (Solana Mobile), Voynich & A2nkF (Ethelsec).

### Scoring — four equal 25% criteria

| Criterion | Question |
|---|---|
| **Stickiness & PMF** | Does it resonate with the Seeker community? Habits, daily engagement? |
| **User Experience** | Intuitive, polished, enjoyable? |
| **Innovation / X-factor** | How novel? Does it stand out? |
| **Presentation & Demo Quality** | Is the idea clearly communicated? Does the demo show the core concept? |

Plus: completion (demo video), technical depth (**GitHub commits**), mobile-optimised UX
and use of mobile features, **interaction with the Solana network**, clarity of vision.

### Submission requirements (all mandatory)

1. **Functional Android APK** — must run on a **device**, not only a simulator
   (*"a video nobody can install is not a submission"*).
2. **GitHub repo** someone else can clone and run (Radiants GitHub app grants judge access).
3. **Demo video** — 3 minutes.
4. **Pitch deck** or brief presentation.

### Eligibility — Talon qualifies on every point

| Rule | Talon |
|---|---|
| Started within 3 months of launch | ✅ first commit 2026-08-04 |
| No VC / angel funding | ✅ self-funded, single developer |
| Android app from the ground up, not a PWA/port | ✅ native Android companion + Expo dev client, APK ships |
| Integrates Solana Mobile Stack + MWA, interacts with Solana | ✅ MWA sign-in, Kit template, on-chain SKR verification |
| Repo clone-and-run | ✅ `./dev.sh`, README quick start |
| dApp Store publish | Not required by deadline; **required within 30 days of winning** |

### Where Talon scores (and the risks)

**Strengths for the 25% criteria**
- *Innovation*: an AI agent that **verifies its own output on a real Android device**
  (typecheck → lint → bundle → gradle → boot → **screenshot → vision → interaction**) is
  genuinely unlike anything else in the field.
- *Presentation*: three surfaces (web IDE, pocket app, device) give a visually rich demo.
- *Technical depth*: 350 commits, 106k lines of Go, 912 backend tests, 91 routes.
- *Solana network interaction*: real on-chain `getTransaction` verification + MWA + Kit.
- *Stickiness*: the companion app is the daily-driver surface (project dashboard, history,
  push on plan-ready/build-failed/input-needed).

**Risks to manage in the deck**
- **Is it a dApp or a devtool?** The judges want consumer dApps with daily engagement.
  Lead with the *end-user outcome* (a person generates, runs and ships a real Android app
  from their phone) and with the **SKR prize**, where Talon's server-side verifier is a
  standout rather than a stretch.
- **SKR usage** is currently scoped to unlocking **export credits**, not a consumer
  feature. Frame it as "real on-chain payment gating a real capability" and show the
  fail-closed verifier — do not overclaim a token economy.
- **Mobile UX criterion**: mobile is a *companion* (no code/terminal), which is a
  deliberate, defensible design law — say so explicitly, and be ready for "why isn't the
  whole IDE on the phone?".
- **Deadline 2026-10-08** — APK URL, demo video and deck URL are the open items
  (`CLOCKIN_SUBMISSION.md` § Open items).

---

## 11. Known limitations — say these first, don't get caught

From `README.md:164-169` and repo state:

1. Billing is a **test-mode mock** behind a deliberate provider swap; mobile checkout is
   an honest stub.
2. No mobile *test* runner yet (README is stale on this — jest-expo with 26 test files
   does exist for the app's own code).
3. Plan-mode turns see attachment names/docs; pixels replay in build turns.
4. `TALONSTUDIO_SESSIONLOG=derive` migration unfinished (dual-write runs, reads still
   legacy by default).
5. Several features are **flag-gated and dark by default** (`TALONSTUDIO_INDEX`,
   `TALONSTUDIO_LSP`, `TALONSTUDIO_ROUTER`, `TALONSTUDIO_SUBAGENTS`, …) — real code, but
   not live in a shipped environment. Don't demo them as on unless they're on.
6. Design law vs. shipped reality on mobile diffs/files (§6.6).
7. `PHASE_17_COMPANION_MOBILE_APP.md` is cited as authority but is currently deleted in
   the working tree (`git status` shows ` D PHASE_17_COMPANION_MOBILE_APP.md`).

---

## 12. Open questions / not yet verified

- Exact submission deadline time — the site says "see the event schedule"; the local
  draft says **2026-10-08**. **Confirm before planning the demo recording.**
- Whether judges see the repo via the Radiants GitHub app install or a public URL.
- Whether the deck must be a specific format (the form accepts "any public URL").
- `e2e/reports/` performance numbers and `ENGINEERING_STANDARDS.md` (244 KB) contain a
  long change log not summarized here.
