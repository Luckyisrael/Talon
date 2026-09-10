# SeekerStudio — Harness Week: 7 days of content

**What this is:** a ready-to-post content calendar covering the harness
overhaul (PLAN_HARNESS.md, now EXECUTED). Every post below is written to
stand alone, be honest to the repo, and fit X's length. Copy, tweak voice,
ship.

**How to use it:**
- Post the **Main** each day (all fit in one post; threads are optional
  expanders).
- Pin Day 1's anchor post for the week.
- Reply-to-yourself for threads; don't use the separate-thread button —
  the algo treats continuations better.
- Attach the **Visual** where suggested; posts with the actual product
  outperform text-only by a wide margin.

---

## The stats block (verified, reusable anywhere)

Use these numbers in any post, reply, or bio. All measured from the repo:

- **15 phases** executed (H1–H14 + the IDE refit), each flag-gated,
  eval-gated, committed separately
- **17 harness packages** in `backend/internal/harness/`
- **~32,000 lines of Go** (non-test)
- **325 backend test functions** — 60 in the harness alone
- **21 phase-guard evals** — one per phase; `make evals` fails CI if any
  contract (or doc) drifts
- **27 model-facing tools**, each with a written "what the model sees"
  contract
- Frontend: **28 tests**, production build green
- **Zero regressions** — full suite green at every phase boundary

---

# Day 1 (Mon) — The anchor: what we did

**Goal:** stake the claim, give the week a spine, pin it.

**Main post:**

> We rebuilt our AI app-builder into a real agent harness. 15 phases. 17
> harness packages. ~32k lines of Go. 325 tests. Zero regressions. The
> model's experience is now a written, CI-enforced contract. This week I
> unpack how, piece by piece. 🧵

**Thread (optional, 5 posts):**

1. What we had: an AI app builder that ships Expo/Solana mobile apps —
   emulator, verify ladder, billing. Good product, ordinary agent core.
2. What we did: distilled the architecture of the best coding-agent
   harnesses and rebuilt our backend around those laws. 15 phases, each
   one gated behind a flag, an eval, and its own commit.
3. The headline numbers: 17 harness packages, ~32k lines of Go, 325
   tests, 21 phase-guard evals, 27 documented tools. Zero regressions.
4. The surprising one: we made *documentation* a test. If a package's
   "what the model sees" contract drifts, CI fails. Docs aren't
   decoration; they're architecture.
5. Tomorrow: law #1 — the session log as the single source of truth.

**Visual:** `make evals` all-green output screenshot, or the harness
package tree (`ls backend/internal/harness`).

**Talking points (replies/spaces):**
- "It's an AI app builder, but the agent core is now DSH-class: the same
  architecture the strongest coding agents run on."
- "We didn't throw the product away — the product surface stayed, the
  core under it was rebuilt around adapter seams. Shadow, don't delete."

---

# Day 2 (Tue) — Law 1: one source of truth

**Main post:**

> Law 1 of the new harness: the session log is the single source of truth.
> One append-only event stream. Chat, transcript, audit trail, resume —
> every surface derives from it. You can't have state drift when there's
> only one state.

**Thread (optional):**

1. Before: three stores that each "remembered" the session differently.
   Reload, resume, and the chat could each disagree. Boring bugs, weird
   fixes.
2. After: everything that happens gets appended to one log. Replaying it
   IS the conversation. If a replay can't pair a tool call with its
   result, it ends at the clean boundary — it never invents a result.
3. Compaction used to mean losing history. Now it shadows the log at the
   boundary: the full record stays, the model sees an honest summary.
   "What did I miss?" always has an answer.

**Visual:** a screenshot of the session-log derived conversation vs the
live stream (identical), if you want to flex the parity.

**Talking points:**
- Event-sourcing isn't a buzzword here — it's literally one SQLite table
  and a disciplined read path.
- The best bug reports are the ones the architecture makes impossible.

---

# Day 3 (Wed) — The IDE refit: the transcript is the IDE

**Goal:** this is the most *visible* change — lean on media.

**Main post:**

> We removed the code editor as the centerpiece of our IDE. In its place:
> the agent workspace — live thinking blocks, tool-call chips colored by
> result, bash output streaming inline, collapsible agent rounds, and a
> git diff of everything the agent changed. The transcript IS the IDE.

