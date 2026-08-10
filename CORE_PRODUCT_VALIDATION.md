# SeekerStudio — Core Product Validation

**This is a gate, not a build phase.** It sits between Phase 8 and Phase 9. Every earlier phase doc verified its own piece in isolation — this document verifies the *whole product*, as a real user would experience it, in one continuous session. Do not begin Phase 9/10 work until this passes.

**Reference:** All prior phase docs (`PHASE_0` through `PHASE_8`), `AGENT_ENGINE_SPEC.md`, `AGENT_ENGINE_SPEC_ADDENDUM.md`, `PHASE_7_ENGINEERING_INTELLIGENCE.md`

---

## 0. Why This Exists

Every checklist so far has tested a component: does the container isolate correctly, does the dev client rebuild-gate correctly, does the trace render correctly. None of them prove the product *feels* like a competent engineer working for you across a real session. This document is that proof — a single, continuous, adversarial test run through the actual product, not a review of the code that's supposed to make it work.

---

## 1. Non-Negotiable Rules for How This Validation Is Conducted

### 1.1 Run against the real, running system
Every stage below must be performed against the actual deployed product — real prompts typed into the real chat interface, a real project, a real Redroid session. No stage may be "verified" by reading the orchestrator code and reasoning about what it should do.

### 1.2 Use real, naturally worded prompts
Prompts used in this validation should read the way you'd actually type them — including underspecified, ambiguous, or multi-part requests — not artificially clean test-harness inputs designed to make the agent look good. The point is to find where it breaks under real usage, not to confirm it handles ideal inputs.

### 1.3 Every stage's result must be recorded as evidence
A screenshot, transcript, or log excerpt for each stage's observed outcome — not a checkbox ticked from memory. If you can't produce the evidence, the stage hasn't actually been verified.

### 1.4 A failure here is a real gap, not a rationalization opportunity
If any stage fails, behaves ambiguously, or "sort of" works, that is the correct outcome of this document doing its job — it means an earlier phase's Definition of Done was signed off too early. Fix the underlying phase, don't talk yourself into treating a partial result as a pass.

### 1.5 Run this twice
Once to find what's broken. Fix it. Run the entire sequence again from a fresh project, start to finish, to confirm the fixes actually hold and didn't break something else. A single successful pass after patching individual failures is not sufficient — the full sequence must succeed in one clean run.

---

## 2. Validation Sequence

### Stage A — Project Bootstrap
1. Create a new project. Record the time it took. Confirm the file tree is empty before any prompt is sent.
2. Create a second project concurrently with the first. Confirm both have distinct identity (name, slug, bundle ID) and are running in fully isolated containers — take an action in one and confirm zero effect on the other.

### Stage B — Understanding Human Input
3. Send a deliberately underspecified prompt — something like "build me a simple app that shows my wallet balance," without specifying screens, navigation, or state management. Observe whether the agent makes and clearly states a reasonable assumption, or asks a clarifying question where genuinely needed — not whether it silently guesses and hopes.
4. Confirm the resulting plan is a real Architecture Decision Record — a stated state-management approach and folder convention — not just a bare list of screens and features.
5. Confirm no code is generated until the plan is explicitly approved.

### Stage C — Visible Thinking
6. Approve the plan and watch the live trace as generation happens. Confirm narration appears *before and during* actions, not only as a summary afterward. Confirm tool calls render with explicit verbs (`Reading`, `Creating`, `Updating`) rather than generic labels, and that exploratory calls group sensibly rather than flooding the trace.

### Stage D — Read-Before-Write Discipline
7. Send a follow-up prompt modifying a file the agent already generated (e.g., "change the balance screen's background color"). Confirm a `Reading` event for that exact file appears in the trace before the corresponding `Updating` event.
8. Manually edit that same file yourself, outside the agent's awareness, then ask for another change to it. Confirm the agent re-reads the file's real current content rather than acting on its memory of having written it earlier.

### Stage E — Code Execution & Live Preview
9. Confirm the phone preview visibly updates to reflect the change after promotion — not just that a file changed in the tree, but that the actual running app on Redroid reflects it.
10. Interact with the running app through the browser preview (tap a button, navigate a screen) and confirm the input registers correctly on the device.

### Stage F — Self-Healing Under Real Failure
11. Deliberately provoke a failure — request something likely to produce a type error, or manually introduce a bug into a generated file and ask the agent to fix it. Confirm the failure is caught by the real verification gates (not merely claimed fixed), that a heal attempt is visibly narrated in the trace, and that the fix is genuinely verified before being presented as resolved.

### Stage G — Consistency & Non-Duplication ("Learning," Concretely)
12. After the healed fix, ask for a second, related feature that should logically reuse what already exists — for example, a second screen that also needs the wallet balance. Confirm the agent reuses the existing hook/store from Stage B rather than duplicating the logic. This is what "learning and improving" means in concrete, testable terms: not repeating solved problems, not fragmenting a single concern across the codebase.

### Stage H — Genuine Self-Awareness
13. Ask the agent directly: "What have you built in this project so far, and why did you make these architectural choices?" Confirm the answer is accurate against real project memory and the real file tree — not a plausible-sounding but fabricated summary. This is the sharpest single test in the whole sequence; give it real weight.

### Stage I — Native Capability & Wallet Round Trip (if Phases 5 and 8 are complete)
14. Ask for a feature that requires a genuinely new native module not in the baked allowlist, and confirm the async rebuild path behaves correctly — the previous app stays usable while it builds, and the new capability lands without blocking the session.
15. Ask for a wallet-connect feature and confirm a full connect-to-sign round trip actually completes against the baked mock wallet, not just that a connect button appears.

---

## 3. Definition of Done

- All fifteen steps across Stages A–I are completed against the real running product, with evidence recorded for each.
- The full sequence succeeds in one clean, unbroken run, on a fresh project, after any issues found in an earlier attempt have been fixed.
- Stage H's answer is judged honestly — if it's vague, generic, or slightly wrong in a way that's being excused, that is a failure of this gate, not a pass.

**Do not resume Phase 9/10 work until this entire sequence passes cleanly, in one run, with evidence.**
