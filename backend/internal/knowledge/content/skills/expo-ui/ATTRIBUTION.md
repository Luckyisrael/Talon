# Attribution — expo-ui

This skill is adapted from Expo's official agent skill **`expo-ui`**.

- **Source:** https://github.com/expo/skills — `plugins/expo/skills/expo-ui/SKILL.md`
- **Vendored commit:** `472d040092900dc8bbf84dc7efb0c90abff77a0d` (2026-08-20)
- **License:** MIT

## What we changed when vendoring

Our skill loader reads only `SKILL.md` and never the upstream `references/` subdir
or `scripts/`, so this copy was rewritten to be self-contained: the dangling
`references/*.md` and `scripts/list-components.js` pointers, the `allowed-tools`
front-matter field, and the "Submitting Feedback" section were removed; the front
matter was flattened to our parser's schema (single-line `description`, `version`,
`autoAttach`, `triggers`); Talon/Android framing and a cross-reference to the
`expo-ui-jetpack-compose` skill were added. The substance (the "use @expo/ui by
default" guidance, the `List`-is-not-virtualized note, the `BottomSheet` example,
and the layer-selection guidance) is Expo's.

## MIT License

```
The MIT License (MIT)

Copyright (c) 2025-present 650 Industries, Inc. (aka Expo)

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
