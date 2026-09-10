# sandbox

The H4 confinement capability: one place deciding whether an operation may
touch the filesystem, under which mode. See `sandbox.go` for the full
contract.

## What the model sees

- **Refusals** arrive as `SANDBOX: <reason>` in the tool result: writes
  outside the workspace, reads outside the project, escapes. The reason
  names the boundary and the mode.
- **Fail-closed:** when enforcement is missing the refusal is the structured
  `SANDBOX_UNAVAILABLE` — the operation is refused rather than run
  unconfined. Never a silent allow.
- **Modes:** read-only (SEEKERSTUDIO_SANDBOX=read-only) refuses every write;
  workspace-write is the default; danger-full-access is composition-change-
  only, never env-selectable.
