import type { NativeIntent } from 'expo-router'

export const redirectSystemPath: NonNullable<NativeIntent['redirectSystemPath']> = ({
  path,
  initial,
}) => {
  // Talon's Device tab deep-links the dev server as
  // `<scheme>://<host>:<port>?token=…`. Expo Router matches routes on path
  // only, so a URL that carries a host resolves to no route and the app
  // opens on its Unmatched screen instead of "/".
  //
  // A real deep link into this app has an empty host (`<scheme>:///route`),
  // so a non-empty host means the launch came from the dev server. Send it
  // to the root and let the app render its home screen.
  //
  // Never throw here: expo-router calls this during launch and a throw
  // crashes the app.
  if (initial) {
    try {
      if (new URL(path).host) return '/'
    } catch {
      // Not a parseable URL — a bare path. Hand it straight to the router.
    }
  }
  return path
}
