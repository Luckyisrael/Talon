// L63/FM-WALLa — the connect flow: one wallet to one account, linked to
// the session that stands in front of it.
//
// The handshake is honest about where each act can legally happen:
//
//   - BEFORE sign-in (onboarding, `beginWalletLink`): the wallet signs the
//     PUBLIC challenge and the proof is held in memory — a signature cannot
//     outlive its challenge (single-use, swept at 10 minutes), and it has
//     no session to land on yet.
//   - AFTER sign-in (`takePendingWalletProof` at the OAuth callback, or
//     `linkWalletNow` from a signed-in screen): verify rides the session
//     token, so the backend binds the wallet to the SIGNED-IN account —
//     never a fresh one (P17's empty-email upsert would create).
//
// Two wire laws are pinned here rather than at call sites:
//
//   - MWA's signed payload is the message with the 64-byte ed25519
//     signature APPENDED. The protocol's own signInFallback recovers it as
//     `slice(len - 64)`; split it here, once.
//   - signatures ride base64 StdEncoding (the backend decodes exactly
//     that), from a pure encoder: neither btoa nor Buffer exists reliably
//     on Hermes.

import { walletAuthApi, walletLinkApi, type WalletProof } from '@/lib/api'

// Structural, not kit-shaped: the flow needs an address and a signature —
// nothing else about the wallet matters to it (and it stays testable
// without importing the native kit).
export type WalletConnectors = {
  connect: () => Promise<{ address: string }>
  signMessages: (message: Uint8Array) => Promise<Uint8Array>
}

export type { WalletProof }

// MWA returns message bytes ‖ signature(64). Anything shorter cannot
// carry a signature — refusing beats slicing a wallet's answer into
// something that happens to parse.
export function splitSignedPayload(signed: Uint8Array): {
  message: Uint8Array
  signature: Uint8Array
} {
  if (signed.length <= 64) {
    throw new Error('signed payload too short to carry a 64-byte signature')
  }
  return {
    message: signed.slice(0, signed.length - 64),
    signature: signed.slice(signed.length - 64),
  }
}

// Pure RFC 4648 base64 — no globals, no polyfills, no environment drift.
export function toBase64(bytes: Uint8Array): string {
  const abc = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/'
  let out = ''
  for (let i = 0; i < bytes.length; i += 3) {
    const b0 = bytes[i]
    const b1 = i + 1 < bytes.length ? bytes[i + 1] : 0
    const b2 = i + 2 < bytes.length ? bytes[i + 2] : 0
    out += abc[b0 >> 2]
    out += abc[((b0 & 3) << 4) | (b1 >> 4)]
    out += i + 1 < bytes.length ? abc[((b1 & 15) << 2) | (b2 >> 6)] : '='
    out += i + 2 < bytes.length ? abc[b2 & 63] : '='
  }
  return out
}

// The proof held between the onboarding handshake and the sign-in it
// attaches to. Consumed exactly once — one proof, one nonce, one attempt,
// mirroring the server's single-use challenge.
let pending: WalletProof | null = null

async function signForChallenge(
  wallet: WalletConnectors,
  address: string,
  challenge: { nonce: string; message: string },
): Promise<WalletProof> {
  const signed = await wallet.signMessages(new TextEncoder().encode(challenge.message))
  const { signature } = splitSignedPayload(signed)
  return { wallet: address, nonce: challenge.nonce, signature: toBase64(signature) }
}

// Pre-auth: connect (the address must be known before the challenge can
// bind it), sign the PUBLIC sign-in challenge, hold the proof for the
// callback. Throws whatever the wallet or the network throws — the caller
// decides whether an abandoned handshake still continues to sign-in.
export async function beginWalletLink(wallet: WalletConnectors): Promise<WalletProof> {
  const account = await wallet.connect()
  const challenge = await walletAuthApi.challenge(account.address)
  const proof = await signForChallenge(wallet, account.address, challenge)
  pending = proof
  return proof
}

// The callback's consume: one shot, then empty — a proof that failed to
// verify must not be retried by a screen that never saw it.
export function takePendingWalletProof(): WalletProof | null {
  const proof = pending
  pending = null
  return proof
}

// What the OAuth callback runs: consume the held proof and attach it to
// the session just minted. Resolves false when there is nothing to
// complete (skipped handshake, already consumed) — the caller treats both
// falses the same way: continue unlinked, let the reminder catch it.
export async function completePendingWalletLink(): Promise<boolean> {
  const proof = takePendingWalletProof()
  if (!proof) return false
  await walletLinkApi.verify(proof)
  return true
}

// The signed-in path (drawer / any live screen): challenge minted against
// THIS session, signed, verified — all in one go, nothing held.
export async function linkWalletNow(wallet: WalletConnectors): Promise<void> {
  const account = await wallet.connect()
  const challenge = await walletLinkApi.challenge(account.address)
  const proof = await signForChallenge(wallet, account.address, challenge)
  await walletLinkApi.verify(proof)
}
