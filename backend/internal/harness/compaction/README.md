# compaction

The H8 context economy: the wire-copy pruner and the spill store. See
`compaction.go` for the full contract.

## What the model sees

- **Pruned tool results** arrive as `head + [N bytes omitted — use read_file
  with a range if you need the middle] + tail`. The threshold is 8,192 bytes
  (4,096-byte head, 1,024-byte tail). The model can re-read the middle
  through the normal file tools.
- **Spilled outputs** arrive as `[output too large for the transcript — N
  bytes saved to <locator>]` plus a 512-byte head. The locator is an absolute
  path under the spill directory; read_file on it returns the body verbatim.
  Spill threshold: 256 KiB.
- **What changes caches:** nothing — pruning applies to tool results after
  capture; the stored transcript keeps full fidelity, so re-derivation and
  the wire stay consistent.
- **Failure shape:** a spill that cannot write degrades to the existing
  32 KB hard cap, never a failed tool call.
