// L55 / FM-APKi — the dev client installs from inside the app.
//
// The Device tab's "Download the dev client (APK)" used to be
// `Linking.openURL(dev_client_url)` — the system browser opened an
// owner-authenticated endpoint carrying none of the session this app
// already holds, so the phone got bounced to the web login (the user's
// "the web is asking me to authenticate again") or to a localhost URL
// that resolves to the phone itself. The app already owns the session and
// a file layer; the download and the install intent belong here.
//
// The law, pinned by lib/devclient.test.ts:
//   L55a — the download rides the session token in the Authorization
//     header (never in the URL — a query token lands in browser history
//     and server logs), through the exact expo-file-system seam L39
//     proved on the export/APK path;
//   L55b — the resolved file reaches Android's install intent
//     (ACTION_VIEW + the archive MIME), so the OS asks its one-time
//     "install unknown apps" question itself;
//   L55c — intent refusal falls back to the system share sheet (the
//     file:// trap on API 24+, FileUriExposedException), never a silent
//     failure and never a browser;
//   L55d — a failed download surfaces as an error; nothing claims
//     success it did not have.
export async function downloadAndInstallDevClient(url: string): Promise<void> {
  // `require`, not `import` — same three reasons as L39's downloadApk
  // (lib/api.ts): native modules stay OUT of this file's static graph so
  // every suite importing the Device screen stays native-free, jest's
  // mock registry answers `require`, and `require('./auth')` reaches the
  // CJS mock for @/lib/auth where `await import()` would not.
  const { File, Paths } = require('expo-file-system') as typeof import('expo-file-system')
  const { startActivityAsync } = require('expo-intent-launcher') as typeof import('expo-intent-launcher')
  const { getToken } = require('./auth') as typeof import('./auth')

  const token = await getToken()
  if (!token) throw new Error('sign in again — the dev client download needs your session')

  const filename = url.split('/').pop() || 'app-debug.apk'
  const file = await File.downloadFileAsync(url, new File(Paths.cache, filename), {
    headers: { Authorization: `Bearer ${token}` },
    idempotent: true,
  })

  try {
    await startActivityAsync('android.intent.action.VIEW', {
      data: file.uri,
      type: 'application/vnd.android.package-archive',
    })
  } catch {
    // ACTION_VIEW on a raw file:// URI dies on API 24+ with
    // FileUriExposedException. The share sheet hands the downloaded file
    // to the system installer instead — still in-app, still one tap, and
    // the system's package-installer flow (its own permission question,
    // its own progress) owns the rest.
    const { shareAsync } = require('expo-sharing') as typeof import('expo-sharing')
    await shareAsync(file.uri, {
      mimeType: 'application/vnd.android.package-archive',
      dialogTitle: 'Install the Talon dev client',
    })
  }
}