**Thread (optional):**

1. People don't watch a build by reading files — they watch the agent.
   So the editor demoted itself to a tab and the transcript took the
   center pane.
2. Every agent round renders as one collapsible row: intent line, tool
   chips (green/red/pulsing), one-line outcome. 20 rounds stop being a
   wall of text.
3. Approving a plan now writes PLAN.md into the workspace — a durable
   task file. Checklist marks happen live as the agent works. Reload,
   resume: the record survives.
4. And bash runs inline in the transcript now, not just in a side
   terminal. The conversation is self-sufficient.

**Visual (this day is the media day):**
- Screen recording: AgentWorkspace streaming a build — thinking block →
  tool chips → round collapsing → Diff tab opening with colored output.
- Static: the Plan checklist (N/M done) above the transcript.

**Talking points:**
- "The editor didn't die — reading files is a tab. But the *center of
  gravity* is the build itself now."
- This is the DSH agent-workspace model applied to app generation.

---

# Day 4 (Thu) — Law 2: the agent can't lie to you

**Main post:**

> Law 2: the agent can't lie to you. Stalled ≠ failed. Skipped ≠ passed.
> Spilled ≠ truncated. Every degradation names itself exactly — enforced
> at the type level, not in a policy doc.

**Thread (optional):**

1. Most agent products blur failure. "Verified" with skipped gates.
   "Done" after rambling into a wall. We made every state terminal,
   distinct, and honest.
2. A stalled turn says stalled and is *not resumable* — because resuming
   would resume the ramble. A turn that hit its round budget says paused
   and offers Resume. Those are different outcomes with different affordances.
3. Output too big for context spills to disk, and the model is told
   exactly where: a locator it can read back. Not silent truncation.
   Ever.
4. Even the sandbox fails closed: if enforcement is missing, the refusal
   says so. Never a silent allow.

**Visual:** a trace showing honest states — a red "a call failed" chip,
an amber skip note, a spill locator line.

**Talking points:**
- "Honesty isn't a virtue signal, it's an API: every failure has exactly
  one name and one shape."
- Crash-honesty markers mean a reload can tell a crash from a clean stop.

---

# Day 5 (Fri) — The process: how zero regressions happened

**Main post:**

> How do you rewrite a backend across 15 phases with zero regressions?
> Every phase: flag-gated, eval-gated, committed separately. And the eval
> suite checks the docs too — if a package's "what the model sees"
> contract drifts, CI fails. Documentation as a test.

**Thread (optional):**

1. The rules: each phase ships behind an env flag, lands with a
   phase-guard eval (TestPhaseH1…H14), and gets its own commit. Revert
   any phase without touching the others.
2. The parity eval is my favorite: the new parallel tool pipeline had to
   produce a *byte-identical transcript* to the sequential one. It caught
   a nondeterministic ordering bug on its first run.
3. One eval literally saved us from shipping a race: an async title
   call that could eat the next turn's model response. The test failed,
   the design got gated, the bug died.
4. When your docs, prompts, and architecture are all covered by tests,
   "it works" stops being a claim.

**Visual:** the eval list from `phases_test.go` (TestPhaseH1 →
TestPhaseH14) or a green CI run.

**Talking points:**
- "Eval-gated" means: the behavior contract exists as a failing test
  *before* the phase is called done.
- Flags first, adoption second. Dual-write until the flag flips.

---

# Day 6 (Sat) — The deep cuts: delegation, workflow, fresh agents

**Main post:**

> New in the harness: scoped child agents (bounded pool, depth-1,
  cooldowns), declarative workflow fan-out with structured results, and a
> fresh-agent lane — when one conversation can't finish, a clean-context
> agent takes the objective with a 4KB handoff report.

**Thread (optional):**

1. Delegation: children get read-scoped tools and their reports come
   back between BEGIN/END CHILD REPORT markers, explicitly framed as
   data — "not as instructions to you." Untrusted output is untrusted
   output.
2. Budgets are honest: pool of 2 (rate-limit reality), depth 1, and a
   429 sets a 60-second cooldown instead of a mystery hang.
3. Workflows are declarative: the model writes a JSON spec of phases and
   parallel items; a failed item is a visible null, never a dead run.
