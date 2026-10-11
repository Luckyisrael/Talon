// L63/FM-WALLa — the connect flow: one wallet to one account, linked to
// the session that stands in front of it.
//
// The handshake is honest about where each act can legally happen:
//
//   - BEFORE sign-in (onboarding, `beginWalletLink`): the wallet signs the
//     PUBLIC challenge and the proof is held in memory + storage (FM-WALLc(v)
//     — a reload between handshake and callback must not lose it) — a
//     signature cannot outlive its challenge (single-use, swept at 10
//     minutes), and it has no session to land on yet.
//   - AFTER sign-in (`takePendingWalletProof` at the OAuth callback, or
//     `linkWalletNow` from a signed-in screen): verify rides the session
//     token, so the backend binds the wallet to the SIGNED-IN account —
//     never a fresh one (P17's empty-email upsert would create).
//
// Two wire laws are pinned here rather than at call sites:
//
//   - MWA's signed payload is the message with the 64-byte ed25519
//     signature APPENDED (spec.md sign_messages: "The signatures should
//     be appended to the message"), and a wallet answering with the BARE
//     64-byte signature is the observed interop variant (Solflare, live
//     2026-10-11: consent rendered, signature returned, length exactly
//     64). Both shapes reduce to the same signature — split here, once.
//   - signatures ride base64 StdEncoding (the backend decodes exactly
//     that), from a pure encoder: neither btoa nor Buffer exists reliably
//     on Hermes.

import AsyncStorage from '@react-native-async-storage/async-storage'
import { walletAuthApi, walletLinkApi, type WalletProof } from '@/lib/api'

// Structural, not kit-shaped: the flow needs an address and a signature —
// nothing else about the wallet matters to it (and it stays testable
// without importing the native kit). The two optional handles are the
// documented recovery surface of L78: `account` says an authorization is
// held, `disconnect` clears it — both come straight off `useMobileWallet`.
export type WalletConnectors = {
  connect: () => Promise<{ address: string }>
  signMessages: (message: Uint8Array) => Promise<Uint8Array>
  account?: { address: string } | undefined
  disconnect: () => Promise<void>
}

export type { WalletProof }

// Two real wallet shapes, one signature: message ‖ signature(64) per
// spec (Phantom), or the bare 64-byte signature (Solflare, observed
// live 2026-10-11). Anything under 64 bytes carries no signature —
// refused with the actual length on the record rather than sliced
// into something that happens to parse.
export function splitSignedPayload(signed: Uint8Array): {
  message: Uint8Array
  signature: Uint8Array
} {
  if (signed.length === 64) {
    return { message: new Uint8Array(0), signature: signed }
  }
  if (signed.length < 64) {
    throw new Error(
      `signed payload too short to carry a 64-byte signature (${signed.length} bytes)`,
    )
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
//
// FM-WALLc(v)/L73(d): it outlives the PROCESS too. A Metro reload or JS
// teardown between the signature and the OAuth callback used to erase
// `pending` silently, so the wallet never linked and nothing said so.
// Storage is the floor; module memory stays the fast path. A storage
// failure degrades to exactly the old in-memory behavior — never worse.
const PENDING_PROOF_KEY = 'talon.pending-wallet-proof'
let pending: WalletProof | null = null

async function storePending(proof: WalletProof): Promise<void> {
  try {
    await AsyncStorage.setItem(PENDING_PROOF_KEY, JSON.stringify(proof))
  } catch {
    // Degrade to the old behavior; the in-memory copy still completes
    // the flow as long as no reload happens.
  }
}

// Read-and-consume, once: the bytes are cleared before parsing, so a
// corrupt payload cannot trap the slot either.
async function takeStoredProof(): Promise<WalletProof | null> {
  let raw: string | null = null
  try {
    raw = await AsyncStorage.getItem(PENDING_PROOF_KEY)
  } catch {
    return null
  }
  if (raw === null) return null
  void AsyncStorage.removeItem(PENDING_PROOF_KEY).catch(() => {})
  try {
    return JSON.parse(raw) as WalletProof
  } catch {
    return null
  }
}

async function signForChallenge(
  wallet: WalletConnectors,
  address: string,
  challenge: { nonce: string; message: string },
): Promise<WalletProof> {
  const payload = new TextEncoder().encode(challenge.message)
  let signed: Uint8Array
  try {
    signed = await wallet.signMessages(payload)
  } catch (err) {
    // L78/FM-WALLd(v): the SAME rot reaches the sign step — the kit's
    // signMessages re-authorizes inside its own session, and Phantom
    // 26.32 refuses that reauthorize even with a token minted seconds
    // earlier (observed live 2026-10-11 01:16: the authorize succeeded,
    // the signMessages reauthorize died `-1` with no UI, and no proof
    // ever formed). Recover on EXACTLY one class: the wallet's own
    // authorization failure (-1) while an authorization is still held.
    // The human's decline (-3) and every network error surface
    // untouched — a refusal is always the human's answer (L73(b)).
    const code = (err as { code?: number } | null)?.code
    console.warn(`[wallet] sign refused (code: ${code}, authorization held: ${wallet.account != null}): ${String(err)}`)
    if (code !== ERROR_AUTHORIZATION_FAILED || !wallet.account) throw err
    if (!(await clearHeldAuthorization(wallet))) throw err
    // ONE retry: a fresh session, a fresh authorize (the human taps
    // consent once more), then the signature.
    signed = await wallet.signMessages(payload)
  }
  const { signature } = splitSignedPayload(signed)
  return { wallet: address, nonce: challenge.nonce, signature: toBase64(signature) }
}

// L78/FM-WALLd(v) — the invisible refusal, and the two traps behind
// recovering it. A held authorization whose token the wallet no longer
// honors dies with `-1/authorization request failed` and no consent UI
// (observed live on Phantom 26.32, repeatedly, 2026-10-10/11), and two
// things must be true for a recovery to actually work:
//
//   1. THE CLEAR MUST REACH THE RETRY. The kit's `disconnect` clears
//      its store locally (`persist(null)`), but `connect` CLOSES OVER
//      the token of the render that created it — a retry that reuses
//      the pre-clear object re-sends the same corpse (observed live,
//      2026-10-11 00:32: the retry's `reauthorize` carried the
//      IDENTICAL dead token, refused again in 80 ms). So the screens
//      bind through `latestWallet`: every call reads the CURRENT hook
//      object. The wait below is not a guess at render latency — it
//      OBSERVES the clear: `account` disappearing from the live object
//      means the post-clear render has committed, and that render is
//      the one whose `connect` closure carries no token. If it never
//      disappears, the original error is re-thrown instead of firing a
//      retry that is certain to re-send the corpse.
//   2. ONLY a held authorization justifies a retry. With nothing held,
//      the refusal is the human's own answer to a fresh prompt —
//      surfaced untouched, never re-prompted (L73(b): a refusal stays
//      put and says why).
export function latestWallet(ref: { current: WalletConnectors }): WalletConnectors {
  return {
    connect: () => ref.current.connect(),
    signMessages: (message) => ref.current.signMessages(message),
    get account() {
      return ref.current.account
    },
    disconnect: () => ref.current.disconnect(),
  }
}

// The protocol's own error code for "this authorization is no good" —
// the ONLY class the sign step recovers from (see signForChallenge).
const ERROR_AUTHORIZATION_FAILED = -1
const RECOVERY_RENDER_WAIT_MS = 2000

// The clear, once, shared by both recovery paths (trap 1 above): the
// kit's local `persist(null)` via `disconnect`, then the wait for the
// clear to LAND as a re-render — observed as `account` disappearing
// from the live object, never as a slept-for duration. Resolves true
// when the post-clear render committed: the observation that makes a
// retry sound, because that render is the one whose closures carry no
// token. False when it never did, because a retry built on the
// pre-clear closures is certain to repeat the refusal.
async function clearHeldAuthorization(wallet: WalletConnectors): Promise<boolean> {
  await wallet.disconnect().catch(() => {})
  const deadline = Date.now() + RECOVERY_RENDER_WAIT_MS
  while (wallet.account != null && Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, 16))
  }
  return wallet.account == null
}

