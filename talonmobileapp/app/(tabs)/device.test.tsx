import React from 'react'
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import DeviceScreen from './device'
import { openMetroStream, projectsApi, sessionsApi, testInfoApi } from '@/lib/api'

// L32 / FM-DEVb + FM-DEVc — the Device tab's truth stays on the server.
//
// S1 test-info follows the session: the server is the declared source of truth
//    for this screen ("Every value comes from the backend"), so a session
//    create/destroy must invalidate ['test-info'] — otherwise the tab keeps
//    serving "Not available yet" after a start, and a DEAD deep link + QR
//    after a destroy (the exact 404-on-the-phone the backend refuses to emit,
//    reproduced client-side).
// S2 the metro stream closes on unmount: openMetroStream resolves AFTER
//    cleanup in the hand-rolled effect, so the socket stayed open to a screen
//    that no longer existed. The stream must close even when the screen
//    unmounts before the promise settles.
//
// Harness notes: '@/lib/api' is PARTIALLY mocked — route shapes and every
// export the hook barrel pulls stay real, only the transport fns the screen
// drives are captured. expo-secure-store / async-storage are mocked at module
// level (the store's persistence layer), '@/lib/session' is mocked so no
// SecureStore round-trip runs. RTL v14: render() and fireEvent() are async —
// unawaited, screen never wires.

jest.mock('react-native-safe-area-context', () =>
  jest.requireActual('react-native-safe-area-context/jest/mock').default,
)
jest.mock('expo-secure-store', () => ({
  getItemAsync: jest.fn(async () => null),
  setItemAsync: jest.fn(async () => undefined),
  deleteItemAsync: jest.fn(async () => undefined),
}))
jest.mock('@react-native-async-storage/async-storage', () => ({
  getItem: jest.fn(async () => null),
  setItem: jest.fn(async () => undefined),
  removeItem: jest.fn(async () => undefined),
}))
jest.mock('@/lib/session', () => ({
  getSessionId: jest.fn(async () => null),
  setSessionId: jest.fn(async () => undefined),
}))
jest.mock('@react-native-clipboard/clipboard', () => ({
  setString: jest.fn(),
}))
jest.mock('react-native-qrcode-styled', () => ({
  __esModule: true,
  default: () => null,
}))
jest.mock('@/lib/api', () => {
  const actual = jest.requireActual('@/lib/api')
  return {
    ...actual,
    projectsApi: { list: jest.fn() },
    sessionsApi: {
      create: jest.fn(),
      destroy: jest.fn(),
      live: jest.fn(),
      get: jest.fn(),
    },
    testInfoApi: { get: jest.fn() },
    openMetroStream: jest.fn(),
  }
})

const mockList = projectsApi.list as jest.Mock
const mockLive = sessionsApi.live as jest.Mock
const mockGet = sessionsApi.get as jest.Mock
const mockCreate = sessionsApi.create as jest.Mock
const mockDestroy = sessionsApi.destroy as jest.Mock
const mockTestInfo = testInfoApi.get as jest.Mock
const mockOpenStream = openMetroStream as jest.Mock

function wrap(qc: QueryClient, ui: React.ReactElement) {
  return <QueryClientProvider client={qc}>{ui}</QueryClientProvider>
}

function freshQueryClient() {
  // gcTime: 0 for BOTH queries and mutations — RQ's defaults (5 min) leave a
  // GC timer per query/mutation after the test ends, which keeps the in-band
  // single-file run's event loop alive long after results print (mutations
  // schedule theirs on construction: Mutation.scheduleGc → 300000ms).
  // Queries-only was the pattern in verify-evidence.test; this file also runs
  // create/destroy mutations, hence the mutations half.
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, refetchOnWindowFocus: false, gcTime: 0 },
      mutations: { gcTime: 0 },
    },
  })
}

// The server's verdict with no session running — the mount state for every
// test below.
const IDLE_TEST_INFO = {
  available: false,
  reason: 'no device session running — start the device first',
  dev_client_url: '',
}

function primeApis() {
  mockList.mockResolvedValue([{ id: 1, name: 'Momentum' }])
  mockTestInfo.mockResolvedValue(IDLE_TEST_INFO)
  mockLive.mockResolvedValue(null)
  mockGet.mockResolvedValue({ id: 7, project_id: 1, state: 'live' })
  // A metro stream that opens cleanly; the test that cares about the open's
  // lifecycle overrides this with a deferred promise.
  mockOpenStream.mockReturnValue(Promise.resolve(jest.fn()))
}

beforeEach(() => {
  jest.clearAllMocks()
  primeApis()
})

it('keeps test-info in step with the session it describes (FM-DEVb)', async () => {
  const qc = freshQueryClient()
  // The server gains a session when create runs, and loses it when destroy
  // runs — the wire's answer to /live-session must follow.
  mockCreate.mockImplementation(async (projectId: number) => {
    const s = { id: 7, project_id: projectId, state: 'live' }
    mockLive.mockResolvedValue(s)
    return s
  })
  mockDestroy.mockImplementation(async () => {
    mockLive.mockResolvedValue(null)
  })

  await render(wrap(qc, <DeviceScreen />))
  await screen.findByText('Start session')

  const afterMount = mockTestInfo.mock.calls.length

  // Start: the tab must re-ask the server, or it keeps serving
  // available:false with no deep link / QR / APK until a remount.
  await fireEvent.press(screen.getByText('Start session'))
  await screen.findByText('#7') // create resolved + rendered
  await waitFor(() =>
    expect(mockTestInfo.mock.calls.length).toBeGreaterThan(afterMount),
  )

  // Destroy: the tab must re-ask again, or it keeps serving a deep link +
  // QR for a relay that no longer exists — a dead link on the phone.
  const afterStart = mockTestInfo.mock.calls.length
  await fireEvent.press(screen.getByText('Destroy session'))
  await screen.findByText('Start session') // session gone
  await waitFor(() =>
    expect(mockTestInfo.mock.calls.length).toBeGreaterThan(afterStart),
  )
})

it('closes the metro stream even when the screen unmounts before it resolves (FM-DEVc)', async () => {
  const qc = freshQueryClient()
  let release!: (close: () => void) => void
  mockOpenStream.mockReturnValue(
    new Promise<() => void>((res) => {
      release = res
    }),
  )
  const closeSpy = jest.fn()

  const { unmount } = await render(wrap(qc, <DeviceScreen />))
  await screen.findByText('Start session')
  expect(mockOpenStream).toHaveBeenCalledTimes(1)

  // Unmount BEFORE the open settles: cleanup runs with close still undefined
  // in the hand-rolled effect, so nothing closes the socket it left behind.
  await unmount()
  release(closeSpy)
  await new Promise((resolve) => setImmediate(resolve))

  expect(closeSpy).toHaveBeenCalled()
})
