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
  latestWallet,
  takePendingWalletProof,
  toBase64,
} from '@/lib/walletlink'

// L73(d)'s backing store. The map lives on globalThis, not in the
// factory closure: jest.resetModules() re-runs this factory for the
// reloaded instance, and a reload simulation must see the bytes the
// pre-reload instance wrote — the disk survives the process, the
// closure does not. One seam, shared by every suite that loads the
// flow module (see test/async-storage-seam.ts).
jest.mock('@react-native-async-storage/async-storage', () => require('@/test/async-storage-seam').asyncStorage())

beforeEach(() => {
  const g = globalThis as { __talonProofStore?: Map<string, string> }
  g.__talonProofStore?.clear()
})

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
    disconnect: async () => {},
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

// FM-WALLc(v) / L73(d) — the proof outlives the process that made it.
//
// Between the onboarding handshake and the OAuth callback stands a Metro
// reload / JS teardown. `pending` used to live in module memory alone, so
// the reloaded callback found nothing and the wallet silently never
// linked. jest.resetModules() IS that reload: same storage, new module.
//
// Red before L73 (observed): the reloaded instance's
// completePendingWalletLink() resolves false — nothing was persisted.
it('L73 — the pending proof survives a reload between handshake and callback (FM-WALLc(v))', async () => {
  type FetchResponse = {
    ok: boolean
    status: number
    json: () => Promise<unknown>
    text: () => Promise<string>
  }
  const challenge = { nonce: 'n-2', message: 'Talon sign-in\nwallet: Wa11et\nnonce: n-2' }
  global.fetch = jest.fn(async (url: unknown, init?: { body?: string }): Promise<FetchResponse> => {
    const path = String(url)
    if (path.endsWith('/v1/auth/wallet/challenge')) {
      return { ok: true, status: 200, json: async () => challenge, text: async () => JSON.stringify(challenge) }
    }
    if (path.endsWith('/v1/me/wallet/verify')) {
      const body = JSON.parse(String(init?.body))
      expect(body.wallet).toBe('Wa11et')
      expect(body.nonce).toBe('n-2')
      expect(body.signature.length).toBeGreaterThan(0)
      return { ok: true, status: 200, json: async () => ({ linked: true }), text: async () => '{"linked":true}' }
    }
    throw new Error(`unexpected call: ${path}`)
  }) as unknown as typeof fetch

  const signedPayload = (msg: Uint8Array) =>
    new Uint8Array([...msg, ...new Array(64).fill(7)])

  // Handshake on the pre-reload instance (the file's top-level import).
  await beginWalletLink({
    connect: async () => ({ address: 'Wa11et' }),
    signMessages: async (m: Uint8Array) => signedPayload(m),
    disconnect: async () => {},
  })

  // The reload: module memory is gone, the disk is not.
  jest.resetModules()
  const reloaded = jest.requireActual('@/lib/walletlink') as typeof import('@/lib/walletlink')

  await expect(reloaded.completePendingWalletLink()).resolves.toBe(true)
  // Consumed exactly once across the reload, as in memory — one proof,
  // one nonce, one attempt.
  await expect(reloaded.completePendingWalletLink()).resolves.toBe(false)
})

