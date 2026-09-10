# ralph

The H14 fresh-agent lane: N fresh-child rounds against the shared workspace
toward one immutable objective. See `ralph.go` for the full contract.

## What the model sees

- **Each round's prompt** = the objective + the previous round's bounded
  report (passed / remaining / the one blocker) — a page, not a transcript.
  No conversation history crosses rounds: the fresh context sees the
  workspace and the report, nothing else.
- **The objective is immutable** for the run; rounds cannot renegotiate it.
- **Budgets:** maxRounds defaults to 8 (our rounds are minutes, not
  seconds); the report caps at 4,096 bytes.
- **Failure shapes:** done / blocker (concrete condition + explanation,
  terminal) / capped (budget spent — honest, not a failure) / a runner
  error ends the run rather than retrying inside it.
- **The offer is always explicit, never automatic** — a ralph run costs
  real tokens; the exhaustion UI presents the choice.
