# Attribution — mobile-motion

Adapted from Appllama's **appllama-app-design-skill** v1.3.0 "Motion laws"
section plus its `references/motion.md` (MIT).

- **Source:** https://github.com/appllama/appllama-skills — `skills/appllama-app-design-skill/SKILL.md` and `.../references/motion.md`
- **License:** MIT (Appllama / Antmind Ventures)
- **Vendored:** 2026-09-15, v1.3.0

## What changed from the original

- **Verification.** The original's bar is a manual Xcode Simulator recording
  (`xcrun simctl io booted recordVideo`) scrubbed frame by frame. Talon's
  agent has the automated equivalent in the verify ladder — `run_boot`,
  `run_vision`, `run_interact` — so the recording instruction is replaced
  with the ladder evidence path. The *judgment* (watch for feel, then frame
  by frame) is unchanged in spirit: the vision gate sees the still, the
  interact gate walks the flow.
- **Reanimated 4.** The original's `runOnJS` note is updated to Reanimated 4's
  `scheduleOnRN`, which the installed `react-native-worklets` 0.7.4 and
  `react-native-reanimated` 4.2.1 actually ship.
- **Dropped.** iOS-specific keyboard-controller framing is kept as a
  preference but the package is not in the template's preinstalled set; if a
  keyboard-tracking surface is needed, install it via `install_package` and
  prefer it over a listener.
