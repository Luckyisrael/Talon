---
name: scl
description: The Solana Common Library (SCL) — RN-friendly primitives Solana Mobile ships for token tables, account derivation, and stablecoin helpers. Load when the build needs display helpers, formatting, or SSS/SPL lookups that the SCL already covers.
version: "SCL 0.5.x (verify against the installed package's package.json)"
autoAttach: false
triggers: [import:@solana-mobile, pkg:@solana-mobile, content:PublicKey, content:Connection, content:getBalance, content:formatAmount, content:mintAddress]
---

# Solana Common Library (SCL) — reach for it before writing your own

The SCL is a set of React Native utilities and Solana primitives that
Solana Mobile ships to keep Solana apps consistent. Before you write a
balance formatter, an account derivation helper, a token table, or a
SSS (Solana Stablecoin Standard) helper, **check whether the SCL
already has it**. The version line in the front matter pins the API.

## What the SCL typically provides

- **Token metadata and table helpers.** Mint, decimals, symbol, logo
  URI, name. Formatters that handle both `6` and `9` decimal tokens
  without surprising rounding.
- **Account derivation.** ATA (Associated Token Account) derivation
  in a way that does not require a `Connection` to be open.
- **Public RPC helpers.** A swappable RPC client with sensible
  timeouts and a fallback strategy for mobile.
- **Display formatters.** `lamportsToSol`, `formatTokenAmount`,
  `shortenAddress` with mobile-appropriate truncation (the
  `g4y...b7x` shape, not the entire base58 string).
- **SSS helpers** (Solana Stablecoin Standard). Detect whether a
  mint is SSS-compliant and read its metadata.

## Before you import

The SCL package name and shape change between SDK versions. **Read
the installed `node_modules/@solana-mobile/*` first.** If what is
installed is older than the version in the front matter, follow the
installed types and re-verify.

## When to write your own

- The SCL doesn't cover your exact display shape (it shouldn't be a
  battle with a helper that mostly does the right thing).
- You are operating on an internal cluster with non-standard mint
  layouts.
- The SCL helper requires a feature you have not installed.

Even then, isolate your custom helper in one place (`lib/solana/`)
and re-export a small surface, so the rest of the app does not
depend on internals.

## The anti-patterns

- Re-implementing `lamportsToSol` with `lamports / 1e9`. This loses
  precision for very small balances and overflows for very large.
  Use a big-decimal library, or the SCL's helper.
- Constructing keys from wallet output. Kit `Address` values are opaque
  strings — never decode, re-derive, or build keypairs from them.
  See `seed-vault` for the custody rules and `wallet-mwa` for the pattern.
- Storing a list of "known mints" in the app. The SCL's metadata
  cache, or a public registry, is the source of truth.
- Hardcoding RPC URLs. The SCL's RPC client, or your own
  `lib/solana/rpc.ts`, is the single place to change cluster.
