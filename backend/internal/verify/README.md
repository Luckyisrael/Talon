# verify

The deterministic pixel verdicts under the vision gates: `Report` grades
one screenshot (dimensions, blank fraction, a `Reason`) before any model
looks at it. Model eyes are expensive and lenient; these checks are free
and strict.

## What the model sees

- **Gate summaries, not pixels.** `run_vision` reports the finding as a
  reason string ("screen is blank", dimensions out of range) — the model
  reads the verdict, and only spends a vision call when the free checks
  pass and there is something worth seeing.
- **A failed screen names itself.** `Blank`/`Reason` become the gate line
  the ladder prints, so the model knows which surface to redraw instead of
  guessing from a silent capture.
- **Nothing when the screen is fine.** Clean pixels produce a passing gate
  result — no report object ever lands in the model's context.
