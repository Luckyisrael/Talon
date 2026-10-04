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
// And L60 / FM-APKj's pin: the intent carries `contentUri` (a
// `content://…FileSystemFileProvider/…` the app boundary will pass) plus
// FLAG_GRANT_READ_URI_PERMISSION — never a bare `file://`, never a
// content URI the installer lacks the grant to read.
//
// Pre-fix this module does not exist — the module-resolution failure IS
// the red (the house's accepted form): an in-app install has no code to
// test before it has code.

import { downloadAndInstallDevClient, downloadAndShareDevClient } from '@/lib/devclient'
import { File } from 'expo-file-system'
import { startActivityAsync } from 'expo-intent-launcher'
import { shareAsync } from 'expo-sharing'
import appJson from '../app.json'

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
const CONTENT_URI =
  'content://com.talon.app.FileSystemFileProvider/cached_expo_files/app-debug.apk'
const downloadMock = (File as unknown as { downloadFileAsync: jest.Mock }).downloadFileAsync
const intentMock = startActivityAsync as jest.Mock
const shareMock = shareAsync as jest.Mock

beforeEach(() => {
  jest.clearAllMocks()
  downloadMock.mockResolvedValue({
    uri: 'file:///cache/app-debug.apk',
    contentUri: CONTENT_URI,
  })
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

  // The URI's shape and grant are L60's law; here: the right action with
  // the archive MIME reaches the intent.
  expect(intentMock).toHaveBeenCalledWith(
    'android.intent.action.VIEW',
    expect.objectContaining({ type: 'application/vnd.android.package-archive' }),
  )
})

test('L55c — intent refusal falls back to the share sheet, never a silent failure', async () => {
  intentMock.mockRejectedValue(new Error('FileUriExposedException'))

  await downloadAndInstallDevClient(URL)

  expect(shareMock).toHaveBeenCalledWith('file:///cache/app-debug.apk', expect.anything())
})

// L60 / FM-APKj — the intent must carry a URI that survives the app
// boundary (`content://`, not the `file://` that makes
// `Intent.prepareToLeaveProcess` throw `FileUriExposedException` on
// API 24+) and the grant that lets the package installer — a different
// UID — actually open the file. Without both, the "primary" path fails
// on every modern device and the share-sheet fallback becomes the real
// path, every time.
test('L60 — the install intent carries the content URI and the read grant, never a bare file://', async () => {
  await downloadAndInstallDevClient(URL)

  const params = intentMock.mock.calls[0][1] as { data: string; flags?: number }
  expect(params.data).toBe(CONTENT_URI)
  expect(params.data).not.toMatch(/^file:/)
  expect(params.flags).toBe(1) // FLAG_GRANT_READ_URI_PERMISSION
})

// L60(d) / FM-APKj(iii) — since API 26 an app must hold
// `REQUEST_INSTALL_PACKAGES` to *initiate* a package install; without it
// the installer refuses the session before drawing any UI, so the button
// cycles back with no dialog and no error. app.json is prebuild's source
// of truth (the merged manifest is its artifact — re-verified after each
// rebuild, the FM-DEVd shape).
test('L60d — the manifest source declares the permission that lets the app start an install', () => {
  const permissions =
    (appJson as { expo?: { android?: { permissions?: string[] } } }).expo?.android?.permissions ?? []
  expect(permissions).toContain('REQUEST_INSTALL_PACKAGES')
})

// L61a / FM-APKi(iv) — the share delivery is a deliberate choice: it
// reuses the session-token download and hands the file to the share
// sheet directly, never touching the install intent (and never a browser).
test('L61a — the share path downloads with the session token and hands the file to the share sheet', async () => {
  await downloadAndShareDevClient(URL)

  expect(downloadMock).toHaveBeenCalledTimes(1)
  const [url, , opts] = downloadMock.mock.calls[0]
  expect(opts).toEqual(
    expect.objectContaining({
      headers: expect.objectContaining({ Authorization: 'Bearer tok-123' }),
      idempotent: true,
    }),
  )
  expect(String(url)).not.toContain('tok-123')
  expect(shareMock).toHaveBeenCalledWith(
    'file:///cache/app-debug.apk',
    expect.objectContaining({ mimeType: 'application/vnd.android.package-archive' }),
  )
  expect(intentMock).not.toHaveBeenCalled()
})

test('L55d — a failed download surfaces as an error instead of pretending', async () => {
  downloadMock.mockRejectedValue(new Error('401 unauthorized'))

  await expect(downloadAndInstallDevClient(URL)).rejects.toThrow(/401/)
  expect(intentMock).not.toHaveBeenCalled()
  expect(shareMock).not.toHaveBeenCalled()
})
