# SeekerStudio — Frontend & IDE UI/UX Overhaul Guide

**Objective:** Resolve the specific, identifiable causes of the current UI feeling inconsistent, and rebuild the IDE workspace — especially the left-side AI chat panel — into the interactive, transparent, world-class experience the product is meant to deliver. The AI chat side must visibly show what the agent is doing and deciding, in real time, in a way a developer genuinely wants to watch.

**Reference:** `FRONTEND_REFERENCE.md` (current state audit), `AGENT_ENGINE_SPEC_ADDENDUM.md` §11 (live trace event schema — the chat panel's behavior is governed by this, not invented fresh here)

---

## 0. Root Diagnosis — Why It Currently Looks Funny

This isn't vague polish work. The reference doc points to specific, fixable causes:

1. **Three competing design languages with no stated hierarchy.** Material You, Bento Grid, and Swiss Minimalism are documented as three equal "pillars." Applied without a dominant one, this reads as visual indecision — a card looks Material-tonal here, a section looks Bento-asymmetric there, and text looks Swiss-grid somewhere else, with no consistent logic tying them together.
2. **Three accent colors compete for attention.** Amber (`#F5A623`), Solana purple (`#9945FF`), and Solana green (`#14F195`) are all documented as usable accents with no rule for when each applies — this reads as noisy rather than deliberate.
3. **Duplicate rendering paths for the same information.** `StreamRenderer` and `Timeline` both render agent activity (tool calls, phases, events) as separate components. Two different visual treatments of the same underlying event stream is a direct source of visual inconsistency.
4. **Zero motion anywhere.** No animation library, no transitions documented. A product with no deliberate motion at all reads as static and unfinished, regardless of how good the static states look.
5. **Dead code sitting next to real implementation.** `Sidebar.tsx` is a never-finished placeholder; the landing components (`Hero`, `Features`, `PhoneMockup`, `Footer`) are exported but unused since `app/page.tsx` has its own inline version. Both are latent sources of accidental drift — someone eventually edits the wrong file.
6. **Missing states everywhere.** No loading skeletons, no error boundaries, no defined empty states beyond "No files yet." Every gap in a state is a moment the product looks broken rather than deliberate.

Every rule below exists to fix one of these six causes — not to add polish on top of an already-solid foundation.

---

## 1. Scope & Non-Goals

**In scope:** the whole frontend's visual language, the IDE workspace layout and behavior, the AI chat/trace panel's implementation of the live-trace spec, motion, and state coverage (loading/error/empty).

**Explicitly out of scope — do not touch in this phase:**
- Backend event schema itself — already defined in `AGENT_ENGINE_SPEC_ADDENDUM.md` §11. This document governs how those events are *rendered*, not what they are.
- Auth/route-protection fixes (`middleware.ts`, token refresh) — real bugs, but unrelated to visual design; track separately.
- The API base URL mismatch noted in the reference doc (`8080` vs `9191`) — fix this first as a prerequisite, since it will make testing this overhaul unreliable, but it isn't itself a design task.

---

## 2. Non-Negotiable Rules

### 2.1 One dominant design language — the other two become subordinate details, not equals
Swiss Minimalism becomes the structural backbone everywhere: grid alignment, typographic hierarchy, generous whitespace, predictable layout. Material You's tonal surfaces are a *material treatment applied within that structure* — cards and elevated surfaces use tonal blending, but the grid and spacing discipline underneath stays Swiss. Bento Grid's asymmetric, playful card sizing is reserved strictly for marketing surfaces — the landing page and dashboard project grid. **The IDE workspace never uses Bento-style asymmetry** — a dense, professional tool needs predictable, aligned regions, not playful variation. This single rule resolves diagnosis #1.

### 2.2 One primary accent color; brand colors are contextual, not decorative
Amber (`--accent`) is the only color used for primary actions, focus states, and general UI emphasis across the entire product. Solana purple and green are reserved exclusively for contexts that are *specifically about Solana* — a network badge, a wallet-connection status indicator, a transaction confirmation — never used as a general accent competing with amber for the user's attention. If a screen has no Solana-specific concept on it, it should contain zero purple or green. This resolves diagnosis #2.

### 2.3 One authoritative trace component — no duplicate rendering paths
`StreamRenderer` and `Timeline` must be consolidated into a single component that owns rendering the agent's event stream, implementing `AGENT_ENGINE_SPEC_ADDENDUM.md` §11 exactly — narration, verb-specific tool labels (Reading/Creating/Updating/Deleting), grouped exploration rows, and live running→done/fail status transitions. There is one visual representation of "what the agent is doing," not two. This resolves diagnosis #3 and is the direct implementation of what you asked for: the chat panel showing everything the agent is doing and deciding.

### 2.4 Real, restrained motion — present everywhere, not sprinkled
Pick one motion approach and apply it consistently — either a lightweight animation library or CSS transitions/the View Transitions API — but the decision must be made once and used everywhere, not chosen ad hoc per component. Minimum required motion:
- Chat messages and trace rows animate in as they stream, not pop in instantly.
- Panel resize, file tree expand/collapse, and modal open/close all transition smoothly.
- Hover and active states on every interactive element have a genuine transition, not an instant color snap.
- Motion must be restrained — fast, purposeful, never decorative for its own sake. This resolves diagnosis #4.

### 2.5 Dead code is removed, not left to drift
`Sidebar.tsx` (unfinished placeholder) and the unused landing components (`Hero`, `Features`, `PhoneMockup`, `Footer`) are either finished and wired in for real, or deleted outright. Nothing exported and unused may remain in the codebase after this phase — this resolves diagnosis #5 and prevents the next contributor from editing the wrong file by mistake.

### 2.6 Every interactive element has all four states, genuinely designed
Default, hover, active/pressed, and disabled must each be deliberately styled — not left to browser defaults or a single opacity change covering every state. This applies to every button, input, tab, and file-tree row in the product.

### 2.7 Every data-driven view has loading, error, and empty states
No view may simply be blank or silently missing while data loads or fails to load. This specifically includes: file tree loading, phone preview connecting/reconnecting, chat stream interruption, and the dashboard's project list — each needs a real, designed state for "loading," "failed," and "genuinely empty," not just the happy path. This resolves diagnosis #6.

### 2.8 Existing hard constraints are preserved, not relaxed
The 44×44px minimum tap target, the corner-nesting radius math (inner radius = outer radius − container padding), and 1px hairline borders at `rgba(150,150,150,0.18)` are correct and stay exactly as defined — this overhaul is about consistency and coherence, not loosening constraints that already work.

---

## 3. IDE-Specific Rules (This Is the Priority)

### 3.1 The AI chat panel is the primary, always-visible left region — not squeezed alongside the file tree
Chat is the dominant, persistent element on the left. The file tree becomes a slim, collapsible rail or an explicit tab the user switches to — it does not permanently compete with chat for the same fixed space. The product's core value is watching the agent work; the layout must reflect that chat is the priority, not an equal partner with file browsing.

### 3.2 The chat panel fully implements the live-trace behavior — this is the single most important rule in this document
Per `AGENT_ENGINE_SPEC_ADDENDUM.md` §11:
- Narration streams as it's generated, appearing before and during actions — never dumped after the fact.
- Every file operation renders with its specific verb: "Reading `X`," "Creating `X`," "Updating `X`," "Deleting `X`" — never a generic tool name.
- Consecutive read-only exploration calls collapse into a single expandable "Explored N searches" row; mutating actions (write, install, build, verify, promote) are always individually visible, never collapsed.
- Every action visibly transitions from a running state to a done/fail state — nothing appears pre-resolved.
- The full trace persists and is scrollable per turn — a developer should be able to scroll back and re-open a past group, not lose it once it's off-screen.
- A "Reading `X`" row must visibly precede an "Updating `X`" row for the same file within a turn — this is both a UX detail and, per Phase 7 §2.13, a live audit signal that the agent is actually reading before writing.

### 3.3 The center panel treats the editor as primary, the terminal as summonable
Monaco is the default, dominant view. The terminal behaves like a bottom drawer the user explicitly opens and closes (matching the familiar VS Code pattern), rather than a fixed panel permanently consuming space when the user isn't using it.

### 3.4 The phone preview visibly communicates its own state
The preview should read as an actual device, not a bare video feed — deliberate phone-frame chrome, a clear connection-status indicator, and a visible signal when a promoted change is about to land versus already applied. Given the staging/promote architecture already established, the user should be able to tell, just by glancing at the preview, whether they're looking at the current live state or watching an update land.

### 3.5 The empty file tree state is deliberately designed, not a blank void
Per Phase 2's rule, a fresh project's file tree is empty until the AI generates something. That empty state must be an intentional, inviting design — pointing the user toward the chat panel to get started — not an unstyled empty list that reads as broken.

### 3.6 A command palette is table stakes for a serious IDE
Add a Cmd+K/Cmd+P-style command palette for file search and common actions. This is expected baseline behavior for any tool positioning itself as a real development environment in 2026 — its absence reads as unfinished regardless of how polished everything else is.

### 3.7 Real-time connection states are never silently broken
The chat stream, the phone preview socket, and the terminal socket must each show a genuine reconnecting/disconnected state when the underlying connection drops — never fail silently while the UI continues to look normal. A developer should always be able to tell, at a glance, whether what they're looking at is live.

---

## 4. Verification Checklist

- [ ] A person unfamiliar with the product, shown three different screens (landing, dashboard, IDE), describes them as one coherent visual language — not three different styles
- [ ] Purple and green appear only on screens with an actual Solana-specific concept present — confirmed by auditing every screen directly
- [ ] Only one component renders the agent's event stream; `StreamRenderer` and `Timeline` no longer exist as separate, overlapping implementations
- [ ] Sending a prompt shows narration and trace rows appearing progressively as the agent works, not dumped at once when the turn completes
- [ ] Every file operation in the trace shows its specific verb (Reading/Creating/Updating/Deleting) — spot-checked against a real session, not the component's source code
- [ ] An "Updating" row for a file is always preceded by a "Reading" row for that same file within the same turn, observed directly
- [ ] Every button, input, tab, and file-tree row has a visibly distinct hover, active, and disabled state
- [ ] Disconnecting the phone preview or chat stream mid-session is confirmed to show a real reconnecting/disconnected indicator, not a silently frozen UI
- [ ] A freshly created project's empty file tree is confirmed to show a deliberate, inviting empty state, not a blank area
- [ ] `Sidebar.tsx` and the unused landing components are confirmed either fully implemented and in use, or removed from the codebase entirely
- [ ] Cmd+K/Cmd+P command palette is confirmed working for file search from within the IDE

---

## 5. Definition of Done

- Every item in §4 is independently confirmed by direct use of the running product — not by reviewing the component code and assuming it looks right.
- All six root causes named in §0 are individually confirmed resolved, not just generally "improved."
- The AI chat panel, specifically, is judged against the original screenshot reference and `AGENT_ENGINE_SPEC_ADDENDUM.md` §11 side by side — if it doesn't match that standard of transparency and interactivity, this phase is not done, regardless of how the rest of the UI looks.

**This is the highest-leverage design work in the product — the chat panel is what a user watches for most of their time in the app. Prioritize §3.2 above every other item in this document if time is constrained.**
