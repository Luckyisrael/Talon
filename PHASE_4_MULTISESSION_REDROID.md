# Talon — Phase 4: Multi-Session Redroid Orchestration

**Objective:** Replace the current single, shared Redroid container with fully isolated, dynamically managed containers — one per active session. This is the foundational gap that makes Talon a genuinely multi-user platform rather than a single-user prototype. Nothing built after this phase (billing, load testing, production hardening) is meaningful until this is solid.

**Reference:** `PROJECT_DOCUMENTATION.md` §14 (current single-container state), original `IMPLEMENTATION_ROADMAP.md` cross-cutting risks table (host capacity model)

---

## 0. Gate Condition — Do Not Start Without This

Phases 0 through 3 must be complete and independently verified. This phase assumes a clean, secured, native environment with a working template/identity system already in place.

---

## 1. Scope & Non-Goals

**In scope:** container lifecycle (create, track, destroy), dynamic port allocation, per-container resource limits, session-to-container state tracking, orphan cleanup, and basic host capacity awareness.

**Explicitly out of scope — do not touch in this phase:**
- Warm-pool pre-booting or idle-pause-for-fast-resume optimization — this is a performance refinement that belongs with the metering/billing phase later, once basic correctness here is proven. Do not build this now; build correct, possibly-slower-to-resume isolation first.
- Video streaming, MJPEG, or touch-input pipeline changes — a separate phase, unrelated to container lifecycle itself.
- Baking the native dev client or mock wallet into the container image — later phases. This phase must not preclude that work, but does not implement it.
- GPU passthrough or hardware video encoding — out of scope entirely for this phase.

If a change would touch streaming/input code, dev-client build tooling, or billing logic, it does not belong in this phase.

---

## 2. Non-Negotiable Rules

### 2.1 One container per active session, without exception
No two sessions may ever be assigned the same Redroid container or the same ADB target simultaneously. The current single shared container (`redroid-talon-dev`, static `localhost:5555`) must be fully replaced — not run alongside the new system as a fallback.

### 2.2 Full lifecycle ownership by the backend
Container creation, health tracking, and destruction must be entirely owned and automated by the Go backend. No manual container management (starting or stopping containers by hand) should be required for the system to function correctly during normal operation.

### 2.3 Dynamic, collision-free port allocation
Each session's container must be assigned its ADB port (and any other exposed port) dynamically from a managed range — never a hardcoded value. The allocator must guarantee no two concurrently running sessions are ever assigned the same port, including under rapid, concurrent session-creation load.

### 2.4 Explicit resource limits per container
Every container must run with explicit CPU and memory limits, not unbounded host access. One session's workload must not be able to degrade the performance of another session's container running on the same host.

### 2.5 Reliable session-to-container state tracking
The mapping between a session and its container (container ID, assigned ports, creation time) must be persisted reliably — not held only in memory in a way that's lost on a backend restart. This state must be the backend's source of truth for what containers should exist.

### 2.6 Restart reconciliation
If the backend process restarts, it must reconcile its expected state (from persisted session-to-container records) against what's actually running on the host — recovering tracking of legitimately running containers, and identifying any container that no longer has a valid corresponding session. A backend restart must never simply "forget" about running containers and leave them unmanaged.

### 2.7 Orphan cleanup
Any container running on the host with no corresponding active session record must be detected and removed by a reconciliation process. This must be treated as an expected, routine cleanup mechanism, not an edge case handled manually after the fact.

### 2.8 Genuine teardown on session end
Destroying a session must actually stop and remove its container — verified at the container-runtime level, not just by marking a database record inactive. A session that appears "destroyed" in the database while its container is still running and consuming resources is a failure of this phase, not an acceptable simplification.

### 2.9 No regression on known-good boot configuration
Every dynamically created container must boot with the same validated configuration already established in earlier phases — Talon resolution and DPI (1080×2400 @ 420), the `androidboot.use_memfd=1` flag, and any other boot parameter already confirmed working. Multi-session support must not come at the cost of quietly reintroducing boot issues already solved.

### 2.10 No hardcoded single-container assumptions remain anywhere
Every code path that currently assumes a single, fixed container (streaming, terminal proxy, ADB command execution) must be updated to route to the correct session-specific container and port. A search of the codebase for the old fixed port or container name should return nothing once this phase is complete.

### 2.11 Concurrency safety under real load
Port allocation, container naming, and state tracking must be tested under genuinely concurrent session creation — not just verified one session at a time. Race conditions in allocation are a failure of this phase even if sequential testing looks correct.

### 2.12 Basic host capacity awareness
The orchestrator must track how many concurrent containers the host can reasonably support and must refuse or gracefully queue new session requests once at capacity, rather than silently overloading the host until it degrades or crashes. This does not require sophisticated scheduling — a straightforward, enforced ceiling is sufficient for this phase.

---

## 3. Verification Checklist

- [ ] Two sessions created at the same time are backed by two distinct containers, confirmed via distinct container IDs and distinct assigned ports
- [ ] Actions performed inside one session's container (installing an app, changing device state) have zero observable effect on any other session's container — tested directly, side by side
- [ ] Rapidly creating many sessions in succession produces no port collisions or container-naming collisions
- [ ] Enforced CPU/memory limits are confirmed under load — a deliberately resource-heavy process in one container does not measurably degrade a sibling container's performance
- [ ] Destroying a session results in its container being genuinely stopped and removed, confirmed via the container runtime directly, not the database alone
- [ ] Restarting the backend process while sessions are active results in correct reconciliation — no orphaned containers left untracked, no tracked sessions pointing at containers that no longer exist
- [ ] A reconciliation sweep correctly identifies and removes containers with no corresponding active session
- [ ] A full repository search confirms no remaining hardcoded reference to the old single-container port or name
- [ ] Exceeding the configured host capacity produces a clear, graceful response to the requesting client — not a silently overloaded host or an unhandled failure
- [ ] Every newly created container is confirmed to boot at the correct Talon resolution, DPI, and boot-flag configuration — spot-checked, not assumed from a single earlier validation

---

## 4. Definition of Done

- Every item in §3 is independently confirmed through direct, concurrent testing — not verified one session at a time, and not assumed from the code.
- The system correctly supports multiple simultaneous, fully isolated sessions on a single host, with no manual intervention required for normal lifecycle management.
- No trace of the old single-shared-container model remains anywhere in the running system or the codebase.

**Do not proceed to later phases (native dev-client baking, video pipeline, billing) until every box above is genuinely checked, not estimated.**
