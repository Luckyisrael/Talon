# SeekerStudio — Phase 7: Engineering Intelligence & Project Memory

**Objective:** The agent must behave like a disciplined senior developer working on a persistent codebase — not a stateless generator that treats every prompt in isolation. It must maintain real, durable awareness of the project's architecture, enforce single-source-of-truth data flow and separation of concerns, catch conflicts with prior decisions before generating, verify its own output the way a careful developer would, and carry genuine memory across turns and sessions.

**Reference:** `AGENT_ENGINE_SPEC.md` (orchestrator loop, §4.2 research, §4.3 pattern detection, §4.8 runtime verification), `AGENT_ENGINE_SPEC_ADDENDUM.md` §11 (live trace)

---

## 0. Gate Condition — Do Not Start Without This

Phases 0 through 4 must be complete and independently verified. Phases 5 and 6 are not strict prerequisites for this phase — it operates primarily at the prompt/orchestration layer — but should ideally be underway in parallel rather than significantly behind, since this phase assumes the underlying generate/verify/promote loop is stable.

---

## 1. Scope & Non-Goals

**In scope:** the Architecture Decision Record concept, project memory (what it holds, when it's read, when it's written), single-source-of-truth enforcement for state/data flow, separation-of-concerns structural conventions, pre-generation consistency checking, self-verification (vision check), real research capability, and visible task tracking during multi-step work.

**Explicitly out of scope — do not touch in this phase:**
- Billing, freemium enforcement, APK export, production deployment — unrelated to engineering behavior.
- Rebuilding the orchestrator's core state machine from `AGENT_ENGINE_SPEC.md` — this phase adds an intelligence and memory layer on top of the existing GENERATE → VERIFY → PROMOTE loop. It does not replace or redesign that loop.

If a change would touch container lifecycle, billing logic, or the native dev-client pipeline, it does not belong in this phase.

---

## 2. Non-Negotiable Rules

### 2.1 Plan mode produces a durable Architecture Decision Record, not a disposable feature list
The first plan for a new project must establish and persist project-wide architectural decisions — the single state-management approach, the folder/module convention, the navigation structure, and any other decision that later generations must remain consistent with. This is not a per-turn output that gets discarded after use; it is a stored record the project carries for its entire life.

### 2.2 Build mode must consult the Architecture Decision Record before generating anything
Before any code is written, the current Architecture Decision Record must be loaded into context. If a request would require deviating from an already-established decision — a second state store, a different navigation library, a conflicting folder convention — the agent must not silently comply. It must either implement the request consistently within the existing architecture, or explicitly surface the conflict to the user rather than generating contradictory code.

### 2.3 Single source of truth for data flow is enforced structurally
Only one canonical state-management mechanism may exist per project once established. Pattern detection, which already runs before generation, must specifically check for and reuse the project's existing state store rather than allowing a second, parallel one to be created out of convenience.

### 2.4 Separation of concerns is a structural convention, not prompt guidance
A real folder/module convention — screens, components, hooks, services/API calls, types — must be part of every project's Architecture Decision Record. Business logic and network calls embedded directly inside UI components are a violation of this rule, not a stylistic preference, and must be checked before a batch promotes, not just discouraged in the system prompt.

### 2.5 Project memory is a distinct, curated record — not raw chat history
A separate project-memory record must exist per project, holding architectural decisions, established conventions, and key facts about what has been built and why. This curated record — not a growing dump of full past conversation turns — must be the primary context fed into every generation call.

### 2.6 Project memory is actively written, not passively accumulated
Following a meaningful change — a new architectural decision, a newly established convention, a significant feature completed — the agent must explicitly write an update to project memory. This is a deliberate step in the loop, not something assumed to happen automatically because the conversation continued.

### 2.7 The agent tracks and visibly updates its own task breakdown during multi-step work
For any request spanning multiple files or steps, the agent must maintain a visible task/step breakdown as it works, updating status as it progresses. This is distinct from the tool-call execution trace (`AGENT_ENGINE_SPEC_ADDENDUM.md` §11): the trace shows what actions ran; the task breakdown shows what the agent understood it was trying to accomplish and how far through it is.

### 2.8 The agent verifies its own output the way a careful developer would
Static verification (typecheck, lint) is necessary but not sufficient — passing a typecheck doesn't mean the feature actually looks or works right. The vision-check step defined in the original orchestrator spec (comparing a post-bundle screenshot against the intended result) must be genuinely wired into `VERIFY_RUNTIME`, not left as unused code. A real developer confirms their work matches intent, not just that it compiles.

