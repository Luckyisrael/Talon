// FM-WALLa — the wire laws of the connect flow, pinned from the client
// side (L63's server pair lives in walletauth_test.go):
//
//   - MWA's signed payload is the message with the 64-byte ed25519
//     signature APPENDED — the protocol's own signInFallback slices
//     len-64 to recover it; split here, once, never at a call site.
//   - the pre-auth proof is consumed exactly once: it stands in for a
//     single-use server challenge, and two takes of one proof would be
//     two attempts at one nonce.
//   - signatures ride base64 (the backend decodes StdEncoding), from a
//     pure encoder — neither btoa nor Buffer exists reliably on Hermes.
//
// Red before L63: lib/walletlink.ts does not exist — module not found.

import {
  splitSignedPayload,
  beginWalletLink,
  completePendingWalletLink,
  takePendingWalletProof,
  toBase64,
} from '@/lib/walletlink'

function bytes(...values: number[]): Uint8Array {
  return Uint8Array.from(values)
}

it('L63 — splits message from the appended 64-byte signature', () => {
  const message = bytes(1, 2, 3, 4)
  const signature = bytes(...new Array(64).fill(9))
  const signed = new Uint8Array([...message, ...signature])

  const out = splitSignedPayload(signed)

  expect(Array.from(out.message)).toEqual([1, 2, 3, 4])
  expect(out.signature.length).toBe(64)
  expect(Array.from(out.signature)).toEqual(new Array(64).fill(9))
})

it('L63 — a payload too short to carry a signature is refused, not sliced', () => {
  expect(() => splitSignedPayload(bytes(1, 2, 3))).toThrow()
})

it('L63 — base64 without btoa or Buffer (pure encoder)', () => {
  // "Talon" → VGFsb24= — the StdEncoding the backend decodes.
  const text = Uint8Array.from('Talon'.split('').map((c) => c.charCodeAt(0)))
  expect(toBase64(text)).toBe('VGFsb24=')
  expect(toBase64(bytes(0, 0, 0))).toBe('AAAA')
  expect(toBase64(bytes(0xff))).toBe('/w==')
})

it('L63 — the pre-auth proof is held until sign-in consumes it, once', async () => {
  type FetchResponse = {
    ok: boolean
    status: number
    json: () => Promise<unknown>
    text: () => Promise<string>
  }
  const challenge = { nonce: 'n-1', message: 'Talon sign-in\nwallet: W\nnonce: n-1' }
  global.fetch = jest.fn(async (url: unknown, init?: { body?: string }): Promise<FetchResponse> => {
    const path = String(url)
    if (path.endsWith('/v1/auth/wallet/challenge')) {
      return { ok: true, status: 200, json: async () => challenge, text: async () => JSON.stringify(challenge) }
    }
    if (path.endsWith('/v1/me/wallet/verify')) {
      // The proof arrives whole: wallet + nonce + base64 signature.
      const body = JSON.parse(String(init?.body))
      expect(body.wallet).toBe('Wa11et')
      expect(body.nonce).toBe('n-1')
      expect(body.signature.length).toBeGreaterThan(0)
      return { ok: true, status: 200, json: async () => ({ linked: true }), text: async () => '{"linked":true}' }
    }
    throw new Error(`unexpected call: ${path}`)
  }) as unknown as typeof fetch

  // The wallet signs the challenge and returns the MWA payload shape:
  // message bytes with the signature appended.
  const signedPayload = (msg: Uint8Array) =>
    new Uint8Array([...msg, ...new Array(64).fill(7)])

  const proof = await beginWalletLink({
    connect: async () => ({ address: 'Wa11et' }),
    signMessages: async (m: Uint8Array) => signedPayload(m),
  })

  expect(proof.wallet).toBe('Wa11et')
  expect(proof.nonce).toBe('n-1')
  // The callback consumes it exactly once: the first completion verifies
  // the whole proof (the fetch mock asserts wallet + nonce arrive whole),
  // the second finds nothing — one proof, one nonce, one attempt.
  await expect(completePendingWalletLink()).resolves.toBe(true)
  await expect(completePendingWalletLink()).resolves.toBe(false)
  expect(takePendingWalletProof()).toBeNull()
})
