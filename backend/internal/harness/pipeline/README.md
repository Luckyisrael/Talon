# pipeline

The H3 bounded tool pipeline: read-only calls overlap in a rolling pool;
exclusive calls are ordering barriers. See `pipeline.go` for the full
contract.

## What the model sees

- **Nothing directly** — the pipeline changes scheduling, never content.
  Transcript parity between legacy and pipeline modes is a tested invariant
  (TestPipelineTranscriptParity); the model cannot tell which ran.
- **Budgets:** the pool is bounded (default 4, clamped [1,32] via
  TALONSTUDIO_TOOL_POOL); an exclusive call drains the pool and runs alone
  — its result order stays the batch order.
- **Failure shapes:** a panicked stage is recovered into a structured
  tool_result naming the crash; a refusal is synthesized in place, never a
  dropped slot.
