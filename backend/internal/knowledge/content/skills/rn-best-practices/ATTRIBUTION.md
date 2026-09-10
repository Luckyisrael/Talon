# Attribution — rn-best-practices

This skill is a condensed, reconciled adaptation of Vercel Labs' **`react-native-skills`**
(a collection of ~30 individual React Native best-practice rules).

- **Source:** https://github.com/vercel-labs/agent-skills — `skills/react-native-skills`
- **Vendored commit:** `dd089a8c752c966dee8bf0f27cb625ba193ffd9e` (2026-08-21)
- **License:** MIT — see the note below on how it is declared upstream.

## License provenance (read this)

The upstream repository has **no repository-level `LICENSE` file**, and its root
`package.json` is marked `"private": true`. The MIT grant is declared **per skill**,
in each rule's front matter (`license: MIT`). We vendored on that per-skill MIT
declaration (the user confirmed proceeding on this basis). Because upstream ships no
full MIT notice with a copyright line, the standard MIT text is reproduced below with
Vercel Labs named as the copyright holder based on repository ownership; if upstream
later publishes a canonical notice, replace the block below with theirs verbatim.

## What we changed when vendoring

Vercel ships each rule as its own skill with a full incorrect-vs-correct code example.
Our loader reads one `SKILL.md` per directory, so those ~30 rules were **condensed into
a single checklist**, grouped by their priority tiers, with the per-rule long-form
examples intentionally **not** vendored (they remain upstream). The guidance was then
**reconciled with Seeker reality**, which changed two things materially:

- **Lists.** Upstream leads with "use a virtualizer (LegendList/FlashList) over
  `ScrollView`+`.map()`." We keep that but reframe it as a ladder that matches what is
  actually installed here: `FlatList` (preinstalled, already virtualized) is the
  baseline default, and `@shopify/flash-list` / `@legendapp/list` are opt-in installs
  for large/heavy lists. This keeps it consistent with the project's own
  `seeker-screen-design` skill rather than contradicting it.
- **Cross-references.** Pointers to this project's `reanimated-v4`, `expo-router-nav`,
  `expo-ui`, and `seeker-screen-design` skills were added so the checklist composes
  with the rest of the skill set instead of duplicating it.

Package names (`@shopify/flash-list`, `@legendapp/list`, `expo-image`), the "wrap
strings in `<Text>`", "no falsy `&&`", GPU-only-animation, native-navigator, and
`Pressable`-over-`TouchableOpacity` guidance are Vercel's; the preinstalled-vs-install
framing and the Seeker cross-references are ours.

## MIT License

```
MIT License

Copyright (c) Vercel Labs

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
