# instructions

The H6 AGENTS.md chain: user-global + project + per-area convention files
under a byte budget. See `instructions.go` for the full contract.

## What the model sees

- **The chain** (system prompt section "instructions/chain"): the global
  conventions first ("# Global conventions (user)"), then the project's
  ("# Project conventions (AGENTS.md)"), then per-area files — broad to
  specific.
- **Budget:** 65,536 bytes. Under pressure, broader files are omitted whole
  (marked: `[N broader instruction file(s) omitted to fit the context
  budget]`) before the most specific is truncated (marked with the byte
  count).
- **Failure shape:** missing files are skipped silently — an empty chain is
  normal; nothing errors.
- **What changes caches:** an AGENTS.md edit changes the chain section's
  fingerprint (H7); the chain is re-read on each prompt enrichment
  (reconciliation-on-touch — no watcher), so a saved edit lands on the next
  turn.
