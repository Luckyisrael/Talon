# guardrails

Deterministic checks that run before a tool call executes — a pure decision
over (tool name, raw input, session state): no I/O, no model call, cheap
enough to sit on the single point every tool call flows through. The
always-on prompt rules tell the model what not to do; these make the
security-critical ones true whether or not the model complied.

## What the model sees

- **Refusals in full sentences.** A rejected call returns a `Decision`
  whose `Message` is written for the model: what was refused and what to
  do instead (a guardrail that only says "no" just burns rounds). The loop
  turns it into the `tool_result` the model answers from — the call itself
  never executes.
- **The stable rule slug stays behind the message.** `Decision.Rule` is
  telemetry and UI vocabulary; the model gets the prose, not the slug.
- **Nothing else.** No guardrail internals, no session-state dumps — a
  passing call looks exactly as it did before the checks existed.
