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
//     success it did not have;
//   L60 / FM-APKj — the intent carries the file's `contentUri`
//     (`content://…FileSystemFileProvider/…`, not `file://`, which
//     `Intent.prepareToLeaveProcess` refuses to cross the app boundary
//     on API 24+) plus FLAG_GRANT_READ_URI_PERMISSION (1), so the
//     package installer — a different UID — can actually open the file.
//     Without both, the primary path fails on every modern device and
//     the L55c fallback silently becomes the real path;
//   L61 / FM-APKi(iv) — the share sheet is also a deliberate choice:
//     `downloadAndShareDevClient` hands the downloaded file straight to
//     `shareAsync` (expo-sharing wraps it in its own FileProvider —
//     L39's proven export seam) without ever attempting the install
//     intent, so a device that can't run the direct flow gets a choice
//     instead of a post-failure fallback. One download seam feeds both.

// The single download both deliveries share — L55a's contract in one
// place. `require`, not `import` — same three reasons as L39's
// downloadApk (lib/api.ts): native modules stay OUT of this file's
// static graph so every suite importing the Device screen stays
// native-free, jest's mock registry answers `require`, and
// `require('./auth')` reaches the CJS mock for @/lib/auth where
// `await import()` would not.
async function downloadDevClientApk(url: string) {
  const { File, Paths } = require('expo-file-system') as typeof import('expo-file-system')
  const { getToken } = require('./auth') as typeof import('./auth')

  const token = await getToken()
  if (!token) throw new Error('sign in again — the dev client download needs your session')

  const filename = url.split('/').pop() || 'app-debug.apk'
  return File.downloadFileAsync(url, new File(Paths.cache, filename), {
    headers: { Authorization: `Bearer ${token}` },
    idempotent: true,
  })
}

export async function downloadAndInstallDevClient(url: string): Promise<void> {
  const { startActivityAsync } = require('expo-intent-launcher') as typeof import('expo-intent-launcher')

  const file = await downloadDevClientApk(url)

  try {
    await startActivityAsync('android.intent.action.VIEW', {
      // contentUri, not uri: a `file://` URI dies at the app boundary on
      // API 24+ (FileUriExposedException), which made this "primary" path
      // fail on every device and the share fallback below the real path.
      data: file.contentUri,
      type: 'application/vnd.android.package-archive',
      // FLAG_GRANT_READ_URI_PERMISSION — the package installer is a
      // different UID; without the grant it resolves the intent and then
      // cannot read the file, failing where the fallback never sees it.
      flags: 1,
    })
  } catch {
    // Genuine refusal only (post-L60: not the everyday path). ACTION_VIEW
    // on a raw file:// URI used to land here every time on API 24+ with
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

// L61 / FM-APKi(iv) — the deliberate second delivery: same session-token
// download, then straight to the share sheet. `shareAsync` takes the
// `file://` uri happily because expo-sharing wraps the file in ITS own
// FileProvider (the L39 export seam) — it is the install intent, not
// sharing, that refuses raw file URIs at the app boundary.
export async function downloadAndShareDevClient(url: string): Promise<void> {
  const file = await downloadDevClientApk(url)

  const { shareAsync } = require('expo-sharing') as typeof import('expo-sharing')
  await shareAsync(file.uri, {
    mimeType: 'application/vnd.android.package-archive',
    dialogTitle: 'Share the Talon dev client',
  })
}
