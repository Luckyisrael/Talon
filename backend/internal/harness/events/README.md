# events

The event taxonomy: 17 kinds, surface vs non-surface, payload shapes. See
`events.go` for the full contract.

## What the model sees

- **Surface events only** during derivation (user/assistant messages, tool
  results, questions): the transcript. Non-surface events (approvals,
  usage, phases) are durable audit records — they never enter the
  conversation.
