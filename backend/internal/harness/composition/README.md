# composition

The H12 preset and overlay layer, plus the W14.2 YAML composer. See
`composition.go` for the preset contract, `yaml.go` for the document
loader and overlay resolution (base → profiles[profile] → presets[preset],
last-write-wins per id, whole-config replacement), and `internal/composer`
— where rows meet live wiring; this package stays data-pure.

## What the model sees

- **Preset differences, in the prompt**: starter carries the SCL start guide
  section and spelled-out discipline; expert compresses; fast tightens the
  pruner and lists only core skills. A preset switch mid-output is refused
  with the remedy (PRESET_SWITCH) — the prompt-cache discipline holds.
- **Overlay rule**: base → mode → preset, per-key last-write-wins,
  whole-value replacement.
- **Rows, in the transcript**: a `harness/loop` row caps the rounds the
  model can spend (the pause names the budget); a `policy/*` row that
  vetoes renders exactly like a native guardrail —
  `Blocked by a project guardrail (<rule>): <reason>` — under
  TALONSTUDIO_LOOP=driver only.
