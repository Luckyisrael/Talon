# Attribution — motion-review

New skill adapted from Emil Kowalski's **review-animations** (MIT). There
was no prior Talon equivalent — motion review previously lived only as the
verify ladder's automated gates, with no craft checklist behind them.

- **Source:** https://github.com/emilkowalski/skills — `skills/review-animations/SKILL.md` (and its STANDARDS.md values, inlined — embedded skills are self-contained)
- **License:** MIT (Emil Kowalski)
- **Vendored:** 2026-09-21

## What changed from the original

- **React Native translation.** Web-only checks are out: WAAPI, `:hover`
  media queries, Framer Motion shorthand props, CSS keyframes-vs-transition
  doctrine (replaced with springs-vs-timing for gesture-driven motion),
  `prefers-reduced-motion` (replaced with Reanimated `ReduceMotion`).
- **Single rule source.** Curves, durations, and spring configs are not
  duplicated here — findings cite `mobile-motion`, which owns the values.
  The two skills can never drift on numbers.
- **Verify-ladder verdict.** The original ends at approval criteria; here
  every finding closes on ladder evidence (`run_boot`, screenshot,
  `run_interact`), matching how this agent proves all other work.
- **No auto-attach.** A reviewer that fires on every write is noise. It
  loads on demand when motion is reviewed, listed in the manifest like
  everything else.
