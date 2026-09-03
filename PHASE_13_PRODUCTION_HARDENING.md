# SeekerStudio — Phase 13: Production Hardening

**Note on numbering:** this was Phase 12 in the original master roadmap, shifted by one when APK Export was split into its own phase earlier. Treat this as Phase 13.

**Objective:** Make the platform survive real production conditions — encrypted traffic everywhere, enforced rate limits, traceable logs, visible metrics, captured errors, and connections that recover from drops instead of silently freezing. None of this is optional polish; it's what separates a working demo from a product that can be trusted with real users' sessions and data.

**Reference:** `PHASE_1_SECURITY_HYGIENE.md` (this phase extends those rules into the production environment specifically), `FRONTEND_UI_UX_OVERHAUL.md` §3.9 (WS connection states)

---

## 0. Gate Condition — Do Not Start Without This

Phase 1 must be complete and independently verified. This phase assumes the security hygiene baseline already exists and extends it into a real deployed environment — it does not re-establish credential hygiene from scratch.

---

## 1. Scope & Non-Goals

**In scope:** TLS/WSS enforcement, production-active rate limiting, structured logging with correlation IDs, metrics, error tracking, WebSocket reconnect resilience, health checks, and graceful shutdown.

**Explicitly out of scope — do not touch in this phase:**
- Automated testing/CI — that's Phase 14. This phase makes the running system resilient; it doesn't build the pipeline that tests it before deploy.
- The actual VPS provisioning and deployment steps — that's Phase 15. This phase defines what "hardened" means; Phase 15 is where it actually gets stood up on real infrastructure.

If a change would touch test suites or host provisioning scripts, it does not belong in this phase.

---

## 2. Non-Negotiable Rules

### 2.1 TLS/WSS is enforced everywhere in production
No plain HTTP or WS traffic is served in the production environment — this includes the chat stream, phone preview, and terminal WebSocket connections, not just the REST API. Local development may remain unencrypted for convenience, but production configuration must reject or redirect any plain-text attempt.

### 2.2 Rate limiting is confirmed active in the real deployed configuration
Phase 1 already required rate limiting on auth and AI-prompt endpoints at the code level. This phase confirms it's genuinely active under the real production reverse proxy/server configuration — a limit that exists in code but is bypassed by infrastructure misconfiguration does not count as enforced.

### 2.3 Logging is structured and traceable across a single user's journey
All backend components emit logs in a consistent structured format, tagged with a correlation ID (session or request ID) so a single user's actions can be traced across every component they touched, not pieced together from disconnected log lines.

### 2.4 Metrics reflect real system health
Key indicators — active sessions, running container count, API latency, error rates, and AI provider call latency/cost (feeding from Phase 12's cost tracking) — are exposed as real, current metrics, not just logged incidentally.

### 2.5 Errors are captured with context, never leaking secrets
A real error-tracking service captures unhandled exceptions on both backend and frontend, with enough context to actually debug the failure — while following Phase 1's logging-hygiene rule that no credential, token, or raw sensitive payload ever appears in a captured error report.

### 2.6 Every WebSocket connection detects and recovers from disconnection
The chat stream, phone preview, and terminal sockets must each detect a dropped connection and attempt reconnection with backoff, showing the user a genuine connecting/disconnected state — per `FRONTEND_UI_UX_OVERHAUL.md` §3.9, a connection must never silently freeze while appearing normal.

### 2.7 Every service exposes a real health check
Health check endpoints must reflect actual service health — including the health of critical dependencies — so whatever process supervises and restarts services can act on genuine signal, not just "the process is technically running."

### 2.8 Shutdowns and deploys are graceful, not abrupt
In-flight requests and active sessions are not abruptly killed on a deploy or restart. A defined drain period allows current work to complete or checkpoint cleanly before a service instance goes down.

### 2.9 Production configuration follows Phase 1's credential hygiene exactly
No development shortcut around secrets, tokens, or credentials may survive into production configuration — this is a direct extension of Phase 1's rules, re-verified specifically against the production environment, not just the codebase.

### 2.10 Critical failures generate a real alert, not a silent dashboard entry
A host at capacity, a payment provider outage, or a database failure must trigger an actual notification reaching you directly — not just an entry in a dashboard nobody is actively watching.

---

## 3. Verification Checklist

- [ ] A direct attempt to reach the production environment over plain HTTP or unencrypted WebSocket is confirmed to be rejected or redirected, tested directly
- [ ] Rate limits are confirmed active against the real deployed endpoints by deliberately exceeding them, not just present in the source code
- [ ] Logs from a real user session are confirmed traceable end to end via a shared correlation ID
- [ ] The metrics view is confirmed to reflect real, current system state when checked against what's actually happening
- [ ] A deliberately triggered backend exception is confirmed to appear in the error-tracking service with useful context and no leaked secrets
- [ ] Deliberately killing a WebSocket connection client-side is confirmed to trigger a visible reconnect attempt and successful recovery
- [ ] A health check endpoint is confirmed to report unhealthy when a real dependency is deliberately taken down
- [ ] A deploy or restart during an active session is confirmed not to abruptly terminate that session without a drain period
- [ ] A direct audit of production configuration confirms no credential hygiene shortcut has survived from development
- [ ] A simulated critical failure (host at capacity, provider outage) is confirmed to generate a real, received alert

---

## 4. Definition of Done

- Every item in §3 is independently confirmed through direct fault injection against the real running production environment — killing connections, exceeding limits, taking down dependencies — not by reviewing configuration and assuming resilience.
- The system is confirmed to degrade gracefully and observably under each of these real failure conditions, rather than failing silently or catastrophically.

**Do not proceed to Phase 14 until every box above is genuinely checked, not estimated.**
