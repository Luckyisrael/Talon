import React from 'react'
import { Linking } from 'react-native'
import { fireEvent, render, screen } from '@testing-library/react-native'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import DeviceScreen from '@/app/(tabs)/device'
import { openMetroStream, projectsApi, sessionsApi, testInfoApi } from '@/lib/api'
import { downloadAndInstallDevClient, downloadAndShareDevClient } from '@/lib/devclient'

// L55 / FM-APKi — fails-first: the APK button must never reach the browser.
//
// The gap: `Linking.openURL(dev_client_url)` handed an owner-authenticated
// URL to the system browser, which carries no session — the re-auth wall
// the user hit on camera. The button now drives the in-app
// download-and-install path (lib/devclient), and this suite pins the
// wiring from this screen's side: the helper gets the URL, the browser
// does not.
//
// Harness notes (from device.test.tsx): '@/lib/api' partially mocked,
// expo-secure-store / async-storage / session mocked at module level,
// RTL v14 render() and fireEvent() are async.

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
jest.mock('@react-native-clipboard/clipboard', () => ({ setString: jest.fn() }))
jest.mock('react-native-qrcode-styled', () => ({ __esModule: true, default: () => null }))
jest.mock('@/lib/api', () => {
  const actual = jest.requireActual('@/lib/api')
  return {
    ...actual,
    projectsApi: { list: jest.fn() },
    sessionsApi: { create: jest.fn(), destroy: jest.fn(), live: jest.fn(), get: jest.fn() },
    testInfoApi: { get: jest.fn() },
    openMetroStream: jest.fn(),
  }
})
jest.mock('@/lib/devclient', () => ({
  downloadAndInstallDevClient: jest.fn(async () => {}),
  downloadAndShareDevClient: jest.fn(async () => {}),
}))

const APK = 'http://10.180.8.39:8080/v1/projects/7/dev-client.apk'
const mockList = projectsApi.list as jest.Mock
const mockTestInfo = testInfoApi.get as jest.Mock
const mockLive = sessionsApi.live as jest.Mock
const mockGet = sessionsApi.get as jest.Mock
const mockOpenStream = openMetroStream as jest.Mock
const installMock = downloadAndInstallDevClient as jest.Mock
const shareMock = downloadAndShareDevClient as jest.Mock

function wrap(qc: QueryClient, ui: React.ReactElement) {
  return <QueryClientProvider client={qc}>{ui}</QueryClientProvider>
}

beforeEach(() => {
  jest.clearAllMocks()
  mockList.mockResolvedValue([{ id: 7, name: 'Momentum' }])
  mockLive.mockResolvedValue(null)
  mockGet.mockResolvedValue(null)
  mockOpenStream.mockReturnValue(Promise.resolve(jest.fn()))
  mockTestInfo.mockResolvedValue({
    available: true,
    dev_client_url: APK,
    deep_link: 'exp://10.180.8.39:8081',
    metro_url: 'exp://10.180.8.39:8081',
    dev_launcher_link: 'exp://10.180.8.39:8081',
  })
})

test('L55e — pressing the APK button installs in-app: the browser never sees the URL', async () => {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  await render(wrap(qc, <DeviceScreen />))

  await fireEvent.press(await screen.findByText('Download the dev client (APK)'))

  expect(installMock).toHaveBeenCalledWith(APK)
  expect(Linking.openURL).not.toHaveBeenCalledWith(APK)
})

// L61b / FM-APKi(iv) — the share control is a sibling choice beside the
// download: pressing it shares the APK, and it never touches the install
// helper or the browser (those are the other button's job, and the
// browser's was deleted in L55 for carrying no session).
test('L61b — pressing Share hands the URL to the share path: not the installer, not the browser', async () => {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  await render(wrap(qc, <DeviceScreen />))

  await fireEvent.press(await screen.findByText('Share (APK)'))

  expect(shareMock).toHaveBeenCalledWith(APK)
  expect(installMock).not.toHaveBeenCalled()
  expect(Linking.openURL).not.toHaveBeenCalledWith(APK)
})