async function connectWithRecovery(wallet: WalletConnectors): Promise<{ address: string }> {
  try {
    return await wallet.connect()
  } catch (err) {
    // The decision path, on the record: a refusal while an authorization
    // is held is the wallet's state rotting; without one, it is the
    // human's own answer (L73(b)).
    console.warn(`[wallet] connect refused (authorization held: ${wallet.account != null}): ${String(err)}`)
    if (!wallet.account) throw err
    if (!(await clearHeldAuthorization(wallet))) throw err
    // ONE retry: a fresh authorize, with no token left to rot.
    return await wallet.connect()
  }
}

// Pre-auth: connect (the address must be known before the challenge can
// bind it), sign the PUBLIC sign-in challenge, hold the proof for the
// callback. Throws whatever the wallet or the network throws — the
// caller decides whether an abandoned handshake still continues to
// sign-in.
export async function beginWalletLink(wallet: WalletConnectors): Promise<WalletProof> {
  const account = await connectWithRecovery(wallet)
  const challenge = await walletAuthApi.challenge(account.address)
  const proof = await signForChallenge(wallet, account.address, challenge)
  pending = proof
  await storePending(proof)
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
// FM-WALLc(v): after a reload the memory copy is gone, so a proof found
// in neither place is the only real "nothing" — the stored bytes answer
// for the reloaded process, still consumed exactly once across both.
export async function completePendingWalletLink(): Promise<boolean> {
  let proof = takePendingWalletProof()
  if (proof) {
    // Memory carried it — the disk copy is consumed with it.
    void AsyncStorage.removeItem(PENDING_PROOF_KEY).catch(() => {})
  } else {
    proof = await takeStoredProof()
  }
  if (!proof) return false
  await walletLinkApi.verify(proof)
  return true
}

// The signed-in path (drawer / any live screen): challenge minted against
// THIS session, signed, verified — all in one go, nothing held. Same
// documented recovery as the onboarding handshake (L78/FM-WALLd(v)).
export async function linkWalletNow(wallet: WalletConnectors): Promise<void> {
  const account = await connectWithRecovery(wallet)
  const challenge = await walletLinkApi.challenge(account.address)
  const proof = await signForChallenge(wallet, account.address, challenge)
  await walletLinkApi.verify(proof)
}
