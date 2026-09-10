# sessionlog

The H1 append-only session log: the event-sourced record every surface
derives from. See `log.go` for the full contract.

## What the model sees

- **Derived history, when SEEKERSTUDIO_SESSIONLOG=derive**: the
  model-visible conversation is derived from the log — same turns, plus
  shadowed regions replaced by their honest summaries.
- **What changes caches:** nothing — the log stores what happened;
  derivation is the read path. Dual-write keeps both stores honest until
  the flag flips.
- **Failure shapes:** a replay that cannot pair a tool_use with its result
  ends at the clean boundary before it — never a fabricated result.
