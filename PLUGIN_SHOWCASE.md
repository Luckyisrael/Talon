# Plugin showcase and talon-mobile-ui build guide

## Goal

A public web surface where users browse Talon plugins, see each component,
copy one command, and paste it to the AI to get that component installed in
their project. Plus the build standard for finishing the talon-mobile-ui
components themselves, whose `references/` files are currently minimal
starting points, not finished work.

## Where things live (decision)

- `talon-plugin-directory` stays data-only: manifests, skills, references,
  registry.json. No website, no app code. It is fetched by hash at build
  time, so everything in it must stay small and static.
- The showcase lives in the Talon web app under `frontend/app/plugins/`:
  one deployment, one design system, and the audience is already there.
  Routes: `/plugins` (list), `/plugins/[name]` (detail with preview,
  props, and the copy command), plus creating/using docs folded into the
  existing Docs page. Header entry goes through the shared nav data in
  `frontend/components/marketing/links.ts` so nav and footer cannot drift.
- A separate repo or site is not worth it until plugin authors outnumber
  Talon users. One repo, one deploy, zero new infrastructure.

## Component build standard

Every component in talon-mobile-ui must meet this bar before it gets a
showcase card. Build each one inside a real Talon test project first, on
device, then promote the final code to `references/` and re-sign the
manifest hashes.

1. Typed props with an interface, sensible defaults, no `any`.
2. Every state rendered: ready, pending, connected, disconnected, error,
   and cancelled (a dismissed wallet sheet returns to ready silently,
   never an error state).
3. Theme through the app's own tokens. The reference shows structure and
   flow; the showcase shows it restyled. No hardcoded colors that fight
   the host app.
4. React Native only: `View`, `Text`, `Pressable`, `StyleSheet`. No DOM,
   no `className`, no CSS. Inputs stay 16px (iOS zoom rule).
5. No em or en dashes anywhere: code, comments, or labels. This is a
   builder rule now (`backend/internal/knowledge/content/rules/core.md`),
   but check by eye before publishing.
6. One screenshot per state, taken on device, stored with the showcase.
   Screenshots are the v1 preview (see below).
7. Each component maps to exactly one skill, and each skill description to
   exactly one component. Connect vs balance vs send, never two. This
   exclusivity is what lets the agent load one skill at a time instead of
   guessing.

## The v1 components

| Component | Skill | Job (only this) |
| --- | --- | --- |
| `ConnectButton` | `wallet-connect` | Wallet connection and the connected account |
| `BalanceView` | `sol-balance` | Address display and SOL balance |
| `SendTransaction` | `sol-send` | Signing, sending, memos, on-chain writes |
| `SwapView` | `swap` (planned) | Token swap UI with quoting and slippage |

All UI components live under talon-mobile-ui. It is the UI umbrella:
anything the user sees on screen belongs here, never in a new plugin.

Build order: `ConnectButton` first (the other three assume a connected
account), then `BalanceView`, then `SendTransaction`, then `SwapView`.
Each one is done when it renders all its states on device and its skill's
inline pattern matches the shipped code.

## The copy command (spec)

Each showcase card shows one copyable command. Exact shape:

```text
Pin talon-mobile-ui@1.0.0 and add the ConnectButton to the home screen,
themed to this app.
```

Component name, plugin name, exact version, and target screen are all
present so the command works with zero follow-up questions. What the
agent does with it, in order:

1. Writes the pin to the project's `.talon/plugins.json` when missing.
2. Shows manifest, license, and version for first-use approval, then waits.
3. Loads the one matching skill through `read_skill`.
4. Fetches the full starting point through `read_plugin_file`, one file.
5. Adapts it to the target screen and theme, writes it to the workspace,
   and runs the normal verify ladder.

If the pin already exists and is approved, step 2 is skipped and the
command runs end to end with no human in the loop.

## Showcase pages (spec)

- `/plugins`: every registry plugin as a card (name, one-line job,
  version, license). Data renders from the registry at build time so the
  page can never list a plugin that does not exist.
- `/plugins/[name]`: per-component sections with state screenshots, props
  table, the skill description verbatim (what the agent sees is what the
  user reads), the copy command, and the exact pin string.
- Docs: extend the existing Plugins section in Docs with using (pin,
  command, approval) and creating (manifest, skills, references, hashes,
  the exclusivity rule). No separate docs site.
- Header: one Plugins entry in the shared marketing nav. Users find the
  list, the list points at commands, commands point at the agent.

## Preview strategy

- v1: device screenshots per state plus the code. Honest, cheap, and
  enough to pick a component.
- v2 (only if worth it): interactive web preview via react-native-web,
  and only for components written within its supported subset. Never
  rewrite a component to fit the preview; the device is the truth.

## Human intervention policy

Pins stay files. The file is git-tracked, auditable, works offline, and
the agent can edit it from natural language, which a database row cannot
match without new UI and new code. Minimal human touch is achieved by
letting the agent do the writing and keeping humans only where trust or
money is involved. Humans are required for exactly four things:

1. First-use approval of a new plugin (manifest, license, version).
2. State-changing mainnet calls (per-send confirmation in the UI).
3. Adopting a version update (pins never move silently).
4. Publishing a pack or version to the directory (your maintainer role).

Everything else, pins, fetches, adaptation, verification, runs without
human input. The click-to-approve card (adopt/keep buttons with a
version diff in the build output) is deferred until updates become
frequent enough that hand-editing the pin file feels slow.

## Phases

1. Finish `ConnectButton` on device to the build standard above.
2. Promote to `references/`, re-sign manifest, publish directory.
3. Showcase routes plus header entry, with the one finished component.
4. `BalanceView`, then `SendTransaction`, then `SwapView`, same loop.
5. Creating/using docs in the Docs page.
6. `panta-prediction-market-api` only after the showcase carries the UI set.

## The next pack

Plugins are building blocks, not UI. Screen components always extend
talon-mobile-ui; a new plugin means a new capability (an API, a service,
a protocol). The second plugin is `panta-prediction-market-api`: the
Panta prediction-market API patterns for Solana Mobile. Same pack shape
(manifest, skills, references, registry row), same trust rules, and its
skills stay mutually exclusive with each other exactly like the UI skills
do. Build it only after talon-mobile-ui carries its first three
components and the showcase is live.
