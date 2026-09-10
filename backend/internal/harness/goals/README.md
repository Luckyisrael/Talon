# goals

The H11 durable objective: one current goal per project, CAS-mutated,
surviving restarts. See `goals.go` for the full contract.

## What the model sees

- **The goal chip** (chat rail, not the transcript): `● goal active
  (N/M rounds) — <objective>`; blocked goals render the explanation
  verbatim.
- **CAS failure shape:** GOAL_STALE — the revision moved; re-read and
  retry. A stale view can never clobber newer state.
- **Budgets:** rounds are append-only; CapReached is the loop's stop-check.
- **What changes caches:** nothing — goals live outside the prompt.
