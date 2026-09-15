# Talon — Phase 15: VPS Deployment & Observability

**Note on numbering:** this was Phase 14 (originally Phase 8 in the very first roadmap draft) in the master plan, shifted by one when APK Export was split into its own phase earlier. Treat this as Phase 15 — the final phase.

**Objective:** Bring every prior phase's work together into one real, reproducibly deployed environment on production infrastructure — proven under real concurrent load, with real cost visibility, a tested backup/restore procedure, and a working rollback path. This is the integration point where isolated, individually-verified pieces either genuinely work together, or reveal that they don't.

**Reference:** This phase is where the redroid session isolation, the billing rules, the cost tracking, and the production hardening must all function together for real.

---

## 0. Gate Condition — Do Not Start Without This

Phases 0 through 14 must all be complete and independently verified. This is deliberately the last phase — it depends on everything that came before it actually working, not just being documented as working.

---

## 1. Scope & Non-Goals

**In scope:** infrastructure-as-code for the production stack, host provisioning automation, staging smoke tests, concurrent-session load testing, a combined cost dashboard, and a tested backup/restore procedure.

**Explicitly out of scope — do not touch in this phase:**
- Any new product feature or behavior change — this phase deploys and proves what already exists, it does not build anything new.
- Retroactively fixing gaps in earlier phases — if this phase reveals an earlier phase wasn't actually done, go back and fix that phase properly rather than patching around it here.

If a change would add a new capability rather than deploy, prove, or observe an existing one, it does not belong in this phase.

---

## 2. Non-Negotiable Rules

### 2.1 The production stack is defined as code, not manual configuration
Docker Compose (or an equivalent) defines the full production stack — backend, frontend, reverse proxy/TLS termination — as a reproducible artifact. Standing up the stack must not depend on a person remembering a sequence of manual steps.

### 2.2 Host provisioning is automated and reproducible
A provisioning script handles everything a fresh VPS needs — Docker installation, the same binder/kernel verification established in Phase 0 applied to the actual production host, firewall configuration — so a new host can be stood up the same way every time, not from memory.

### 2.3 A staging smoke test gates every deploy
Before any deploy is considered live, a defined smoke test sequence — create a project, send a prompt, confirm the preview updates, confirm a wallet round trip completes — must pass against the actual newly deployed environment. A deploy that hasn't passed this is not considered complete.

### 2.4 Concurrent-session load is tested against the real host, not simulated on paper
A genuine load test creates many simultaneous sessions against the real deployed host and confirms Phase 4's capacity gating and Phase 9/10's warm pool behave correctly under real, concurrent conditions — not just in isolated, one-at-a-time testing.

### 2.5 A burn-rate dashboard reflects true combined cost
Real-time visibility into total cost — Phase 12's AI provider spend combined with actual host resource cost — must exist, so the true per-user cost of running the platform is visible on an ongoing basis, not discovered unexpectedly when a bill arrives.

### 2.6 Backup and restore is tested, not just documented
A written runbook is not sufficient — a real restore from backup (database, any persistent volumes) must actually be performed at least once, successfully, before this is considered done. An untested backup procedure is not a real safety net.

### 2.7 Rollback is demonstrated, not assumed
A defined way to revert a bad deploy quickly must be demonstrated in practice — deliberately deploy something broken in a controlled way and confirm the rollback path actually recovers the system.

### 2.8 This phase proves integration, it doesn't patch gaps
If load testing, the smoke test, or the burn-rate dashboard reveals that an earlier phase wasn't actually complete — isolation breaking under concurrent load, billing math disagreeing with actual spend — the correct response is to return to that phase and fix it properly, not to work around the symptom here.

---

## 3. Verification Checklist

- [ ] A fresh VPS is confirmed to reach a fully running Talon deployment using only the provisioning script and Compose definition — no undocumented manual step required
- [ ] The staging smoke test sequence is confirmed to pass against the real, actually deployed environment
- [ ] A genuine concurrent-session load test is confirmed to have been run against the real host, with Phase 4's capacity gating and Phase 9/10's warm pool observed behaving correctly under that real load
- [ ] The burn-rate dashboard is confirmed to reflect real, combined AI and host costs when checked against actual known spend
- [ ] A real backup restore has been performed and confirmed successful — not merely documented as a procedure
- [ ] A rollback of a deliberately broken deploy is confirmed to actually recover the system, demonstrated directly
- [ ] Any gap surfaced by this phase's testing has been traced back to and fixed in its correct originating phase, not patched around here

---

## 4. Definition of Done

- Every item in §3 is independently confirmed through direct action on real production infrastructure — a real provisioned host, a real load test, a real restore, a real rollback — not by reviewing configuration and assuming it would work.
- The full platform is confirmed functioning as one integrated system under real, concurrent, production-like conditions.
- This is the final gate before the platform can genuinely be called production-ready.

**This is the last phase in the roadmap. Once every box above is genuinely checked, Talon is ready to be presented as a real, production platform — not a demo that happens to work when nobody else is using it.**
