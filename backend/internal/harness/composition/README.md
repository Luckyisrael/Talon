# composition

The H12 preset and overlay layer. See `composition.go` for the full
contract.

## What the model sees

- **Preset differences, in the prompt**: starter carries the SCL start guide
  section and spelled-out discipline; expert compresses; fast tightens the
  pruner and lists only core skills. A preset switch mid-output is refused
  with the remedy (PRESET_SWITCH) — the prompt-cache discipline holds.
- **Overlay rule**: base → mode → preset, per-key last-write-wins,
  whole-value replacement.
