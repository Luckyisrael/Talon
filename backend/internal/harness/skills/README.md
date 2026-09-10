# skills

The H6 multi-source skills registry: embedded knowledge plus per-project
filesystem skills. See `skills.go` for the full contract.

## What the model sees

- **The manifest** (system prompt section "skills/catalog"): one line per
  skill — `**name** — description` — with workspace-authored skills
  annotated `*(workspace)*`. Bodies never appear in the prompt.
- **read_skill** loads one body on demand; workspace skills resolve exactly
  like embedded ones. Embedded wins name clashes (binary knowledge is
  authoritative).
- **Budgets:** none at prompt level — the manifest is a handful of lines;
  bodies are pay-per-read.
- **Failure shapes:** a malformed workspace skill is skipped silently in
  the catalog (a gap, not an error); read_skill on an unknown name lists
  what exists.
- **What changes caches:** a workspace skill appearing or disappearing
  changes the manifest section's fingerprint (H7) — the cached prompt
  prefix survives; only the tail moves.
