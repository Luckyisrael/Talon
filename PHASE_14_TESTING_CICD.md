# Talon — Phase 14: Testing & CI/CD

**Note on numbering:** this was Phase 13 in the original master roadmap, shifted by one when APK Export was split into its own phase earlier. Treat this as Phase 14.

**Objective:** Convert the manual, one-time adversarial verifications performed throughout every earlier phase — race-safe metering, container reconciliation, read-before-write, wallet isolation — into permanent, automated tests that run on every change. A behavior verified once by hand can silently regress later; a real test suite is what prevents that.

**Reference:** Every prior phase doc's Verification Checklist section — these are the source material for what this phase must automate.

---

## 0. Gate Condition — Do Not Start Without This

This phase can begin once the core product has passed `CORE_PRODUCT_VALIDATION.md` at least once. It is intentionally positioned late, once real behavior exists to write meaningful tests against, rather than early against a design that might still change.

---

## 1. Scope & Non-Goals

**In scope:** unit tests for core business logic, integration tests for the full agent loop, fault-injection tests converting prior manual adversarial checks into automation, frontend critical-path tests, and the CI pipeline itself.

**Explicitly out of scope — do not touch in this phase:**
- Production hardening itself (Phase 13) — this phase tests behavior, it doesn't harden the running system.
- Host provisioning and deployment (Phase 15) — CI validates code before deploy; it doesn't perform the deploy itself.

If a change would touch the production environment's actual configuration or host provisioning, it does not belong in this phase.

---

## 2. Non-Negotiable Rules

### 2.1 Unit tests cover real business logic with meaningful assertions
Orchestrator state transitions, tool allowlist enforcement, guardrail validators, and billing/metering logic each need real unit tests that assert actual expected behavior — not placeholder tests that merely confirm a function doesn't throw an exception.

### 2.2 An integration test exercises the full agent loop
Using mocked or faked AI provider responses (to keep the test fast and deterministic, not dependent on a real, costly AI call), an integration test must confirm the generate → verify → promote → heal loop works correctly end to end within a test environment.

### 2.3 Fault-injection tests convert prior manual checks into permanent regression coverage
Every adversarial scenario called out as a manual verification step in an earlier phase doc must become an automated test where feasible, specifically:
- Container restart/reconciliation (Phase 4)
- Rebuild failure fallback (Phase 5)
- Payment webhook failure handling (Phase 9/10)
- Provider outage fallback (Phase 12)
- Read-before-write discipline (Phase 7 §2.13)
- Race-safe metering under concurrent load (Phase 10)

These were manually verified once during their originating phase — that verification does not survive as protection against future regression unless it becomes a real, automated test.

### 2.4 Frontend has critical-path test coverage and a build check
At minimum, the chat/trace rendering component and the phone preview's state transitions have real component tests, and `tsc`/lint runs as part of the pipeline — not just the backend being tested while the frontend ships untested.

### 2.5 CI runs the full suite on every push and blocks merge on failure
The GitHub Actions pipeline runs `go test`, `go vet`, `tsc --noEmit`, frontend lint/build, and the dependency vulnerability scan from Phase 1, on every relevant push or pull request — and a failure genuinely blocks merge, rather than being a status check nobody enforces.

### 2.6 CI never has access to real secrets
CI uses test-mode credentials only — payment provider test keys, no production database access, no real AI provider keys beyond what's needed for a mocked/sandboxed test call. A CI environment compromised or misconfigured must never be able to touch real user data or real money.

### 2.7 Tests must be proven to actually catch the failure they claim to cover
For each fault-injection test in §2.3, deliberately break the underlying behavior once and confirm the test actually fails as a result — a test that would pass regardless of whether the behavior works is worse than no test, because it creates false confidence.

### 2.8 CI stays fast enough to remain trusted
If a full CI run grows slow enough that it becomes something routinely ignored or worked around, that defeats its purpose — cache dependencies and parallelize test execution as needed to keep the full pipeline within a reasonable time budget.

---

## 3. Verification Checklist

- [ ] Real, meaningful unit tests exist for orchestrator logic, guardrail validators, and billing/metering — confirmed by reading the actual assertions, not just confirming tests exist
- [ ] An integration test is confirmed to exercise the full generate → verify → promote → heal loop using mocked AI responses
- [ ] Automated fault-injection tests exist for container reconciliation, rebuild fallback, webhook failure handling, provider fallback, read-before-write, and race-safe metering
- [ ] Each fault-injection test is confirmed to actually fail when the underlying behavior is deliberately broken — tested directly, not assumed
- [ ] CI is confirmed to run on every push/PR and to genuinely block merge on failure — tested by deliberately submitting a failing change
- [ ] CI is confirmed to use only test-mode credentials, with no path to real production secrets or payment keys
- [ ] A full CI run completes within a reasonable, agreed time budget

---

## 4. Definition of Done

- Every item in §3 is independently confirmed — including the mutation-style check in §2.7, where a real behavior is deliberately broken to confirm its corresponding test actually catches it, not just that the test exists.
- CI is demonstrated blocking a genuinely broken pull request in practice, not just described as configured to do so.
- Every adversarial scenario manually verified once in an earlier phase now has a corresponding permanent automated test protecting it from silent regression.

**Do not proceed to Phase 15 until every box above is genuinely checked, not estimated.**
