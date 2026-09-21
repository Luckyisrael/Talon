# Talon — deferred work

Tracked here so it survives the session. Anything with a checkbox is
deliberately not started; when it moves, delete the line and let the plan
files carry the detail.

## Later
- [ ] Real billing provider swap (spec 2.5/2.8): the mock stays until then.
  Includes mobile checkout wiring (paywall UI exists, CTA is an honest stub),
  restore purchases, refund and cancel paths, and store policies.
- [ ] Production secrets and data: real `JWT_SECRET`, managed R2
  credentials and bucket, OneSignal keys, Postgres or backed-up sqlite.
- [ ] Automated mobile tests: no runner exists yet. Cover auth, composer,
  and offline paths once manual API testing settles them.
- [ ] OTA updates (EAS Update) so JS fixes ship without rebuilds.
- [ ] Offline queueing for prompts composed without connectivity.
- [ ] Finish the session-log derive migration still behind
  `TALONSTUDIO_SESSIONLOG=derive`.

## Done, kept honest
- Refresh tokens with rotation, reuse-kill, sessions screen, logout-all.
- R2 object storage for attachment images with local fallback.
- Mobile identity: Talon name, talon:// scheme, com.talon.app package,
  dark splash, eas.json profiles. iOS config removed (Android only).
