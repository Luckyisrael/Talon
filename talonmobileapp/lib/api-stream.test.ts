// L49 / FM-SSEa — fails-first: the phone's SSE stream must deliver events
// while the turn is still open.
//
// The failure being tested: React Native's global fetch is whatwg-fetch
// 3.6.20, whose promise settles only in xhr.onload — the whole response
// body, which an SSE turn never delivers before its own 15s connect
// deadline. So the harness reproduces both transports exactly:
//   - global fetch is stubbed to whatwg-fetch semantics: a promise that
//     never settles while the stream is open (the body ends when the turn
//     ends — minutes away);
//   - 'expo/fetch' is mocked to the native module's real contract: settle
//     at headers (RESPONSE_RECEIVED) and hand back a caller-fed reader
//     (didReceiveResponseData → body.getReader()).
// Pre-fix, openStream awaits the global fetch: no event ever reaches the
// callback, and the browser fetch is touched. Post-fix it reads expo/fetch.

import type { StreamEvent } from '@/lib/api'

jest.mock('expo-secure-store', () => ({
  getItemAsync: jest.fn(async () => 'test-token'),
  setItemAt: jest.fn(async () => undefined),
  setItemAsync: jest.fn(async () => undefined),
  deleteItemAsync: jest.fn(async () => undefined),
}))

// The mocked transport, hoisted with the factory (jest's `mock*` prefix rule).
const mockExpoFetch = jest.fn()
jest.mock('expo/fetch', () => ({
  fetch: (...args: unknown[]) => mockExpoFetch(...args),
}))

// whatwg-fetch over an open SSE: xhr.onload never fires before the turn
// ends, so the promise stays pending for the whole test.
const neverSettles = () => new Promise<never>(() => {})

type ReadChunk = { done: boolean; value?: Uint8Array }

// A body whose chunks the test feeds by hand — the shape of a stream that
// delivers incrementally instead of all at once.
function controlledBody() {
  const queue: ReadChunk[] = []
  let waiter: ((c: ReadChunk) => void) | null = null
  const deliver = (c: ReadChunk) => {
    if (waiter) {
      const w = waiter
      waiter = null
      w(c)
    } else {
      queue.push(c)
    }
  }
  return {
    body: {
      getReader: () => ({
        read: () =>
          new Promise<ReadChunk>((resolve) => {
            const next = queue.shift()
            if (next) resolve(next)
            else waiter = resolve
          }),
      }),
    },
    feed: (text: string) => deliver({ done: false, value: new TextEncoder().encode(text) }),
    close: () => deliver({ done: true }),
  }
}

// openStream's inner reader loop is a chain of awaits; drain the microtask
// queue hard so the assertion reads settled state, not an in-flight frame.
const flush = async () => {
  for (let i = 0; i < 40; i++) await Promise.resolve()
}

async function streamOnce(onEvent: (e: StreamEvent) => void, onError?: (e: Error) => void) {
  const { openStream } = await import('@/lib/api')
  return openStream('/v1/prompts/stream', { method: 'POST' }, onEvent, onError)
}

beforeEach(() => {
  mockExpoFetch.mockReset()
})

it('delivers events while the stream is still open', async () => {
  const events: StreamEvent[] = []
  const errors: Error[] = []
  const ctrl = controlledBody()
  mockExpoFetch.mockResolvedValue({ ok: true, status: 200, body: ctrl.body })
  global.fetch = neverSettles as unknown as typeof fetch

  const close = await streamOnce(
    (e) => events.push(e),
    (e) => errors.push(e),
  )

  // The server's first frame arrives while the body is still open — long
  // before anything could resolve on body-end.
  ctrl.feed('event: phase\ndata: {"phase":"PLAN"}\n\n')
  await flush()

  expect(events).toEqual([{ kind: 'phase', data: '{"phase":"PLAN"}' }])
  expect(errors).toEqual([])
  close()
})

it('never talks to the browser fetch for a stream', async () => {
  const browserFetch = jest.fn(() => neverSettles())
  global.fetch = browserFetch as unknown as typeof fetch
  const ctrl = controlledBody()
  mockExpoFetch.mockResolvedValue({ ok: true, status: 200, body: ctrl.body })

  const close = await streamOnce(() => {}, () => {})
  await flush()

  expect(browserFetch).not.toHaveBeenCalled()
  expect(mockExpoFetch).toHaveBeenCalledTimes(1)
  close()
})
