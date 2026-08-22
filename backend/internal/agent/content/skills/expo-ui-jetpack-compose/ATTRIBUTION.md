# Attribution — expo-ui-jetpack-compose

This skill is authored from the Jetpack Compose reference **inside** Expo's official
`expo-ui` skill. There is no standalone `expo-ui-jetpack-compose` skill upstream — the
content lives as a reference file, which we've adapted into a self-contained skill
because our loader reads only `SKILL.md` (never a `references/` subdir).

- **Source:** https://github.com/expo/skills — `plugins/expo/skills/expo-ui/references/jetpack-compose.md`
- **Vendored commit:** `472d040092900dc8bbf84dc7efb0c90abff77a0d` (2026-08-20)
- **License:** MIT

## What we changed when vendoring

Front matter was authored to our parser's schema. The upstream
`node <skill-root>/scripts/list-components.js` invocation was dropped (the script is
not vendored); its intent — "confirm the real API against the installed version" — is
preserved by pointing at the installed `.d.ts` type files in `node_modules`. Docs
URLs were kept as optional pointers. Seeker/Android-first framing, the SDK 55-vs-56
version note, and a cross-reference to `rn-best-practices` (for large-list guidance)
were added. The substance — the Android-only warning, Expo Router file placement,
`Host` usage, `LazyColumn`/`Icon`/`useNativeState` notes — is Expo's.

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
