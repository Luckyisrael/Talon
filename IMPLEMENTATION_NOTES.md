# Talon — Implementation Notes (W-series)

Running engineering log for `PLAN_WORLDCLASS.md`: per phase, what changed, and how
it connects to the two clients — the **web IDE** (`frontend/`) and the **companion
app** (`talonmobileapp/`). That plan says *what* and *why*; this file says *how it
reaches the screens*.

---

## Transport map (verified 2026-09-22, from source)

| Client | Base URL | Auth | Live updates |
|---|---|---|---|
| Web IDE — `frontend/lib/api.ts` | `BASE` (same-origin) | JWT; a 401 rotates via `POST /v1/auth/refresh` and replays the request once | `EventSource` on `/v1/projects/{id}/metro?token=…`; prompt/build streams over SSE (`Stream` in `internal/api/prompts.go`) |
| Companion — `talonmobileapp/lib/api.ts` | `EXPO_PUBLIC_API_URL` (dev: LAN IP, never localhost-on-device) | OAuth GitHub/Google, `talon://` redirect, single-use exchange code, single-flight refresh; JWT in Keystore-backed storage | Push notifications + reconcile-on-open (PHASE_17 §2.6 — never trusts a held socket) |
| Backend — `internal/api` | `/v1/*` | Bearer JWT middleware | SSE endpoints: prompts stream, metro, usage |

**Design law (PHASE_17 §2.3, permanent):** the companion app never shows code,
file trees, diffs, or terminal — chat, plan approval, status headlines,
notifications, handoff only. Any new endpoint that exposes code content stays
web-only; any new status event must map to a *headline* on mobile, never a
raw trace verb.

## Conventions for every W-series change

1. **New endpoint** → typed wrapper in `frontend/lib/api.ts` (web first); a
   mobile wrapper only if it serves chat / plan / status / notification /
   handoff. Same route + shape for both — one backend surface, two mirrors.
2. **New SSE/event kind** → document it here with the payload shape; the web
   trace renders a chip, mobile folds it into a headline status.
3. **Flag-gated behavior** → note the flag and the *client-visible difference*
   (what changes when it flips).
4. **Auth** → JWT stays in headers; the only URL-token pattern is EventSource's
   `?token=` (SSE cannot set headers) — new streams follow that same pattern
   and nothing else does.
5. **Never** change a response shape without grepping both `lib/api.ts` mirrors
   and `frontend/lib/types.ts`.

---

## Phase log

### W0 — the gate is green (2026-09-22)

- **`backend/internal/ai/client.go`** — `WithModel`/`WithRetryHook` no longer
  struct-copy the `Client` (which copied `sync.Mutex` — the copylocks vet error
  that had `make evals` red since `75c6ef8`). New `clone()` builds the copy
  field-by-field and snapshots `lastErr`/`lastErrAt` *under the lock* (the old
  copy read them with no lock — a latent race the fix also closes). The comment
  above `WithModel` ("never struct-copied") is now true; comment = contract.
  *Client impact: none — same credentials/workspace/pacer/hook sharing, same
  health semantics on `readyz`.*
- **`backend/internal/evals/phases_test.go`** — `TestPhaseW0GateGreen` runs
  `go vet ./internal/ai` from inside the suite, so a reintroduced copylock
  fails a bare `go test ./...` too, not just the Makefile's `vet` target.
- **`.github/workflows/ci.yml`** — new **Tree guard** step
  (`git diff --exit-code` after build/vet/test): the gate itself may never
  modify a tracked file. This is the mechanical answer to "don't break
  anything": drift gets caught in CI, at the gate, not at review time.
- **`PLAN_HARNESS.md`** — errata header: the "all green" status was false at
  audit time; phase laws that landed dark are itemized with pointers here.
- *No API surface change → no frontend/mobile work in this phase.*
