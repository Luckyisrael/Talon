---
name: wallet-mwa
description: Connect a wallet and sign or send transactions with Mobile Wallet Adapter. Load this whenever the task touches wallet connection, signing, sending transactions, or reading the connected account.
version: "@solana-mobile/mobile-wallet-adapter-protocol-web3js@2.1.x, @solana/web3.js@1.98.x"
autoAttach: true
triggers: [import:mobile-wallet-adapter, import:transact, pkg:@solana-mobile]
---

# Wallet connection & signing via MWA

This project talks to wallets **only** through Mobile Wallet Adapter. There is no
browser wallet, no `@solana/wallet-adapter-react`, no injected provider. Every
wallet interaction is one `transact()` session with the installed wallet app.

**Pinned to this project:** `@solana-mobile/mobile-wallet-adapter-protocol-web3js`
(2.1.x) and `@solana/web3.js` (1.98.x) — both already installed. This package pairs
MWA with web3.js `Transaction`/`VersionedTransaction` objects directly.

## Confirm the exact API before you rely on it

The shapes below are correct for the 2.1.x line, but signatures shift between minor
versions. Before writing signing code, read the installed types to confirm method
names and argument shapes:

- `node_modules/@solana-mobile/mobile-wallet-adapter-protocol-web3js/lib/typescript/`

Trust the installed `.d.ts` over memory. If what you find differs from this skill,
follow the installed types and keep going.

## The one pattern that matters

Everything happens inside a `transact` callback. The session is only alive inside it.

```ts
import { transact } from '@solana-mobile/mobile-wallet-adapter-protocol-web3js';
import { PublicKey, Transaction } from '@solana/web3.js';

const APP_IDENTITY = {
  name: 'Seeker App',
  uri: 'https://seeker.app',
  icon: 'favicon.ico', // relative to uri
};

// Connect: authorize once, keep the returned authToken to reauthorize later.
const { accounts, auth_token } = await transact(async (wallet) => {
  return await wallet.authorize({
    chain: 'solana:devnet',      // 'solana:mainnet' for production
    identity: APP_IDENTITY,
  });
});
// accounts[0].address is a base64-encoded address, NOT a base58 string.
```

## Rules that keep this correct

- **Reuse the auth token.** Store `auth_token` from the first `authorize`, then call
  `wallet.reauthorize({ auth_token, identity })` on later sessions so the user is not
  re-prompted every time.
- **Convert the address.** MWA returns `accounts[0].address` as base64. Decode it to
  bytes and build a `PublicKey` — do not treat it as a base58 string.
- **Prefer `signAndSendTransactions`.** For sending, use
  `wallet.signAndSendTransactions({ transactions: [...] })`. `signTransactions` is
  deprecated in MWA 2.x — only sign-without-send when you have a specific reason
  (e.g. handing the signed tx to a relayer).
- **Rejection is normal.** The user can dismiss the wallet sheet. That throws — catch
  it, treat it as "cancelled", and return the UI to its resting state. It is not an
  error to surface as a crash.
- **Show before you sign.** Render what the user is about to approve (amount,
  recipient, action) before the sheet opens.
- **Fetch a blockhash first.** Build transactions with a recent blockhash from your
  `Connection` before entering `transact` where possible; keep the callback short.

## What not to do

- Do not persist keys or seeds — there are none to persist.
- Do not open a second `transact` inside another; do one session at a time.
- Do not import `@solana-mobile/mobile-wallet-adapter-protocol` directly for signing
  when the `-web3js` variant is installed; the web3js wrapper is what handles
  `Transaction` objects for you.
