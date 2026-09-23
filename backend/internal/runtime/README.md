# runtime

Android logcat parsing: `Line` decodes `-v time` rows (time, pid, level,
tag, message) and `CrashExcerpt` pulls the lines around a fault. The
orchestrator attaches the result to the session as a `CrashEntry`
(`sess.NoteCrash`).

## What the model sees

- **Crash excerpts, not log dumps.** `CrashExcerpt(output, 20)` keeps
  twenty lines around the fault; that bounded excerpt is what the failure
  path surfaces, so a crash diagnosis starts from the stack that crashed
  instead of a raw logcat wall.
- **Parsed levels and tags.** What reaches the model is a structured entry
  (level, tag, message) — the regex parsing and timestamp decoding stay
  behind the package.
- **Nothing on healthy runs.** A turn with no crash has no logcat in its
  context at all.
