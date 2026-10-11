# Talon

**An AI app factory in your pocket.** Chat with an agent that plans, writes, verifies and ships real mobile apps — then test them on your phone live, and export a signed release APK. Sign-in is your Solana wallet; your keys never leave it.

- **Chat with a real engineering agent** — plan mode and build mode, streaming tool calls, file edits in a sandboxed workspace
- **Live device sessions** — the agent sees and drives a device (screenshots, taps, logs) to verify what it builds
- **Verification ladder** — typecheck, lint, bundle, gradle, boot, vision, interact; a release never ships on an unproven build
- **Release export** — pay for a release build on-chain (devnet SOL for testing, SKR in production), get an APK
- **Wallet auth** — sign in with Solflare by signing a plain message; the backend never holds private keys

---

## Architecture

```
talon/
├── backend/            Go backend (API :8080, agent orchestrator, sandboxed tool
│                       harness, verification ladder, release builder, store)
│   ├── cmd/server/     entrypoint
│   ├── internal/       api, orchestrator, harness, tools, skr (payment rail),
│   │                   store, release, verify, …
│   └── template/       the Expo project template every new workspace project
│                       is generated from (AGENTS.md / TEMPLATE.md drive the agent)
├── talonmobileapp/     Expo/React Native app (the thing you install on your phone)
│   ├── app/            expo-router screens (auth, chat, files, export, device)
│   ├── components/     build surface, export panel, wallet UI
│   └── lib/            API client, MWA wallet adapter glue, SKR transfer helpers
├── frontend/           web IDE surface (same backend API)
└── e2e/                end-to-end gauntlet + behaviour reports
```

One backend serves both the mobile app and the web frontend — identical routes, identical shapes. Each project the agent builds lives in `backend/workspaces/<project-id>/` as a full Expo app generated from the template.

---

## Prerequisites

| Tool | Why |
|---|---|
| **Go 1.22+** | backend |
| **Node 20+** | mobile app + template builds |
| **JDK 17** | Android/gradle stages |
| **Android SDK** (`ANDROID_HOME`) | gradle gates + APK builds |
| **Solflare** (browser or mobile) | wallet sign-in — the only auth you need to start |

---

## 1. Run the backend

```bash
cd backend
go run ./cmd/server
```

The backend needs a `backend/.env` (never committed). Minimum to start and sign in with a wallet:

```ini
JWT_SECRET=long-random-string
AI_API_KEY=…            # your LLM key
AI_BASE_URL=…           # OpenAI-compatible endpoint
AI_MODEL=…              # e.g. the chat model you want the agent on
AUTH_CALLBACK_BASE=http://localhost:8080
CORS_ALLOWED_ORIGINS=*
```

Optional (only for production billing — see *Export & payment* below):

```ini
SKR_RPC_URL=…  SKR_MINT=…  SKR_TREASURY=…
```

Health check:

```bash
curl http://localhost:8080/healthz     # → ok
```

The listen address, workspace dir (`WORKSPACE_DIR`), template dir and DB path all have sane defaults; set them only if you want things elsewhere.

## 2. Run the mobile app (WiFi — recommended)

Your phone and your computer must be on the **same network**. No cable required.

```bash
cd talonmobileapp
npm install
echo "EXPO_PUBLIC_API_URL=http://<YOUR_COMPUTER_LAN_IP>:8080" > .env.local
npx expo start
```

Find your computer's LAN IP with `ip addr` (Linux) or `ipconfig` (Windows/macOS) — e.g. `192.168.1.8`. Open the app on your phone (Talon dev client or Expo Go) and it talks to your backend over WiFi.

> **adb is optional.** `adb reverse tcp:8080 tcp:8080` is only useful when you want the phone on USB (e.g. recording the screen with scrcpy). Over WiFi the app just needs `EXPO_PUBLIC_API_URL` pointing at your computer.

## 3. Install the Talon APK (release build)

A prebuilt **Talon.apk** is attached to the submission (a Google Doc link — it can be swapped there as builds are updated). It is a standalone release build: the JS bundle is embedded, no Metro needed, only the backend.

Two ways to point it at your backend:

