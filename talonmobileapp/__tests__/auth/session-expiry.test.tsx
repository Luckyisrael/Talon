import React from 'react'
import { DeviceEventEmitter } from 'react-native'
import { render, waitFor } from '@testing-library/react-native'
import RootLayout from '@/app/_layout'
import { meApi, usageApi } from '@/lib/api'
import { getToken, setRefreshToken, setToken } from '@/lib/auth'

// L67 / FM-SESSa — a dead session ends at the sign-in screen, from
// anywhere. The transport already does its whole job when a refresh dies
// (refresh → retry → clear); what it never did was SAY SO: the token was
// cleared and nothing observed it, so the app kept whatever screen it was
// on while every request started failing (FM-SESSa(i)) — the rescue was
// scattered across splash / auth self-heal / settings, each screen only
// saving itself (ii). The law: token death emits one event the root
// observes, landing the app on /auth from any screen; the transport's
// chain and web's handleExpiredSession stay untouched.
//
// Red before L67 (observed): the request from a deep screen clears the
// token, but no `auth:expired` fires and the root never navigates.

// The root's navigation seam: a mock-prefixed spy (jest's module factory
// may only close over `mock*` names) plus the segments it reports, so a
// test can put the app "on the auth stack" and pin the guard.
const mockReplace = jest.fn()
let mockSegments: string[] = ['(tabs)', 'build']

jest.mock('expo-router', () => {
  const R = require('react')
  const Stack = ({ children }: { children?: React.ReactNode }) =>
    R.createElement(R.Fragment, null, children)
  ;(Stack as { Screen?: unknown }).Screen = () => null
  return {
    Stack,
    useRouter: () => ({ replace: mockReplace }),
    useSegments: () => mockSegments,
  }
})

// Native seams the root imports for reasons this test doesn't exercise:
// the real modules need a dev client (push), a browser session (OAuth
// callback completion), reanimated's native thread, and the whole
// provider stack (keyboard, wallet, network) that wraps the Stack. The
// logic under test — the auth:expired effect — lives in RootLayout
// itself, which stays real.
jest.mock('expo-status-bar', () => ({ StatusBar: () => null }))
jest.mock('expo-web-browser', () => ({ maybeCompleteAuthSession: jest.fn() }))
jest.mock('react-native-reanimated', () => ({}))
jest.mock('@/lib/push', () => ({ initPush: jest.fn() }))
jest.mock('@/components/app-providers', () => ({
  AppProviders: ({ children }: { children?: React.ReactNode }) => children,
}))

// The real auth module on a fake SecureStore: getToken / setToken /
// clearToken are the shipped ones, so "the transport cleared the session"
// is observed as the store the app actually reads, not as a mock call.
const mockStore = new Map<string, string>()
jest.mock('expo-secure-store', () => ({
  getItemAsync: jest.fn(async (key: string) => mockStore.get(key) ?? null),
  setItemAsync: jest.fn(async (key: string, value: string) => {
    mockStore.set(key, value)
  }),
  deleteItemAsync: jest.fn(async (key: string) => {
    mockStore.delete(key)
  }),
}))

function respond(status: number, body: unknown) {
  const json = async () => body
  return {
    ok: status >= 200 && status < 300,
    status,
    json,
    text: async () => JSON.stringify(body),
  }
}

/** A session whose access token AND refresh token are both refused. */
function deadSession() {
  global.fetch = jest.fn(async (url: unknown) => {
    const u = String(url)
    if (u.includes('/v1/auth/refresh')) {
      return respond(401, { error: 'refresh token expired' }) as unknown as Response
    }
    return respond(401, { error: 'access token expired' }) as unknown as Response
  }) as unknown as typeof fetch
}

async function signIn(token: string, refresh: string) {
  await setToken(token)
  await setRefreshToken(refresh)
}

async function rejection<T>(p: Promise<T>): Promise<any> {
  try {
    await p
  } catch (e) {
    return e
  }
  throw new Error('expected the call to fail')
}

describe('a dead session lands on sign-in from anywhere (FM-SESSa)', () => {
  beforeEach(() => {
    mockStore.clear()
    mockReplace.mockClear()
    mockSegments = ['(tabs)', 'build']
  })

  it('L67 — a dead refresh clears the session, announces once, and the root replaces to /auth', async () => {
    await signIn('jwt-deep', 'rt-deep')
    deadSession()
    const seen: string[] = []
    const sub = DeviceEventEmitter.addListener('auth:expired', () => seen.push('expired'))

    await render(<RootLayout />)

    // A request from a deep screen: the refresh dies mid-flight…
    const err = await rejection(usageApi.get())
    expect(err.status).toBe(401)

    // …the transport did its last job before announcing: the token is gone.
    expect(await getToken()).toBeNull()

    // The announcement itself, and the one place that acts on it.
    expect(seen).toEqual(['expired'])
    await waitFor(() =>
      expect(mockReplace).toHaveBeenCalledWith('/auth'),
    )

    sub.remove()
  })

  it('L67 — N concurrent 401s share one announcement and one navigation', async () => {
    await signIn('jwt-parallel', 'rt-parallel')
    deadSession()
    const seen: string[] = []
    const sub = DeviceEventEmitter.addListener('auth:expired', () => seen.push('expired'))

    await render(<RootLayout />)
    // Both calls ride the same single-flight refresh; both fall through to
    // clear. The latch is keyed to the token that died, so the root hears
    // one death — and this token is not the previous test's, so the latch
    // has re-armed for this session (a sign-in announces its own death).
    await Promise.all([rejection(usageApi.get()), rejection(meApi.get())])

    expect(seen).toEqual(['expired'])
    expect(mockReplace).toHaveBeenCalledTimes(1)

    sub.remove()
  })

  it('L67 — a death announced while already on the auth stack does not re-navigate', async () => {
    mockSegments = ['auth', 'index']
    await signIn('jwt-on-auth', 'rt-on-auth')
    deadSession()
    const seen: string[] = []
    const sub = DeviceEventEmitter.addListener('auth:expired', () => seen.push('expired'))

    await render(<RootLayout />)
    await rejection(usageApi.get())

    // The transport always announces — it cannot know where the user is.
    // The root does, and the guard keeps the auth screen's own self-heal
    // from stacking a second navigation.
    expect(seen).toEqual(['expired'])
    expect(mockReplace).not.toHaveBeenCalled()

    sub.remove()
  })

  it("L67 — a guest's 401 (no session to die) never announces", async () => {
    // Nothing signed in: the transport's refresh branch never runs
    // (no token → nothing to refresh → nothing to announce), so a guest
    // hitting an authed route is an error on that screen — not a session
    // death that drags the whole app to sign-in.
    deadSession()
    const seen: string[] = []
    const sub = DeviceEventEmitter.addListener('auth:expired', () => seen.push('expired'))

    await render(<RootLayout />)
    await rejection(usageApi.get())

    expect(await getToken()).toBeNull()
    expect(seen).toEqual([])
    expect(mockReplace).not.toHaveBeenCalled()

    sub.remove()
  })
})
