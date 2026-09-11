---
name: wallet-mwa
description: Connect a wallet and sign or send transactions with Mobile Wallet Adapter via the wallet-ui kit. Load this whenever the task touches wallet connection, signing, sending transactions, or reading the connected account.
version: "@wallet-ui/react-native-kit@4.x, @solana/kit@6.x, @solana-mobile/mobile-wallet-adapter-protocol-kit@0.4.x"
autoAttach: true
triggers: [import:mobile-wallet-adapter, import:transact, import:useMobileWallet, import:wallet-ui, pkg:@solana-mobile, pkg:@wallet-ui, pkg:@solana/kit]
---

# Wallet connection & signing via MWA (kit)

This project talks to wallets **only** through Mobile Wallet Adapter, via the
`@wallet-ui/react-native-kit` hook. There is no browser wallet, no
`@solana/wallet-adapter-react`, no injected provider, no `@solana/web3.js`
(`Connection`, `PublicKey`, `Transaction` are legacy — do not import them).

**Pinned to this project:** `@wallet-ui/react-native-kit` (4.x),
`@solana/kit` (6.x), `@solana-mobile/mobile-wallet-adapter-protocol-kit`
(0.4.x) — all already installed. Working examples live in
`features/account/` (`account-feature-connect`, `-sign-transaction`,
`-get-balance`): read the installed feature before inventing your own flow.

## Confirm the exact API before you rely on it

The shapes below are correct for the pinned line, but signatures shift between
minor versions. Before writing signing code, read the installed types to
confirm method names and argument shapes:

- `node_modules/@wallet-ui/react-native-kit/dist/types/index.d.ts`
- `node_modules/@solana/kit/dist/types/index.d.ts`

If a package uses a different layout, read its `types` (or `typings`) field
in `package.json` — that field is the entry point, not any conventional path.

Trust the installed `.d.ts` over memory. If what you find differs from this
skill, follow the installed types and keep going. Better yet, call
`inspect_package` with the package name — one call returns the full surface.

## The one pattern that matters

Everything goes through the `useMobileWallet()` hook. Connect once, send
instructions; the hook owns the MWA session lifecycle.

```tsx
import { useMobileWallet } from '@wallet-ui/react-native-kit';
import { getAddMemoInstruction } from '@solana-program/memo';
import { Address, Instruction } from '@solana/kit';

const { account, connect, sendTransactions } = useMobileWallet();

// Connect: hook handles authorize + session. account holds the address.
await connect();

// Send: build kit Instructions, hand them over. No manual transact,
// no base64 juggling, no PublicKey — addresses are kit `Address` strings.
const instructions: Instruction[] = [
  getAddMemoInstruction({ memo: 'hello talon' }),
];
const signature = await sendTransactions(instructions);
```

## Rules that keep this correct

- **Use the hook, not raw transact.** Do not import
  `@solana-mobile/mobile-wallet-adapter-protocol` (or `-kit`) directly for
  signing when `@wallet-ui/react-native-kit` is installed; the hook owns the
  session and the account state your UI reads.
- **Addresses are strings.** Kit `Address` is a base58 string type — there is
  no base64 decode step, no `PublicKey` construction. If an API hands you
  raw bytes, convert once at the boundary with kit codecs, not inline.
- **Build instructions with program clients.** `@solana-program/memo`,
  `@solana-program/system`, `@solana-program/token` — never hand-roll
  instruction bytes.
- **Rejection is normal.** The user can dismiss the wallet sheet. That throws —
  catch it, treat it as "cancelled", and return the UI to its resting state.
  It is not an error to surface as a crash.
- **Show before you sign.** Render what the user is about to approve (amount,
  recipient, action) before the sheet opens.
- **Read balances with kit RPC, not MWA.** MWA is for signing; balances and
  account data come from `@solana/kit` RPC (`rpc.getBalance(address).send()`).
  See `use-account-get-balance.tsx` in the installed features.

## What not to do

- Do not persist keys or seeds — there are none to persist.
- Do not import `@solana/web3.js` (`Connection`, `Keypair`, `Transaction`,
  `PublicKey`) — legacy v1 API, not installed. The kit-web3-interop reference
  covers migration only if you inherit v1 code.
- Do not open raw `transact()` sessions alongside the hook; one session owner
  per screen.
