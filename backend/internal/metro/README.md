# metro

Metro bundler lifecycle: start/stop the bundler against a workspace,
classify its stdout/stderr lines, and fan them to subscribers — the SSE
bridge behind the preview badge, and the agent's `metro_status` tool.

## What the model sees

- **`metro_status` answers.** The model polls the bundler through
  `metro_status` and gets the classified event lines (bundling, ready,
  errors) — its only direct window into Metro.
- **Errors worth acting on.** A bundle failure surfaces as the classified
  line that names it; process spam and readiness chatter stay on the SSE
  badge, not in the model's context.
- **Nothing when it never asks.** A turn that doesn't call the tool carries
  no bundler output at all.
