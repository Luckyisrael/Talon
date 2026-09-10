# SeekerStudio — Performance Plan (build, test, infra)

Diagnosis date: 2026-09-05
Scope: APK build, Redroid testing, general infra speed
Mode: diagnosis + planned fixes only; no edits made.

---

## Summary of where the time goes today

| Phase                        | Current cost (approx) | Bottleneck                            |
| ---------------------------- | --------------------- | ------------------------------------- |
| Cold Redroid boot            | 30-45s                | Fresh container, no warm pool         |
| Per-session APK install × 3  | 10-30s serial         | Sequential `adb install`              |
| Redroid ADB polling          | up to 45s             | 2s `adb devices` + `adb connect` loop |
| Gradle build (release)       | minutes               | Daemon/cache OK, but prebuild --clean |
| Gradle build (verify gate)   | minutes               | No daemon/cache flags at all          |
| Metro bundle (verify gate)   | seconds-minutes       | Cold haste map, no .expo/cache priming |
| Typecheck (verify gate)      | up to 4 min           | No `incremental: true`                |
| Lint file walk (verify gate) | seconds               | Re-walks entire workspace             |
| Template rebuild             | 2-5 min               | `npm install` not `npm ci`, no cache  |
| Scratch copy                 | seconds               | `filepath.Walk` + full read/write     |
| Per-edit rebuild thrash      | minutes each          | Patch buffer lane off (flag-gated)    |
| JS-only edit                 | still pays gradle     | Fast path exists but isn't wired      |

---

## 1. Warm Redroid container pool
File: `backend/internal/redroid/manager.go:111-173`, `:299-319`
Gap: every session launches a fresh Redroid container and tears it down at end. No warm pool. First-paint latency ≈ 60-90s (boot + 3× adb install + scrcpy push).
Fix:
- Add a pool sized N (config-driven) in `backend/internal/redroid/pool.go`.
- Pool pre-boots containers on server start, pre-installs the 3 APKs once, primes scrcpy-server, marks as "warm".
- Session start: `pool.Acquire()` with timeout; on miss, log and start cold.
- Session end: `pool.Release(container)` instead of `StopContainer`; idle containers kept warm for TTL.
- `Acquire` should hand back a struct with `ADBSerial`, `ContainerID`, `WSEndpoint` already populated.
- Add metrics: pool size, wait time, hit/miss, cold-start count.

## 2. Bake APKs into a pre-baked Redroid image
File: `infra/redroid/Dockerfile.seeker:7-9`, `infra/redroid/launch-dev.sh:88-91`
Gap: APKs are `COPY`ed into the image but not installed (Redroid has no shell during build). Each cold container pays ~10-30s × 3 for sequential `adb install -r`.
Fix:
- New `infra/redroid/Dockerfile.seeker-prebaked` that:
  1. Starts a temp Redroid with the stock image
  2. `adb install` the three APKs
  3. `adb shell pm grant` runtime perms
  4. Commits the userdata into a new image tag `seeker-redroid-prebaked:latest`
- `launch-dev.sh` selects the pre-baked image by default; falls back to stock + install if missing.
- One-time cost to bake; saves ~30s per cold container thereafter.

## 3. Parallelize APK installs / use install-multi-package
File: `infra/redroid/launch-dev.sh:89-91`, `backend/internal/devclient/manager.go:251`
Gap: 3 sequential `adb install -r` calls.
Fix:
- Short term: run them in background with `&` and `wait`.
- Better: use `adb install-multi-package apk1.apk apk2.apk apk3.apk` (single install session, parallel under the hood).
- Apply the same to the rebuild install path in `devclient/manager.go`.

## 4. Replace `waitForADB` polling with single-wait
File: `backend/internal/redroid/manager.go:299-319`
Gap: 2s `adb devices` + `adb connect` loop up to 45s; each `adb` invocation is 50-100ms of process startup.
Fix:
- Use `adb -s <serial> wait-for-device` (single command, blocks until ready, no polling).
- Or tail the container's `logcat`/`init` log for "boot completed".
- Reduce max wait from 45s to a sane cap with a clear error.

## 5. Add repo-level `gradle.properties` for the template
File: `backend/internal/tools/verify.go:349`, `backend/internal/release/build.go:121`, `backend/internal/devclient/manager.go:235`
Gap: `run_gradle` in verify path has no flags. Project-level props missing.
Fix: in template root, ensure `android/gradle.properties` contains:
```
org.gradle.daemon=true
org.gradle.caching=true
org.gradle.configuration-cache=true
org.gradle.parallel=true
org.gradle.configureondemand=true
org.gradle.jvmargs=-Xmx4g -XX:+UseParallelGC -Dfile.encoding=UTF-8
```
Apply via `build-template.sh` so every generated workspace inherits it.

## 6. Patch buffer lane ON (with measurement)
File: `backend/internal/build/patch_buffer.go` (flag-gated off per commit `G6 patch buffer lane behind flag (default off)`)
Gap: per-edit AI patches currently trigger one rebuild each. The patch buffer is built but disabled.
Fix:
- Enable behind a small default TTL (~500ms) so rapid edits coalesce.
- Add metrics: patches coalesced, builds avoided.
- Make the buffer aware of "gradle-required" patches (native deps, manifest changes) so it doesn't coalesce those.

