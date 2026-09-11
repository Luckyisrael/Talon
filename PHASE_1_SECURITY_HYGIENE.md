# Talon — Phase 1: Security & Hygiene Triage

**Objective:** Close every security gap that exists purely because the project moved fast during early development — leaked credentials, unverified sandbox boundaries, unaudited dependencies, unscoped network exposure. This phase produces no new features. It produces a codebase that is safe to keep building on and safe to eventually expose to real users.

**Reference:** `PROJECT_DOCUMENTATION.md`, `PHASE_0_ENVIRONMENT_PARITY.md` (must be complete first)

---

## 0. Gate Condition — Do Not Start Without This

Phase 0 must be fully done — this phase assumes a clean, verified native Ubuntu environment. Do not run this phase against a half-migrated setup.

---

## 1. Scope & Non-Goals

**In scope:** credentials, git history, network/API exposure, filesystem sandbox boundaries, command execution safety, dependency vulnerabilities, logging hygiene, baseline auth hardening.

**Explicitly out of scope — do not touch in this phase:**
- New guardrail *content* (e.g. new Solana-specific validators) — that belongs to the orchestrator phases. This phase only confirms the guardrails that already exist are actually wired in and enforced, not adding new ones.
- TLS/WSS certificate setup for production — that's the deployment phase, once there's a real host to issue certs for. This phase ensures nothing *assumes* plaintext HTTP is safe, without standing up TLS itself.
- Freemium/billing enforcement — unrelated to this phase's purpose.
- Multi-session Redroid orchestration — a later phase; this phase only audits the sandbox boundaries of whatever session model exists today.

If a change would add a new feature rather than close a hole in an existing one, it does not belong in this phase.

---

## 2. Non-Negotiable Rules

### 2.1 Secrets & Credential Hygiene
- No credential of any kind — API keys, tokens, database passwords — may exist in plaintext inside any file tracked by git, including markdown documentation. Documentation must reference "see `.env`" or similar, never the value itself.
- `.env` must be listed in `.gitignore` before any other work in this phase begins.
- A `.env.example` file must exist with every required variable name present and every value blank or a placeholder — this is what a new environment setup should reference, never a real `.env` copied from another machine.
- The previously exposed `ANTHROPIC_AUTH_TOKEN` must be confirmed rotated (not just replaced locally — confirmed invalid at the provider) before this phase is considered complete.
- Any other credential in the codebase (database connection strings, JWT signing secrets, third-party API keys) gets the same treatment — audit all of them, not just the one already found. Assume there may be more than one.

### 2.2 Git History Hygiene
- If the exposed key (or any other secret) was ever committed to git, deleting the file today is not sufficient — it remains recoverable in git history indefinitely. This must be identified and, if the repository has any shared or remote history, the secret must be treated as permanently compromised regardless of history rewriting (rotation, not history-scrubbing, is the real fix — history rewriting is a bonus, not a substitute for rotation).
- Going forward: no commit may introduce a real credential. This is a standing rule for every future phase, not just this one.

### 2.3 API & Network Exposure Hardening
- CORS configuration must be an explicit allowlist of known origins, never a wildcard, even in development — wildcard CORS during dev has a way of quietly surviving into production if nobody revisits it.
- Every WebSocket endpoint (streaming, terminal) must require the same authenticated, per-session token model already used elsewhere — confirm this is actually enforced on each socket, not just intended. An unauthenticated WS endpoint is a full sandbox escape regardless of how well-guarded the REST API is.
- Auth endpoints (login, register) must be rate-limited. Without this, brute-force credential guessing is unbounded.
- AI-prompt-triggering endpoints must be rate-limited independently of general API rate limits — these are the endpoints that cost real money per call; an unbounded loop or abusive client hitting them repeatedly is a direct cost-exposure risk, not just a stability one.

### 2.4 Filesystem & Sandbox Boundary Enforcement
- Every tool that touches the filesystem (`read_file`, `write_file`, `delete_file`, `grep_codebase`, and any future additions) must be individually confirmed to reject paths that escape the session's designated workspace — parent-directory traversal (`../`), absolute paths outside the workspace, and symlink-based escapes must all be tested, not just the traversal case that happens to already be handled.
- Confirm this boundary is enforced identically across every tool. A gap in just one (for example, if `write_file` was never given the same protection `read_file` has) is a full escape regardless of how solid the others are.
- The staging directory and the live workspace directory must both be covered by this enforcement — a boundary check that only applies to one of the two is incomplete.

