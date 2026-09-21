# Attribution — mobile-motion

Rebuilt from Emil Kowalski's **animate-expo** skill plus Appllama's
**appllama-app-design-skill** v1.3.0 "Motion laws" section and its
`references/motion.md`. Both sources MIT.

- **Sources:**
  - https://github.com/emilkowalski/skills — `skills/animate-expo/SKILL.md`
  - https://github.com/appllama/appllama-skills — `skills/appllama-app-design-skill/SKILL.md` and `.../references/motion.md`
- **License:** MIT (Emil Kowalski; Appllama / Antmind Ventures)
- **Vendored:** 2026-09-15 Appllama v1.3.0; 2026-09-21 Emil animate-expo rebuild

## What changed from the originals

- **Decision sequence.** Emil's 9-step build order is the skeleton
  (gate → purpose → tool → properties → spring-or-timing → thread →
  press → haptics → reduced motion); Appllama's frequency gate, two
  families, and designer spring configs survive inside it nearly verbatim.
- **Talon-only cuts.** iOS-only items are out (formSheet-as-screen,
  `Link.Menu`/`Link.Preview`, `headerLargeTitle`, 120 fps plist screening):
  Talon ships Android. Lottie/Skia stay unmentioned — neither is
  preinstalled, and the skill must not send the agent shopping mid-build;
  illustration needs go through `install_package` like everything else.
- **Recipes pointer dropped.** The original ends at `RECIPES.md`, which is
  a separate artifact, not part of this skill. Pattern requests are built
  from the tables here instead.
- **Kept from the Appllama adaptation.** The verify-ladder bar
  (`run_boot`, screenshot, `run_interact` in place of simulator video),
  the Reanimated 4 `scheduleOnRN` note pinned to the installed
  `react-native-worklets` 0.7.4 / `react-native-reanimated` 4.2.1, and the
  `react-native-keyboard-controller` preference (still not preinstalled).
- **Kept from Emil, new to this skill.** Tool picker (cheapest that
  works), thread-law specifics (no `setState` in gestures, no shared
  read/write during render, `.get()`/`.set()`, `'worklet'` first line),
  haptics table with same-frame and one-per-action laws, reduced-motion
  shipping with the animation, the silent-breakage setup checklist.
