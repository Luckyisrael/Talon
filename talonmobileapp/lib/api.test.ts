import { exportApi, skrApi } from '@/lib/api'

// FM-SKRb(i) — a refusal has to reach the caller as an offer, not as prose.
//
// The backend's 402 carries code / spare_credits / skr_amount_per_pack /
// credits_per_pack precisely so the unlock sheet can render the whole offer
// from the refusal itself, with no second round trip. parseResponse builds
// `new Error(body.error)` and attaches only `.status` — every field that
// names the price is dropped before a screen can read it, which leaves the
// app with a sentence it has to show and no way to say what unlocking costs.
//
// The fix is additive by construction: `.message` keeps the server's words
// (line 808 already branches on `.status`, and nothing may change for it),
// and the untouched body rides alongside under `.detail`.

// No module mock: lib/api.ts imports './auth' dynamically, and intercepting
// that specifier puts the dynamic import into a loader that rejects it before
// parseResponse ever runs. The real module resolves on its own (getToken has
// no stored session here and returns without an Authorization header), so the
// transport stays real and only `fetch` is captured — which is what makes this
// a test of the shipped refusal path rather than of a mock.

const OFFER = {
  error: 'a release build spends one credit — you have none left',
  code: 'export_credits_required',
  spare_credits: 0,
  credits_per_export: 1,
  skr_amount_per_pack: 100,
  credits_per_pack: 5,
}

function respond(status: number, body: unknown) {
  const json = async () => body
  global.fetch = jest.fn(async () => ({
    ok: status >= 200 && status < 300,
    status,
    json,
    text: async () => JSON.stringify(body),
  })) as unknown as typeof fetch
}

async function rejection<T>(p: Promise<T>): Promise<any> {
  try {
    await p
  } catch (e) {
    return e
  }
  throw new Error('expected the call to be refused')
}

describe('exportApi.start', () => {
  it('keeps the paywall offer on the thrown error', async () => {
    respond(402, OFFER)

    const err = await rejection(exportApi.start(7, 'first release'))

    // The status the existing consumers branch on, unchanged.
    expect(err.status).toBe(402)
    // The message the user reads, unchanged.
    expect(err.message).toBe(OFFER.error)

    // The offer itself — the whole reason the server sends a 402 body.
    expect(err.detail).toBeDefined()
    expect(err.detail.code).toBe('export_credits_required')
    expect(err.detail.spare_credits).toBe(0)
    expect(err.detail.credits_per_export).toBe(1)
    expect(err.detail.skr_amount_per_pack).toBe(100)
    expect(err.detail.credits_per_pack).toBe(5)
  })

  it('still hands a non-402 refusal its own body', async () => {
    respond(404, { error: 'project not found' })

    const err = await rejection(exportApi.start(7, 'first release'))

    expect(err.status).toBe(404)
    expect(err.message).toBe('project not found')
    expect(err.detail.error).toBe('project not found')
  })

  it('leaves a successful start alone', async () => {
    respond(202, { job_id: 41, project_id: 7, status: 'queued' })

    await expect(exportApi.start(7, 'first release')).resolves.toEqual({
      job_id: 41,
      project_id: 7,
      status: 'queued',
    })
  })
})

// FM-SKRc(i) — the paywall's first question must reach the server the
// server actually serves. The router registers POST /v1/skr/quote; the
// client sent GET (request()'s default), so every quote died 405 and the
// panel rendered "could not read your export credits" — a wire-contract
// error wearing the costume of a ledger failure (field record: four
// GET /v1/skr/quote status=405 at 03:52). Red today: quote() passes no
// method, so fetch is called with none — GET.
describe('L62 — the quote reaches the router in the method the router registered', () => {
  it('calls POST /v1/skr/quote', async () => {
    respond(200, {
      skr_amount: 100,
      credits: 5,
      mint: 'mint',
      treasury: 'treasury',
      spare_credits: 0,
    })

    await skrApi.quote()

    const init = (global.fetch as jest.Mock).mock.calls[0][1] as RequestInit
    expect(init.method).toBe('POST')
  })
})