## 7. Wire JS-only fast path in the orchestrator
File: `backend/internal/devclient/allowlist.go:111` (`IsJSOnlyChange` exists), no orchestrator consumer
Gap: when an edit only touches `.ts/.tsx/.js/.jsx/.css`, dev-client rebuild still runs gradle. The fast path helper exists but isn't connected.
Fix:
- In the rebuild decision code, check `IsJSOnlyChange(changedFiles)`.
- If true: push files to Metro, trigger `Reload` via dev-client, skip gradle entirely.
- Keep gradle path as fallback when Metro is unreachable.

## 8. Pre-warm Metro cache in template bake
File: `backend/scripts/build-template.sh:24`, `backend/internal/tools/verify.go:297`
Gap: `npx --no-install expo export` cold every verify. No `.expo/cache` priming.
Fix:
- In `build-template.sh`, after `npm ci`, run one `npx --no-install expo export` to a throwaway dir so the haste map and Metro cache are populated.
- Commit/cache `.expo/` and `node_modules/.cache/metro/` into the template's first-run snapshot.

## 9. Drop `--clean` from release `expo prebuild`
File: `backend/internal/release/build.go:115`
Gap: `expo prebuild --clean` wipes `android/` every release build → no incremental gradle.
Fix:
- Drop `--clean`.
- Surgically patch `applicationId` (and any other dynamic fields) in the generated `android/app/build.gradle` after prebuild.
- Cache `android/` keyed on `app.json` hash; reuse when hash unchanged.

## 10. `incremental: true` in template `tsconfig.json` + `tsc --build`
File: `backend/internal/tools/verify.go:115`
Gap: full project typecheck every call, no `.tsbuildinfo`.
Fix:
- Set `"incremental": true`, `"tsBuildInfoFile": ".tsbuildinfo"` in template `tsconfig.json`.
- Switch orchestrator call to `npx --no-install tsc --build` and rely on the build info file to skip unchanged files.

## 11. Cache `walkSource` per session
File: `backend/internal/tools/verify.go:160`
Gap: every `run_lint` re-stats the whole workspace.
Fix: memoize the list of lintable files per session ID, invalidate on template version bump or explicit `invalidate` event.

## 12. Hardlink workspace into scratch instead of `copyTree`
File: `backend/internal/release/build.go:416-447`
Gap: full read+write tree copy.
Fix:
- For release: `cp -al` (hardlink) the read-only template parts; only the writable `app/` and `assets/` are copied or hardlinked with copy-on-write where supported.
- Or use `rsync -a --link-dest=<template>`.
- Measure; for very large workspaces this is the difference between seconds and tens of seconds.

## 13. `npm ci` + persistent cache in `build-template.sh`
File: `backend/scripts/build-template.sh:24`
Gap: `npm install` every re-bake, no `npm ci`, no cache.
Fix:
- Switch to `npm ci` against a committed `package-lock.json`.
- Use a persistent npm cache (`--cache /var/cache/npm`) restored from a volume.
- Add a cache hit/miss metric.

## 14. Replace `waitForADB` polling (duplicate of #4)
Tracked in #4; keeping the line here so the order of work is explicit.

## 15. Parallelize sign + verify in release build
File: `backend/internal/release/build.go:129-151`
Gap: `aapt dump badging` → `apksigner sign` → `apksigner verify --print-certs` run serially. Each up to 60s timeout.
Fix: run sign and the previous APK's `aapt` extract in parallel; defer verify to async with a quick smoke check.

## 16. Shared SDK discovery helper
File: `backend/internal/release/build.go:358-412`, `backend/internal/devclient/manager.go:286-337`
Gap: two copies of `findJavaHome`/`findAndroidHome`/`findApksigner`. Drift risk, not a speed issue.
Fix: extract to `backend/internal/sdk/detect.go` and call from both. Lets the release and dev-client paths pick the same SDK.

## 17. Frontend perf (not yet diagnosed)
Files: `frontend/package.json`, `next.config.ts`, `vitest.config.ts`
Gap: not read in this pass.
Fix: read next pass; known risks to look for — `next build` without `swcMinify`, missing `experimental.optimizePackageImports`, missing image config, full Vitest run without `shard`/`testNamePattern`.

---

## Suggested execution order (highest ROI first)

1. Warm Redroid pool (#1)
2. Pre-baked Redroid image with APKs (#2)
3. Repo-level `gradle.properties` (#5)
4. Parallel APK installs (#3)
5. Patch buffer ON (#6)
6. JS-only fast path (#7)
7. Metro cache pre-warm (#8)
8. Drop `--clean` from release prebuild (#9)
9. `tsc --build` + `incremental` (#10)
10. Cache `walkSource` (#11)
11. Replace `waitForADB` polling (#4)
12. Hardlink scratch copy (#12)
13. `npm ci` + cache (#13)
14. Parallelize sign/verify (#15)
15. Shared SDK helper (#16)
16. Frontend pass (#17)

---

## Open questions for you

- Is the Redroid pool size a config knob, or should it auto-scale on session count?
- Do we keep both stock and pre-baked Redroid images, or only pre-baked once it's stable?
- Patch buffer default TTL — 500ms feels right; want it tunable per tenant?
- Is Metro fast path (dev-client) acceptable for production, or dev-only?
- Do we have a baseline-build-time benchmark today? If not, we should add one before changing anything so we can measure deltas.
