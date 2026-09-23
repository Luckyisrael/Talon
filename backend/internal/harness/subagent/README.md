# subagent

The H9 delegation service: scoped child loops over the same provider
client. See `subagent.go` for the full contract.

## What the model sees

- **delegate** takes a self-contained `task` and a short `label`. By
  default (W12.3) the child runs in the **background**: the call
  acknowledges immediately, and the child's report becomes durable on its
  record — collected through **list_children**. `background: false`
  restores the blocking contract, where the report arrives between
  `----- BEGIN CHILD REPORT -----` markers framed as a report from an
  assistant that did the work — not instructions. The frame line is what
  teaches the parent to treat child output as data. `mutating: true`
  declares the child writes files (see isolation below).
- **delegate_fork** is the same blocking contract with a seeded context:
  the child's first request carries this conversation's history prefix
  (resolved at spawn time), and the project's session log forks — the
  child's key holds a byte-identical copy of the prefix plus a
  `session/forked` lineage event that both logs record, then the task and
  the final report. Omitting the task runs `DefaultForkTask` (a generic
  continuation), so a fork's prompt is never empty. A fork stays inline
  by default (the plan names `delegate` for the background flip).
- **list_children** lists label, state (running/done/idle/failed), start
  time — and, since W12.3, the durable report (or failure reason) of a
  finished child, framed the same way. Discovery without transcripts, but
  nothing is lost between the spawn and the next poll.

## Budgets and isolation (W12, law 10)

- **Pool** of 2 concurrent children (rate-limit reality); **depth 1**
  (children cannot spawn children — `ErrDeep` is the structured refusal,
  and the four delegation tools are *removed* from a child's registry,
  not merely unadvertised).
- **429 cooldown**: a provider rate limit in any child sets a 60-second
  cooldown on the whole fan-out (`ErrCooldown` carries the reason).
- **Spend cap**: every child's `CostUSD` (priced from
  `ROUTING_PRICING_JSON` via the loop's outcome) accumulates against the
  turn's cap (`TALONSTUDIO_CHILD_SPEND_CAP`, default $2, 0 = unlimited).
  The Service lives exactly one turn, so the accumulator needs no reset.
  At the cap, the next spawn refuses with `DELEGATION_SPEND` carrying the
  spent/cap figures — completed reports stay durable (the partial
  fan-out survives).
- **Wall-clock**: each child runs under `TALONSTUDIO_CHILD_WALL_SECS`
  (default 600, 0 = unlimited); overrun cancels the loop and reports the
  structured `DELEGATION_WALL`.
- **Isolation** (`TALONSTUDIO_CHILD_ISOLATION`): under the default
  `readonly` posture a `mutating: true` spawn refuses with
  `DELEGATION_ISOLATION` *before* reserving anything. Under `worktree`,
  the runner opens a private detached git worktree for the child and
  merges the diff back on dispose (capture → apply → reverse-check →
  cleanup; conflicts surface as structured `CHILD_WORKTREE` apply
  errors; a failed child's overlay is discarded, never merged). The
  spawn's reservation is synchronous in both the sync and background
  paths — every refusal is immediate, only the loop detaches.

## Failure shapes

`DELEGATION_DEPTH` (grandchild), `DELEGATION_POOL` (pool full),
`DELEGATION_COOLDOWN` (post-429 pause), `DELEGATION_SPEND` (turn budget
reached), `DELEGATION_WALL` (child overran), `DELEGATION_ISOLATION`
(mutating child under the readonly posture) — each keyed into the
failure index so the parent gets its known fix instead of a reasoning
round — and child crashes surface as errored results naming what
happened, never as parent failures.
