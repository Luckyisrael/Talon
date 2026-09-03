# SeekerStudio — Phase 12: Model Routing & Cost Controls

**Note on numbering:** this was Phase 11 in the original master roadmap. It shifted to Phase 12 when APK Export was split out and took the Phase 11 slot earlier in this project's planning. Treat this as Phase 12 going forward.

**Objective:** Route every AI call to the cheapest model tier genuinely capable of handling it, across whichever providers are configured — Gemini, OpenRouter, GPT, Claude, or any future addition — rather than sending every request to one expensive, heavy-tier model regardless of complexity. Track real cost per provider and per request so a margin problem is visible before it becomes a crisis, not after.

**Reference:** `PROJECT_DOCUMENTATION.md` (current all-calls-to-one-model state), `AGENT_ENGINE_SPEC.md` (orchestrator GENERATE/PLAN phases this routing logic hooks into)

**Framing note:** this phase assumes the multi-provider abstraction already exists and is adaptable across providers — that vendor-lock-in problem is already solved by design. This phase is about *tier-routing logic and cost visibility on top of* that abstraction, not about choosing or replacing a provider.

---

## 0. Gate Condition — Do Not Start Without This

The core orchestrator loop (`AGENT_ENGINE_SPEC.md`) must be functioning and independently verified via `CORE_PRODUCT_VALIDATION.md`. This phase is a cross-cutting concern layered on top of a working generation pipeline — it doesn't depend on the infra phases (4–8) specifically, but it does depend on the agent loop itself being solid.

---

## 1. Scope & Non-Goals

**In scope:** request-complexity classification, tier-routing policy, per-provider/per-request cost tracking, fallback behavior on provider failure, and routing-decision logging.

**Explicitly out of scope — do not touch in this phase:**
- Choosing or replacing a specific provider — the multi-provider abstraction is a given; this phase routes *across* whatever is configured.
- Billing/metering enforcement logic itself (Phase 9/10) — this phase produces cost data that feeds that system, it does not duplicate or replace its enforcement.

If a change would touch which providers exist in the abstraction layer, or how a user is billed, it does not belong in this phase.

---

## 2. Non-Negotiable Rules

### 2.1 Model selection is configuration-driven, not scattered per call site
A single routing policy — a table or ruleset mapping "request classification" to "provider + model tier" — governs every AI call. There must be no hardcoded model choice scattered across individual call sites in the orchestrator; changing which tier handles which kind of request means changing configuration, not hunting through code.

### 2.2 The provider abstraction stays genuinely uniform
Every provider (Gemini, OpenRouter, GPT, Claude, or any future addition) must be reachable through one consistent internal interface — same request shape in, same response shape out — regardless of the differences in each provider's own API underneath. Orchestrator logic must never reference a specific provider's SDK or response format directly; it only ever talks to the abstraction.

### 2.3 Requests are classified by complexity before routing
A real classification step — not an afterthought — determines whether a request is routine (a small, well-scoped edit; a simple plan confirmation) or complex (multi-file architectural decisions, a healing attempt after repeated failure, genuinely ambiguous intent requiring judgment). Routine requests route to a cheap/fast-tier model; complex requests route to a heavy-tier model. This classification is a defined, inspectable step in the pipeline, not an implicit side effect of which function happens to get called.

### 2.4 Routing policy is tunable without a full redeploy where possible
Because the right cost/quality balance will need adjusting as real usage data comes in, the routing policy should live in configuration that can be changed without a full code release cycle wherever feasible.

### 2.5 Cost is tracked per request, attributed to the real session/user/project
Every provider call's actual cost — computed from real token counts and that provider/model's real pricing — must be recorded and attributed to the specific session, user, and project it belongs to. This is the same metering data Phase 9/10's billing depends on; it must feed that exact system, not a separate, second cost ledger that could disagree with it.

### 2.6 Provider failure triggers a defined fallback, not a failed request
If a configured provider errors out or rate-limits, the system must fall back to another configured provider/model rather than simply failing the user's request. Reliability matters here — a request failing outright during a demo or beta period is a worse outcome than a slightly more expensive fallback completing it.

### 2.7 Routing decisions are logged for later tuning
Which provider/model handled a given request, and why (which classification triggered that choice), must be logged. Without this, the classification logic can't be improved from real data later — it would just be guesswork repeated indefinitely.

### 2.8 Any proxy provider still in the mix is held to its own vendor-risk awareness
If a third-party proxy (such as AgentRouter) remains one of the configured providers, it should still be understood on its own terms — its rate limits, terms of service, and uptime history documented — since it can still be a single point of failure for whichever share of traffic routes through it specifically, even though the platform is no longer solely dependent on it.

---

## 3. Verification Checklist

- [ ] A deliberately simple, routine prompt is confirmed to route to the configured cheap/fast tier — observed directly in logs, not assumed from the policy definition
- [ ] A deliberately complex prompt (multi-file change, or a healing retry) is confirmed to route to the configured heavy tier — observed directly
- [ ] Changing which provider serves a given tier is confirmed to require only a configuration change, not an orchestrator code change — tested directly by making such a change
- [ ] Cost tracked for a real request is confirmed accurate against the actual token usage and that provider's real published pricing — spot-checked by hand
- [ ] Cost data is confirmed to feed the exact same metering/usage system used by Phase 9/10's billing — no separate, potentially conflicting cost ledger exists
- [ ] A simulated provider failure or rate-limit is confirmed to trigger the defined fallback, and the user's request still completes
- [ ] Routing decisions are confirmed to be logged with enough detail (classification reason, provider/model chosen) to review and tune later
- [ ] A cost/spend view exists and reflects real, current data when checked directly

---

## 4. Definition of Done

- Every item in §3 is independently confirmed through direct testing with real prompts and a real simulated provider outage — not by reading the routing configuration and assuming correct behavior.
- The routing policy can be demonstrated to change behavior via configuration alone, with no code change required for a routine tier reassignment.
- Cost data is confirmed accurate and unified with the billing system's source of truth, with no discrepancy found under direct inspection.

**Do not proceed to Phase 13 until every box above is genuinely checked, not estimated.**