// L78/FM-WALLd(v) — the refusal that is nobody's answer, and the wiring
// law behind recovering it.
//
// A held authorization whose token the wallet no longer honors dies
// with `-1/authorization request failed` and no consent UI (observed
// live on Phantom 26.32, repeatedly, 2026-10-10/11), and the kit's
// cached corpse keeps poisoning every next attempt.
//
// The live mechanism, modeled exactly (RED before L78, observed on
// device 2026-10-11 00:32: the recovery's retry `reauthorize` carried
// the IDENTICAL dead token and was refused again in 80 ms): the kit's
// `connect` CLOSES OVER the token of the render that created it, so a
// retry that reuses the pre-clear object cannot succeed no matter how
// many times it runs. Only two things fix it: reading the CURRENT
// hook object through `latestWallet(ref)` (the post-clear render is a
// new object in the ref), and waiting for the clear to LAND as a
// re-render — which the flow observes as `account` disappearing,
// never as a slept-for duration.
it('L78 — the stale-token recovery retries through the post-clear render, never the corpse (FM-WALLd(v))', async () => {
  type FetchResponse = {
    ok: boolean
    status: number
    json: () => Promise<unknown>
    text: () => Promise<string>
  }
  const challenge = { nonce: 'n-3', message: 'Talon sign-in\nwallet: Wa11et\nnonce: n-3' }
  global.fetch = jest.fn(async (url: unknown): Promise<FetchResponse> => {
    const path = String(url)
    if (path.endsWith('/v1/auth/wallet/challenge')) {
      return { ok: true, status: 200, json: async () => challenge, text: async () => JSON.stringify(challenge) }
    }
    throw new Error(`unexpected call: ${path}`)
  }) as unknown as typeof fetch

  const refusal = Object.assign(new Error('authorization request failed'), { code: -1 })
  // The corpse: this render's connect closed over the dead token.
  const staleConnect = jest.fn(async () => {
    throw refusal
  })
  // The post-clear render: connect rebuilt with no token.
  const freshConnect = jest.fn(async () => ({ address: 'Wa11et' }))
  const disconnect = jest.fn(async () => {
    // The store clear becomes a re-render: the ref now holds the fresh
    // object — account gone, closures rebuilt without the token.
    ref.current = {
      connect: freshConnect,
      signMessages: ref.current.signMessages,
      account: undefined,
      disconnect,
    }
  })
  const ref: { current: import('@/lib/walletlink').WalletConnectors } = {
    current: {
      connect: staleConnect,
      signMessages: async (m: Uint8Array) => new Uint8Array([...m, ...new Array(64).fill(7)]),
      account: { address: 'Wa11et' },
      disconnect,
    },
  }

  const proof = await beginWalletLink(latestWallet(ref))

  expect(disconnect).toHaveBeenCalledTimes(1)
  expect(staleConnect).toHaveBeenCalledTimes(1) // the corpse was never re-sent
  expect(freshConnect).toHaveBeenCalledTimes(1) // exactly one fresh authorize
  expect(proof.wallet).toBe('Wa11et')
  expect(proof.nonce).toBe('n-3')
})

// The wait is bounded by observation, not faith: if the clear never
// becomes a re-render, the ORIGINAL error is re-thrown — retrying
// through a corpse closure is certain to be refused again, and a
// second identical refusal teaches nobody anything new.
it('L78 — a clear that never lands re-throws the original refusal instead of re-sending the corpse', async () => {
  const refusal = Object.assign(new Error('authorization request failed'), { code: -1 })
  const connect = jest.fn(async () => {
    throw refusal
  })
  const disconnect = jest.fn(async () => {}) // clears nothing — no re-render lands

  await expect(
    beginWalletLink({
      connect,
      signMessages: async () => new Uint8Array(),
      account: { address: 'Wa11et' },
      disconnect,
    }),
  ).rejects.toBe(refusal)

  expect(connect).toHaveBeenCalledTimes(1) // never retried
  expect(disconnect).toHaveBeenCalledTimes(1)
})