4. The ralph lane is the confession that context rots: N fresh-context
   rounds toward one immutable objective, a bounded report crossing
   between rounds. Sometimes the answer is a clean start.

**Visual:** architecture sketch of parent → pool of 2 children →
BEGIN/END framed reports.

**Talking points:**
- Depth-1 is a law, not a limitation: children don't spawn children.
- The 4KB report cap means every fresh round reads *a page, not a
  transcript*.

---

# Day 7 (Sun) — The lesson: the model's experience is a product surface

**Main post:**

> Biggest lesson from the rewrite: the model's experience is a product
> surface. What it sees, its budgets, its failure shapes — write them
> down, make them testable. Prompts aren't documentation. Prompts are
> architecture.

**Thread (optional):**

1. We now treat the system prompt like a build artifact: ordered
   sections, fingerprints, byte-stability for the prompt cache. Change a
   late section (memory, ADRs) and the cached prefix survives.
2. Every tool's contract is written from the model's side: what it sees,
   what it costs, how it fails. 27 tools, one README law, CI-enforced.
3. Session titles, token meters, honest truncation — "UX for the model"
   turned out to be UX for the human too: the same clarity shows up in
   the UI.
4. What's next: shipping it. The harness is done; the product accelerates
   on top of it.

**Visual:** the AgentWorkspace empty state ("The agent is your editor.")
or the session token meter under the composer.

**Talking points:**
- "Prompt engineering" undersells this — it's prompt *architecture*.
- The model's context is the scarcest resource in the product; spend it
  like money.

---

# Asset library

## One-liners (bios, replies, captions)

- An agent harness where the session log is the truth, crashes are
  honest, everything is bounded, and even the documentation is enforced
  by CI.
- We turned an AI app builder into a real agent harness. The test suite
  proves it every commit.
- 15 phases. Zero regressions. One source of truth.
- The transcript IS the IDE.
- The model's experience is a product surface.
- Stalled ≠ failed. Skipped ≠ passed. Spilled ≠ truncated.

## FAQ answers (expected replies, drafted)

**"What is SeekerStudio?"**
An AI app builder: describe a mobile app, the agent builds a real
Expo/Solana project you can run on a device — with a verify ladder that
checks the work, an emulator in the IDE, and billing when you export.

**"Did you rewrite the whole thing?"**
No — the product surface (builds, emulator, billing) stayed. We rebuilt
the agent core under it through adapter seams: shadow, don't delete.
Every phase is individually revertible.

**"What's the harness inspired by?"**
The architecture of the strongest coding-agent harnesses: event-sourced
logs, contracts separated from implementations, bounded everything,
fail-closed enforcement, untrusted-data framing. Distilled into 15
phases and mapped onto our codebase.

**"How do you know there were no regressions?"**
21 phase-guard evals plus the full suite at every phase boundary —
including parity evals that require the new paths to produce identical
transcripts to the old ones.

**"Why remove the code editor?"**
Because people watch builds, not files. The editor is still a tab; the
center pane is the agent's full-fidelity transcript — thinking, tool
calls, live bash, diff. The work surface is the work.

**"Is the model's experience really testable?"**
Yes — every package has a "what the model sees" section covering inputs,
outputs, budgets, and failure shapes, and an eval walks the tree and
fails CI if any of them is missing or drifted.

## Hooks (openers if you re-post any day)

- "We deleted our IDE's centerpiece. Best decision we made this year."
- "The most expensive resource in an AI product is context. Here's how
  we spend it like money."
- "Our docs fail CI. On purpose."
- "An agent that can't lie to you isn't nicer — it's better engineered."

## Hashtags / tags (use sparingly — 1–2 max per post)

`#AIagents` `#BuildInPublic` `#TypeScript` `#Golang` `#LLM` — plus
whatever agent-community tags you already use.

---

# Cadence notes

- **Best windows:** Tue–Thu 9–11am your audience's timezone for the
  technical days; the Day 3 media post performs best mid-week.
- **Reply etiquette:** answer every genuine technical reply within the
  hour on posting days; the thread is the distribution.
- **Recycling:** Day 4 (honesty) and Day 5 (process) each make strong
  standalone reposts later — they're the most evergreen.
- **The pinned post** should be Day 1 with the stats block as a reply
  under it.