### 2.5 Command Execution Safety
- `run_command`'s allowlist must be re-verified as a strict allowlist, not a denylist — anything not explicitly permitted must be rejected, not merely anything explicitly forbidden.
- Confirm command arguments are never concatenated into a raw shell string that gets interpreted by a shell — arguments must be passed as a discrete argument array to the underlying process, so that shell metacharacters in a value (semicolons, backticks, pipes) cannot be used to chain an unintended command.
- Confirm this holds even for values that originate from AI-generated content — a package name or file path suggested by the model must go through the exact same validation as one supplied by a human, since the model's output is not a trusted input source.

### 2.6 Dependency & Supply Chain Hygiene
- Run a vulnerability audit against both the Go module graph and the npm dependency tree. Any finding at high or critical severity must be resolved (upgrade, patch, or documented justification if no fix exists yet) before this phase is closed.
- This audit must become a standing check, not a one-time pass — flag it for inclusion in the CI phase later, but perform it manually now since CI doesn't exist yet.

### 2.7 Logging Hygiene
- Confirm no credential, auth token, or full user prompt content is written to logs in plaintext where it could leak through log aggregation, error reporting, or a shared log file. Structured logs should reference identifiers (user ID, session ID), not the sensitive payload itself.
- Error messages returned to the client must not leak internal paths, stack traces, or environment details — confirm error responses are sanitized before this phase closes.

### 2.8 Database File Hygiene
- The SQLite database file must not have permissions broader than the application user needs — confirm it isn't world-readable on disk.
- Confirm the database file itself is excluded from git (it almost certainly contains user data and hashed credentials by the time this matters) and covered by whatever backup strategy exists, even an informal one at this stage.

### 2.9 Baseline Auth Hardening
- Confirm bcrypt is being used with a reasonable cost factor (not a default that was fine for early testing but weak for real use).
- Confirm JWTs have a real, deliberate expiry — not indefinite — and that there's at least a plan (even if not fully implemented yet) for what happens on logout or token compromise. Full refresh-token rotation can be a later phase, but expiry must not be open-ended today.

---

## 3. Verification Checklist

- [ ] No credential exists in any tracked file, anywhere in the repository, including documentation
- [ ] `.env` is gitignored; `.env.example` exists with placeholder values only
- [ ] The exposed key is confirmed rotated at the provider, not just removed locally
- [ ] Git history has been checked for any other committed secret, and each one found has been rotated
- [ ] CORS is an explicit allowlist, verified in the actual running config, not just the source code's intent
- [ ] Every WebSocket endpoint rejects unauthenticated or invalid-token connection attempts — tested directly, not assumed
- [ ] Auth endpoints and AI-prompt endpoints both have independent rate limits, each verified by attempting to exceed them
- [ ] `read_file`, `write_file`, `delete_file`, and `grep_codebase` each individually reject traversal, absolute-path escape, and symlink escape attempts — tested against all four tools, not just one
- [ ] `run_command` rejects any command not on the allowlist, and rejects shell-metacharacter injection attempts in arguments
- [ ] A dependency vulnerability scan has been run against both Go and npm, with all high/critical findings resolved or explicitly documented
- [ ] Logs contain no plaintext credentials, tokens, or full raw prompts tied to identifiable users
- [ ] Client-facing error responses contain no internal paths or stack traces
- [ ] The SQLite database file has restrictive permissions and is excluded from git
- [ ] Bcrypt cost factor and JWT expiry are both deliberately set, not left at incidental defaults

---

## 4. Definition of Done

- Every item in §3 is independently confirmed by actually attempting the failure case (sending a traversal path, an unauthenticated WS connection, an over-limit request) and observing it get rejected — not by reading the code and assuming it works.
- No open high/critical dependency vulnerability remains unresolved without a documented reason.
- A fresh reviewer (or the AI itself, self-auditing) could read this document, attempt every check in §3 against the running system, and pass all of them without any manual fix needed mid-verification.

**Do not proceed to Phase 2 until every box above is genuinely checked, not estimated.**
