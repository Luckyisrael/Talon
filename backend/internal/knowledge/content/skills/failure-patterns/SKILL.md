---
name: failure-patterns
description: A short, curated list of common build-loop failures and the one-line fix for each. Load when a tool error matches a known signature — TypeScript codes, Metro / Expo / gradle errors, adb failures, MWA rejection messages. Avoid re-discovering the same fix twice.
version: "index seed 2025-09-04"
autoAttach: false
triggers: [symptom:TS[0-9]+, symptom:Cannot find module, symptom:Could not resolve, symptom:Unable to resolve module, symptom:ENOSPC, symptom:ENOENT, symptom:adb: device not found, symptom:adb: no devices/emulators found, symptom:INSTALL_FAILED_UPDATE_INCOMPATIBLE, symptom:INSTALL_PARSE_FAILED, symptom:Hermes, symptom:java\.lang\.OutOfMemoryError, symptom:Gradle, symptom:Could not connect to development server, phase:PhaseHeal]
---

# Failure patterns — known cause, known fix

The orchestrator maintains an **error-signature → fix** index. When a
tool error matches a signature in this skill, the orchestrator
injects the one-line fix and (if needed) loads this skill before
re-asking the model. The seed below is the curated starting set; the
model can append via a `record_failure_pattern` tool when a pattern
hits twice in a row.

The list is short on purpose. One line per pattern, with the right
file or class to look at, and a one-line fix. If you find yourself
adding a long explanation, the explanation belongs in a different
skill — `rn-best-practices`, `mobile-defaults`, `expo-router-nav`,
`reanimated-v4`, etc. This skill is the index; the others are the
how-to.

## TypeScript

- `TS2322: Type 'X' is not assignable to type 'Y'` — the model passed
  a `string` where a `number` (or vice versa) is expected. Read the
  type definition, not the call site. Don't `as any`; narrow.
- `TS2304: Cannot find name 'X'` — `X` is not imported. Either the
  import is missing, the package isn't installed (`inspect_package`),
  or the name is misspelled. Do not invent it.
- `TS2305: Module 'X' has no exported member 'Y'` — the package's
  API differs from the model's memory. Read
  `node_modules/<pkg>/index.d.ts` (or its types entry) for the
  real name. Do not guess; the export may be renamed.
- `TS2349: This expression is not callable` — the model imported a
  type as a value, or a namespace as a function. The fix is the
  import shape, not a cast.
- `TS2531: Object is possibly 'null'` — the model did not narrow.
  Use `?.`, an explicit check, or a default. Do not `!`-assert in
  app code; reserve `!` for places where the type system can't see
  a guarantee.

## Metro / Expo

- `Error: Cannot find module 'X'` — the module isn't installed, or
  Metro can't see it. `inspect_package` first; if it's installed,
  check `tsconfig.json` `paths` and the bundler's `resolver`.
- `Unable to resolve module X from Y` — same as above, with a
  source-file context. Often a wrong import path (`@/foo` without
  the alias set up).
- `Metro hash mismatch` — the dev server is serving an older
  bundle. Restart Metro (`expo start --clear`) and reload the
  client.
- `Could not connect to development server` — the device can't
  reach the Metro server. On the redroid container, check the
  bridge IP. On a real device, the workstation and phone must be
  on the same network and the dev server must listen on `0.0.0.0`,
  not `localhost`.

## Gradle / native build

- `Could not determine java version from 'X'` — Java version
  mismatch. Expo SDK 55 needs JDK 17. The redroid image ships it;
  the workstation must too.
- `java.lang.OutOfMemoryError: Java heap space` — gradle ran out
  of memory. Set `org.gradle.jvmargs=-Xmx4g -XX:MaxMetaspaceSize=1g`
  in `android/gradle.properties`.
- `Gradle build daemon disappeared` — transient. Re-run.
- `SDK location not found` — set `ANDROID_HOME` or
  `ANDROID_SDK_ROOT` in the environment. The redroid launch script
  sets these; CI must too.
- `NDK at .../ndk/.../ did not have a source.properties file` —
  NDK version mismatch. The pinned NDK is in
  `android/build.gradle`; let the project's `npx expo prebuild`
  set it.

## adb / device

- `adb: device not found` / `adb: no devices/emulators found` —
  the redroid container is not running, or `adb` is not attached.
  `docker ps | grep redroid` first; then `adb connect
  localhost:5555`.
- `INSTALL_FAILED_UPDATE_INCOMPATIBLE` — the package is already
  installed with a different signing key. `adb uninstall
  <package>` and retry. The mock wallet uses a debug key; the
  release build uses the release key — they don't share state.
- `INSTALL_PARSE_FAILED_NO_CERTIFICATES` — APK is not signed. The
  release build needs a keystore; the debug build signs with the
  debug key automatically.
- `error: more than one device/emulator` — `adb -s <serial>`
  before the command, or set `ANDROID_SERIAL` in the
  environment.

## Hermes / runtime

- `TypeError: undefined is not a function` (Hermes) — the call
  site is using an API that doesn't exist on the platform, or
  the import is wrong. Read the installed types.
- `Invariant Violation: ...` — a React Native invariant. The
  message names the violation. Follow the suggested fix in the
  message; do not catch and ignore the invariant.
- `Possible Unhandled Promise Rejection` — an async path is not
  wrapped in `try/catch`. Wrap it, surface a user-visible retry,
  log the error to `logx` (server-side) for triage.

## MWA

- `Wallet not found` — no wallet is installed. On real hardware,
  show "install a wallet" with a deep link to the dApp Store. On
  redroid, the mock wallet is preinstalled; if it isn't,
  re-run the launch script.
- `User rejected the request` — the user dismissed the wallet
  sheet. That is a normal outcome; surface a "cancel" UI state.
  Do not retry automatically.
- `Invalid transaction: ...` — the transaction is malformed. The
  common cause is wrong blockhash, missing signature, or a fee
  payer mismatch. Read the message; do not retry the same
  transaction.
- `Base64 decode error` on an `accounts[0].address` — the model
  is treating the base64 MWA returns as base58. See `seed-vault`.

## The cooldown rule

The orchestrator will not re-inject the same pattern into the
prompt within `cooldown_rounds` rounds. If the model is still
failing on the same signature after a fix, the next step is to
**re-read the error and the file** — not to repeat the previous
fix. The cooldown is a hint that the model is in a loop; break it
by reading more, not by re-prompting faster.

## How to record a new pattern

If the same error signature hits twice in a row across rounds, the
model can call `record_failure_pattern(signature, cause, fix,
skill_to_load)`. The orchestrator appends it to the runtime
index. The next time the signature hits, the fix is injected
automatically. Don't pollute the index with one-offs.
