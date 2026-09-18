# TEMPLATE.md — what this project ships, and what to keep

The template is a **starting point, not a scaffold**. It exists so every app
does not begin at an empty `package.json` — the design system, the wallet
stack, and the build config are already correct, and the MWA capability demos
show how the platform's APIs are meant to be called.

**Your app is not obligated to any of the demo screens.** Read this before you
plan, decide what survives, and say the decision in the plan's `structure`
field. A plan that writes three new screens and leaves the demo home behind
produces exactly the broken app you would expect: two entry points, no
navigation, and a home screen that renders capability buttons no user asked
for.

## System — keep these

These are the app's foundation. They are not demo code; removing them breaks
the build or the design system.

| Path | What it is |
|---|---|
| `index.js` | The native entry. Imports `expo-router/entry`. **Never delete** — the native build refuses to compile without it. |
| `app/_layout.tsx` | The root layout: providers and the root `<Stack>`. Every route is a child of this. |
| `app.json` | Expo config: name, orientation, icons, the Android status bar. |
| `constants/theme.ts` | The design tokens — every colour the app uses. One accent, one grey family, one radius scale. |
| `constants/app-styles.ts` | The styles built from those tokens (`createStyles(t)`). Screens call this; they do not invent a second system. |
| `constants/app-config.ts` | App identity (name, uri) the wallet hand-off reads. |
| `components/action-button.tsx` | The primary button. Use it instead of `<Button>` — the platform control's colour and hit area are not ours. |
| `components/app-providers.tsx` | The providers the root layout mounts (theme, network, wallet). |
| `features/network/network-provider.tsx` | The RPC cluster context. Real wallet work needs a cluster; keep it. |
| `features/network/use-network.ts` | The hook over that context. |
| `assets/images/*` | Icons and splash. Replace the art, keep the paths. |

## Demo — prune when your app outgrows it

`app/index.tsx` and the `features/*/account-feature-*` /
`network-feature-get-*` files are **capability demos**: they exist to show a
developer "this is how you call `getBalance`, this is how you sign a message."
They are not product screens. A user-facing app replaces them.

| Path | What it demonstrates |
|---|---|
| `app/index.tsx` | The demo home — renders `AccountFeatureIndex` and `NetworkFeatureIndex`. **Your app's real entry replaces this.** Do not keep it alongside a new home screen. |
| `features/account/account-feature-connect.tsx` | The connect call — the right pattern to copy into a real Auth screen, then remove. |
| `features/account/account-feature-get-balance.tsx` | The balance read — copy the call, remove the demo. |
| `features/account/account-feature-sign-in.tsx` | Sign-in / authorize. |
| `features/account/account-feature-sign-message.tsx` | Message signing. |
| `features/account/account-feature-sign-transaction.tsx` | Transaction signing. |
| `features/account/account-feature-disconnect.tsx` | Disconnect. |
| `features/account/account-feature-index.tsx` | The demo index that mounts all of the above. |
| `features/network/network-feature-get-version.tsx` | `getVersion` — RPC plumbing demo. |
| `features/network/network-feature-get-genesis-hash.tsx` | `getGenesisHash` — same. |
| `features/network/network-feature-index.tsx` | The demo index for the network features. |
| `features/network/network-ui-select.tsx` | The devnet/mainnet switcher. **Keep this if your app shows a network badge** — it is a real control, not a demo. |

## How to prune

1. **Copy the call pattern before you delete the demo.** `account-feature-get-balance.tsx` is the reference for how your WalletFeed reads a balance. Read it, write your screen against the same API, then prune the demo.
2. **Prune the index last.** `app/index.tsx` imports both feature indexes; nothing else does. Remove the features, then the indexes, then the demo home.
3. **Typecheck after pruning.** A removed file that something still imports is a broken build, not a clean app. `run_checks` after the prune, before the next unit of work.
4. **Say it in the plan.** The plan's `structure.prune` list is how the person knows what you removed and why. An unexplained deletion looks like an accident.

## Folder conventions

- `app/` — routes only. One file per screen; file-based navigation via expo-router.
- `features/<name>/` — one folder per capability (wallet, feed, onboarding…). The screen, its hooks, and its sub-components live together.
- `components/` — shared UI used by more than one feature.
- `constants/` — tokens, config, styles. No logic.
- `assets/` — images and fonts only.
- Keep authored files focused: anything reused, stateful, or laid out by a
  screen earns its own file. Screens compose; they don't accumulate.

## Wallet stack

The only wallet stack is Mobile Wallet Adapter via `@wallet-ui/react-native-kit`
(`useMobileWallet()`: connect, `sendTransactions()` with `@solana/kit`
program-client instructions). Never `@solana/web3.js` (`Connection`,
`PublicKey`, `Transaction`) or `@solana-mobile/mobile-wallet-adapter-protocol-web3js` —
legacy, not installed. Browser wallet packages cannot run on-device —
never add them. Keys stay in the wallet app; this app never sees a seed phrase.
