# approval

The H4 consent seam: one place where a sensitive action asks the user,
once, and the answer binds. See `approval.go` for the full contract.

## What the model sees

- **Tier-2 shell refusals** already carry the ask_user instruction; the seam
  audits every decision (allowed-once / rejected / cancelled / unavailable)
  to the session log as approval/decided events — visible in the audit
  trail, not the transcript.
- **The four terminals** are the whole vocabulary: never "unknown", never
  retried at the seam. Under the never policy (headless), requests are
  unavailable without prompting — deterministic refusal.
- **One-shot:** a grant covers this action, this turn; the next sensitive
  action asks again.
