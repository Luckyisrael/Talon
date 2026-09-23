# release

The export pipeline: per-project signing (`keystore.go`), release assets,
and the gradle build behind the client's export route (`api/export.go`).
User-initiated — it runs outside the agent loop, after a turn is over.

## What the model sees

- **Nothing directly.** Export never runs inside a turn: no export output,
  signing error, or artifact path enters the model's context.
- **Its workspace is the input.** What the pipeline builds is whatever the
  turn left on disk — which is why the verification ladder (typecheck,
  lint, bundle) runs before a build is called done: a workspace that
  fails the ladder is one that would export broken.
- **Signing stays server-side.** Keystores, passwords, and bundle ids are
  project configuration read by this package and the export route — never
  tool results.
