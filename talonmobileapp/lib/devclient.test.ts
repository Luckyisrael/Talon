// L55 / FM-APKi — fails-first: the dev client installs from inside the app.
//
// The gap: the Device tab's "Download the dev client (APK)" was
// `Linking.openURL(dev_client_url)` — the system browser opened an
// owner-authenticated endpoint carrying none of the session this app
// already holds, so the phone got bounced to the web login (the user's
// "the web is asking me to authenticate again") or to a localhost URL
// that resolves to the phone itself.
//
// The law, three pins below:
//   L55a — the download rides the session token in the Authorization
//     header (never in the URL — a query token lands in browser history
//     and logs), through the same expo-file-system seam L39 proved;
//   L55b — the resolved file reaches Android's install intent
//     (ACTION_VIEW + the archive MIME) so the OS asks its one-time
//     "install unknown apps" question itself;
//   L55c — intent refusal falls back to the system share sheet (the
//     file:// trap on API 24+), never a silent failure and never a
//     browser.
//
// Pre-fix this module does not exist — the module-resolution failure IS
// the red (the house's accepted form): an in-app install has no code to
// test before it has code.

import { downloadAndInstallDevClient } from '@/lib/devclient'
import { File } from 'expo-file-system'
import { startActivityAsync } from 'expo-intent-launcher'
import { shareAsync } from 'expo-sharing'

jest.mock('expo-file-system', () => {
  const downloadFileAsync = jest.fn()
  function FileCtor(this: { parts?: string[] }, ...parts: string[]) {
    this.parts = parts
  }
  ;(FileCtor as unknown as { downloadFileAsync: jest.Mock }).downloadFileAsync = downloadFileAsync
  return { File: FileCtor, Paths: { cache: '/data/user/0/cache' } }
})
jest.mock('expo-intent-launcher', () => ({ startActivityAsync: jest.fn() }))
jest.mock('expo-sharing', () => ({ shareAsync: jest.fn() }))
jest.mock('@/lib/auth', () => ({ getToken: jest.fn(async () => 'tok-123') }))

const URL = 'http://10.180.8.39:8080/v1/projects/7/dev-client.apk'
const downloadMock = (File as unknown as { downloadFileAsync: jest.Mock }).downloadFileAsync
const intentMock = startActivityAsync as jest.Mock
const shareMock = shareAsync as jest.Mock

beforeEach(() => {
  jest.clearAllMocks()
  downloadMock.mockResolvedValue({ uri: 'file:///cache/app-debug.apk' })
  intentMock.mockResolvedValue({})
})

test('L55a — the download carries the session token in the header, never the URL', async () => {
  await downloadAndInstallDevClient(URL)

  expect(downloadMock).toHaveBeenCalledTimes(1)
  const [url, dest, opts] = downloadMock.mock.calls[0]
  expect(url).toBe(URL)
  expect(dest).toBeDefined()
  expect(opts).toEqual(
    expect.objectContaining({
      headers: expect.objectContaining({ Authorization: 'Bearer tok-123' }),
      idempotent: true,
    }),
  )
  // The token rides the header only — no query string, ever.
  expect(String(url)).not.toContain('tok-123')
})

test('L55b — the install intent fires ACTION_VIEW with the archive MIME', async () => {
  await downloadAndInstallDevClient(URL)

  expect(intentMock).toHaveBeenCalledWith('android.intent.action.VIEW', {
    data: 'file:///cache/app-debug.apk',
    type: 'application/vnd.android.package-archive',
  })
})

test('L55c — intent refusal falls back to the share sheet, never a silent failure', async () => {
  intentMock.mockRejectedValue(new Error('FileUriExposedException'))

  await downloadAndInstallDevClient(URL)

  expect(shareMock).toHaveBeenCalledWith('file:///cache/app-debug.apk', expect.anything())
})

test('L55d — a failed download surfaces as an error instead of pretending', async () => {
  downloadMock.mockRejectedValue(new Error('401 unauthorized'))

  await expect(downloadAndInstallDevClient(URL)).rejects.toThrow(/401/)
  expect(intentMock).not.toHaveBeenCalled()
  expect(shareMock).not.toHaveBeenCalled()
})
