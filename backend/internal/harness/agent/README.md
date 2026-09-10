# agent

The H2 live agent registry: one handle per running turn, the steer route's
lookup. See `agent.go` for the full contract.

## What the model sees

- **Steer markers**: `[user steered the running build] <text>` and
  `[user added context mid-build] <text>` — mid-turn user messages drained
  into the round's request, honestly labeled, never later than the next
  boundary.
- **Failure shape:** a steer to a turn with no live agent is a 409, not a
  queued message.