// L78 — the rot reaches the SIGN step too, and it is a different door:
// the kit's signMessages re-authorizes inside its own session, so even
// a token minted seconds earlier can be refused (observed live
// 2026-10-11 01:16: authorize succeeded at 01:15:43, the signMessages
// reauthorize died `-1` at 01:16:12 with no UI, no proof formed).
// Recovery here is narrower than connect's — code -1 AND a held
// authorization — because this step is where the human's own decline
// (-3) lives.
it('L78 — a -1 from the sign step clears the held authorization and re-signs through the post-clear render (FM-WALLd(v))', async () => {
  type FetchResponse = {
    ok: boolean
    status: number
    json: () => Promise<unknown>
    text: () => Promise<string>
  }
  const challenge = { nonce: 'n-4', message: 'Talon sign-in\nwallet: Wa11et\nnonce: n-4' }
  global.fetch = jest.fn(async (url: unknown): Promise<FetchResponse> => {
    const path = String(url)
    if (path.endsWith('/v1/auth/wallet/challenge')) {
      return { ok: true, status: 200, json: async () => challenge, text: async () => JSON.stringify(challenge) }
    }
    throw new Error(`unexpected call: ${path}`)
  }) as unknown as typeof fetch

  const rot = Object.assign(new Error('authorization request failed'), { code: -1 })
  // This render's signMessages re-authorizes with the token connect just
  // minted — and Phantom refuses it.
  const staleSign = jest.fn(async () => {
    throw rot
  })
  // The post-clear render: signMessages rebuilt without a token.
  const freshSign = jest.fn(async (m: Uint8Array) => new Uint8Array([...m, ...new Array(64).fill(7)]))
  const disconnect = jest.fn(async () => {
    // The store clear becomes a re-render: ref holds the fresh object.
    ref.current = {
      connect: ref.current.connect,
      signMessages: freshSign,
      account: undefined,
      disconnect,
    }
  })
  const ref: { current: import('@/lib/walletlink').WalletConnectors } = {
    current: {
      // Connect itself succeeds — this is the 01:15:43 shape.
      connect: async () => ({ address: 'Wa11et' }),
      signMessages: staleSign,
      // Held: connect just persisted THIS session's authorization.
      account: { address: 'Wa11et' },
      disconnect,
    },
  }

  const proof = await beginWalletLink(latestWallet(ref))

  expect(disconnect).toHaveBeenCalledTimes(1)
  expect(staleSign).toHaveBeenCalledTimes(1) // the refused re-sign, never repeated
  expect(freshSign).toHaveBeenCalledTimes(1) // exactly one fresh authorize + sign
  expect(proof.wallet).toBe('Wa11et')
  expect(proof.nonce).toBe('n-4')
})

// L78 — and the line the sign-step recovery must not blur: a -3 is the
// HUMAN'S answer on a rendered prompt. It surfaces untouched — no
// disconnect, no second prompt — even with an authorization held
// (L73(b): a refusal stays put and says why).
it('L78 — a human decline (-3) from the sign step surfaces untouched even while held: no disconnect, no re-prompt', async () => {
  type FetchResponse = {
    ok: boolean
    status: number
    json: () => Promise<unknown>
    text: () => Promise<string>
  }
  const challenge = { nonce: 'n-5', message: 'Talon sign-in\nwallet: Wa11et\nnonce: n-5' }
  global.fetch = jest.fn(async (url: unknown): Promise<FetchResponse> => {
    const path = String(url)
    if (path.endsWith('/v1/auth/wallet/challenge')) {
      return { ok: true, status: 200, json: async () => challenge, text: async () => JSON.stringify(challenge) }
    }
    throw new Error(`unexpected call: ${path}`)
  }) as unknown as typeof fetch

  const decline = Object.assign(new Error('sign request declined'), { code: -3 })
  const signMessages = jest.fn(async () => {
    throw decline
  })
  const disconnect = jest.fn(async () => {})

  await expect(
    beginWalletLink({
      connect: async () => ({ address: 'Wa11et' }),
      signMessages,
      account: { address: 'Wa11et' }, // held — and still untouched
      disconnect,
    }),
  ).rejects.toBe(decline)

  expect(signMessages).toHaveBeenCalledTimes(1) // never re-prompted
  expect(disconnect).not.toHaveBeenCalled()
})

// L78 — and the boundary the recovery must not cross: with nothing
// held, the refusal is the human's own answer to a fresh prompt. It is
// surfaced untouched — no disconnect, no second attempt (L73(b)).
it('L78 — a refusal with no held authorization surfaces untouched: no disconnect, no retry', async () => {
  const decline = new Error('sign request declined')
  const disconnect = jest.fn(async () => {})

  await expect(
    beginWalletLink({
      connect: async () => {
        throw decline
      },
      signMessages: async () => new Uint8Array(),
      disconnect,
    }),
  ).rejects.toBe(decline)

  expect(disconnect).not.toHaveBeenCalled()
})