### 2.9 The agent researches instead of guessing when uncertain
Under the research-trigger conditions already defined (an unfamiliar API surface, a failed generation that suggests the model is guessing at a package's behavior), the agent must perform real web/GitHub research — not a placeholder, not a guessed implementation dressed up with confident-sounding code.

### 2.10 Consistency checking happens before generation, not discovered after failure
Before writing any code, the agent must check the incoming request against what's already established — the Architecture Decision Record, existing patterns, existing files — and identify likely conflicts or redundancy as an explicit step. Finding out about a conflict only after a typecheck failure is too late; it should be anticipated during context-gathering.

### 2.11 The agent can explain its own state on demand
Given a direct question — "what have you built so far, and why" — the agent must answer accurately from project memory and the real current file tree. It must not fabricate a plausible-sounding answer or lose track of earlier decisions. This is the real test of whether memory is functioning versus merely present in name.

### 2.12 Plan mode and build mode remain a hard two-phase contract
The existing boundary must be preserved, not weakened by adding memory and intelligence on top: plan mode never emits file contents, and build mode never proceeds on anything architecturally significant without an approved plan. Memory informs both phases — it does not collapse the distinction between them.

### 2.13 Reading and understanding a file is mandatory before modifying it
Before the agent writes to any file that already exists — editing, extending, or refactoring — it must first read that file's actual current content as part of its generation context for this turn. The agent must never blindly overwrite or append to a file it has not genuinely inspected in the current turn.

This applies even to files the agent itself wrote earlier in the same session. Prior authorship is not an exemption — the agent's memory of having written a file is not a substitute for reading it fresh before altering it, because the file may have changed since (through a heal cycle, a prior turn's edit, or a user's own manual change). Relying on remembered content instead of actual content is exactly what causes an agent to contradict its own earlier work, re-solve an already-solved problem, or loop on the same file across multiple turns without making real progress.

This read must be paired with genuinely parsing the user's current request against that real file content — not just loading the file into context as a formality while still generating from a stale mental model of what it contains.

---

## 3. Verification Checklist

- [ ] A new project's first plan produces and persists an Architecture Decision Record — confirmed to exist as a retrievable, stored record, not just present in that single response
- [ ] A later request that would introduce a second, conflicting state-management approach is either reconciled to the existing one or explicitly flagged to the user — tested directly by attempting such a request
- [ ] Business logic and API calls are confirmed absent from a sample of generated UI components — spot-checked directly against real generated files
- [ ] Project memory is confirmed to be a distinct, curated record, separate from raw chat history, and confirmed to be the actual context fed into generation calls — inspected directly, not assumed
- [ ] A meaningful architectural decision or completed feature results in an observable write to project memory — confirmed directly, not inferred
- [ ] A multi-step request produces a visible, updating task breakdown distinct from the raw tool-call trace
- [ ] The vision-check step is confirmed to actually execute as part of runtime verification on a real generation — not merely present as unused code
- [ ] A prompt deliberately requiring an unfamiliar or ambiguous API triggers genuine research rather than a guessed implementation — tested directly
- [ ] Asking the agent directly what has been built so far, and why, produces an answer that is accurate against project memory and the real file tree — tested directly
- [ ] Plan mode is confirmed to never emit file contents; build mode is confirmed to require an approved plan before generating anything architecturally significant
- [ ] Editing an existing file is confirmed, via the tool-call trace, to be preceded by an actual read of that exact file within the same turn — not assumed from the agent's prior familiarity with it
- [ ] Asking the agent to modify a file it authored earlier in the same session is confirmed to trigger a fresh read of that file, rather than the agent proceeding from memory of having written it

---

## 4. Definition of Done

- Every item in §3 is independently confirmed through direct testing — including deliberately adversarial tests (attempting to introduce a conflicting architecture, asking the agent to justify its own history) — not by reviewing the design and assuming it holds.
- A knowledgeable developer reviewing a real multi-turn session's output recognizes consistent architecture throughout, no duplicated state stores, genuine separation of concerns, and an agent that can accurately account for its own prior work.
- This is a judgment-heavy phase — supplement the checklist with periodic human review of real sessions, not just automated checks, before considering it genuinely done.

**Do not proceed to later phases until every box above is genuinely checked, not estimated.**
