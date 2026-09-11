# RENAME.md — SeekerStudio → Talon (2026-09-11)

The answer key for future greps. If `git grep -i seeker` returns something
you don't expect, this document explains what each surviving hit means.

## What changed

| Was | Is |
|---|---|
| SeekerStudio / SeekersStudio / seekerstudio / seeker_studio | Talon / talon |
| Go module `github.com/seekersstudio/backend` | `github.com/luckyisrael/talon/backend` |
| Docker container prefix `seeker-redroid-*` | `talon-redroid-*` |
| E2E pre-baked image `seeker-redroid:latest` | `talon-redroid:latest` |
| Android package prefix `com.seekerstudio.*` | `com.talon.*` (template + devclient + maestro appId) |
| Template app id `com.seekerstudio.app` | `com.talon.app` (android/ regenerated via expo prebuild) |
| Mobile app dir `seekerstudiomobileapp/` | `talonmobileapp/` |
| Mobile app id `com.anonymous.seeker_studio_mobile_app` | `com.anonymous.talonmobileapp` |
| Skills `seeker-mobile`, `seeker-screen-design` | `talon-mobile`, `talon-screen-design` |
| `cmd/seeker-headless` | `cmd/talon-headless` |
| JWT issuer `seekersstudio` | `talon` |
| Storage keys `seeker.jwt`, `seeker.session.*` | `talon.jwt`, `talon.session.*` (signs out prior installs — pre-launch, zero cost) |
| `SEEKER_BENCH_WS` | `TALON_BENCH_WS` |
| E2E scratch `seekersstudio-e2e.db`, `workspaces-e2e` | `talon-e2e.db`, `workspaces-talon-e2e` |
| Dev db `seekersstudio.db` | wiped (archived: `backups/pre-e2e-20260911-003334.tar.gz`) |
| Dev workspaces 30–34 | wiped (user decision; same tarball) |

## What did NOT change — and why

- **`Solana Seeker`, `Seeker device`, `Seeker phone`, `Seeker hardware`,
  `Seeker app`** — these describe the *phone hardware the product targets*,
  not the product. The rename is about the product's name only. The phone
  is still the Solana Seeker.
- **`com.solana.mobilewalletadapter.*` APKs** (demowallet/demodapp) —
  Solana Mobile's own apps, not ours; not rebuilt.
- **`expokit://`** scheme — the deep link is unchanged.
- **`backups/`** — history stays history. The pre-rename tarball is never
  rewritten; it archives the old world under its old name.
- Archived pre-rename E2E logs (`e2e/live-run*.log`) — they describe the
  runs that proved the product under its old name; rewriting evidence is
  falsification.

## The gates that proved it

- `git grep -i seeker` → only the hardware vocabulary above.
- Backend 39/39 suites + vet green under the new module path.
- Frontend tsc + vitest 39/39 green; mobile tsc + eslint clean from
  `talonmobileapp/`.
- Full live E2E (`make e2e-live`) — the final certificate, run under the
  Talon name end to end.

## Known deliberate exceptions

- The repo directory moved `SeekerStudio → Talon`; the GitHub repo display
  rename is a Settings action (old URLs redirect).
- Old mobile installs signed out once (storage keys renamed) — pre-launch
  population is the dev team; zero external users affected.
- `infra/redroid/install-seeker-apps.sh` renamed to `install-talon-apps.sh`;
  the mock-wallet APKs it installs are Solana's and keep their ids.