- **USB:** `adb reverse tcp:8080 tcp:8080` → the APK's baked `http://localhost:8080` reaches the backend through the cable.
- **WiFi:** rebuild the APK with your LAN IP baked in (the URL is frozen into the bundle at build time):

  ```bash
  cd talonmobileapp
  sed -i 's#EXPO_PUBLIC_API_URL=.*#EXPO_PUBLIC_API_URL=http://<YOUR_LAN_IP>:8080#' .env
  cd android && ./gradlew assembleRelease
  # → android/app/build/outputs/apk/release/app-release.apk
  ```

  First build compiles all native modules (~15–30 min); cached rebuilds take ~5 min.

---

## Signing in

Open the app → **Connect wallet** → Solflare. The app asks your wallet to **sign a plain message** (no transaction, no funds, no approvals). That signature is the whole credential — the backend verifies it and issues your session.

- **The backend never holds or can spend your keys.** Signing happens only inside your wallet (mobile wallet adapter).
- GitHub/Google OAuth is also wired (`AUTH_GITHUB_CLIENT_ID/SECRET`, `AUTH_GOOGLE_*`) but is optional — wallet sign-in works with nothing but `JWT_SECRET` set.

---

## How a build works

1. **Plan** — the agent reads your request and writes a plan (visible in the Plan tab).
2. **Build** — it edits files in the project's sandboxed workspace, streaming every tool call (edits, checks, shell) into the chat.
3. **Verify** — at the end of a turn that changed code, the ladder runs: `typecheck → lint → bundle → gradle → boot → vision → interact`. With a device session attached, boot/vision/interact actually launch the app and drive it.
4. **Device session** — *Device tab → Start session* gives the agent a device container (install, screenshot, tap, logs) so verification is evidence, not a promise.
5. **Export** — the *Export tab* builds a signed release and hands you the APK (see below).

Sensitive actions (shell, git push, release export) route through an **approval seam**: the agent asks, you tap allow, the answer is audited. Fail-closed — nothing sensitive runs unseamed.

---

## Export & payment

Release exports are paid on-chain. **This build ships the devnet-SOL test rail** so anyone can test end-to-end for free:

1. The panel fetches a quote — price, credits, and the treasury address come from the server, never from app constants.
2. Solflare signs a **devnet SOL** transfer to the treasury (switch Solflare to devnet first).
3. The backend verifies the settled transaction on-chain (recipient, amount, finality) and grants export credits.
4. The release build runs and the APK is delivered.

Production billing runs on **SKR**: set `SKR_RPC_URL`, `SKR_MINT` and `SKR_TREASURY` in `backend/.env` and the same flow charges the SKR token instead. Nothing in the app changes — the rail is a server decision.

> **Known gate:** a release export additionally requires a fresh **full verification pass** (no skipped gates) for the project — the fail-closed rule that keeps unverified builds from shipping. Projects whose last sweep skipped device gates can be refused at export until a sweep runs with a device session attached.

---

## Project structure (mobile app)

| Path | What it is |
|---|---|
| `app/(auth)/` | wallet + OAuth sign-in screens |
| `app/(tabs)/` | Chat · Files · Export tabs; Build/Diff/Device header |
| `components/build/` | chat stream, plan view, file tree, export panel, verify badge |
| `lib/api.ts` | typed client for every backend route (`EXPO_PUBLIC_API_URL`) |
| `lib/skr-transfer.ts` | builds the System transfer instruction Solflare signs |
| `components/wallet/` | mobile-wallet-adapter glue (challenge sign, tx sign, disconnect) |

---

## Development notes

- **Never commit `backend/.env`** or any real keys. Wallet keys stay in the wallet — by design there is no code path that exports a private key.
- Tests: backend `go test ./...`; mobile `npm test` (jest), `npx tsc --noEmit`, `npm run lint:check`.
- The agent's generated projects live under `backend/workspaces/` — safe to delete; each is regenerated from `backend/template/`.

---

## Status

Hackathon build. Everything above runs today on a local machine; there is deliberately no hosted backend — the agent, your wallet and your keys stay on hardware you control.
