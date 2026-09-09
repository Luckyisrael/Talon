---
name: seed-vault
description: How Seed Vault (the TEE-backed key store on the Seeker) constrains what an app is allowed to do with keys. Load when the build touches signing, custody, or anything key-shaped.
version: "Seed Vault 1.x (Seeker 1); MWA via @wallet-ui/react-native-kit 4.x"
autoAttach: false
triggers: [import:@solana-mobile/mobile-wallet-adapter, pkg:@solana-mobile, content:tweetnacl, content:ed25519, content:Keypair]
---

# Seed Vault — what your app is allowed to do

Seed Vault is a **TEE-backed key custody layer** in the Seeker. It is
hosted by the system, accessed only through Mobile Wallet Adapter (MWA),
and **your app does not see key material** — ever. This skill is the
"keys never leave the device" side, written as a separate document from
`wallet-mwa` (which is the "how to talk to MWA" side) so it is only
loaded when the question is about custody, not connection.

## The hard rules

- **Never** import, install, or use `tweetnacl`, `@noble/ed25519`,
  `ed25519-hd-key`, `bip39`, or any key-derivation library in the app
  bundle. The app does not need them; the wallet does the signing.
- **Never** write a `KeyPairSigner` from raw bytes, `createKeyPairSignerFromBytes(...)`
  with app-held material, or any `fromSecretKey`/`fromSeed` equivalent. There
  is no secret key in your app.
- **Never** store a base58 / base64 / hex private key, mnemonic, or seed
  phrase in `AsyncStorage`, `expo-secure-store`, `MMKV`, or any other
  persistence layer. If your code is doing this, the design is wrong.
- **Never** log, send over the network, or display a key. Even for
  debugging. Especially for debugging.
- **Never** bundle a `.env` with a `SECRET_KEY` or `PRIVATE_KEY`. A
  repo-wide grep for those strings should be empty.

## What the app is allowed to do

- Ask the wallet to **authorize** a session. The wallet returns the
  account `Address` (a base58 string) and keeps the session.
- Ask the wallet to **sign** a transaction or message. The wallet
  shows the user what is being signed; the user approves or cancels.
- Ask the wallet to **sign and send** a transaction. The wallet
  returns a signature; the app then waits for confirmation via kit RPC
  (`rpc.confirmTransaction(...)` / signature subscriptions).
- Read **public on-chain state** for the user's address (balance,
  tokens, history) via a public RPC. No key material involved.
- Display what is being signed **before** the wallet sheet opens.
  This is both a UX rule and a Seed Vault trust rule: the user is
  supposed to be able to see and reject what is being asked of their
  key.

## What the app must do

- **Use the address as given.** The hook hands you a kit `Address`
  (base58 string). Do not decode, re-derive, or construct keys from it —
  treating it as anything but an opaque address produces wrong keys and
  confusing on-chain errors.
- **Catch the user cancelling the sheet.** The wallet throws when the
  user dismisses. That is a normal outcome, not a crash.
- **Connect once, reuse the session.** A second session should reuse the
  hook's account, not re-prompt, so the user is not asked every time.
- **Show before signing.** Render the amount, recipient, and action
  before opening the wallet. The user can only approve what they see.

## What the model often gets wrong

- "I need a keypair to sign." You don't. MWA does.
- "I should generate a key on first launch and save it." No. The user
  already has a wallet, and the wallet holds the key. The app just
  talks to the wallet.
- "Let me add a token program call to mint a token." MWA + a *program*
  mints a token; the app signs. The key still lives in the wallet.
- "The mock wallet returns the same address every time, so the test
  is fine." The mock is for the dev loop. On real hardware the user
  chooses which wallet to connect, and the address differs from the
  mock's.

## Verifying your build against these rules

- `grep -r "Keypair.fromSecretKey" app/ components/ lib/` → empty.
- `grep -r "process.env.SECRET_KEY\\|process.env.PRIVATE_KEY" .` → empty.
- `grep -r "tweetnacl\\|ed25519" package.json` → empty.
- No `CryptoKey` / `KeyPairSigner` is ever constructed from app-held bytes.
  Signers come from the wallet session, never from code.
