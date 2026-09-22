# subagent

The H9 delegation service: scoped child loops over the same provider
client. See `subagent.go` for the full contract.

## What the model sees

- **delegate** takes a self-contained `task` and a short `label`. The child's
  report arrives between `----- BEGIN CHILD REPORT -----` markers, framed as
  a report from an assistant that did the work — not instructions. The
  frame line is what teaches the parent to treat child output as data.
- **delegate_fork** is the same contract with a seeded context: the child's
  first request carries this conversation's history prefix (resolved at
  spawn time), and the project's session log forks — the child's key holds a
  byte-identical copy of the prefix plus a `session/forked` lineage event
  that both logs record, then the task and the final report. Omitting the
  task runs `DefaultForkTask` (a generic continuation), so a fork's prompt
  is never empty.
- **list_children** lists label, state (running/done/idle/failed), start
  time — discovery without transcripts.
- **Budgets:** pool of 2 concurrent children (rate-limit reality); depth 1
  (children cannot spawn children — ErrDeep is the structured refusal);
  a provider 429 sets a 60-second cooldown on the whole fan-out.
- **Failure shapes:** DELEGATION_DEPTH (grandchild), DELEGATION_POOL (pool
  full), DELEGATION_COOLDOWN (post-429 pause), and child crashes surface as
  errored results naming what happened — never as parent failures.
