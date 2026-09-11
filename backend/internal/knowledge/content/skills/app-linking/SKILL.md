---
name: app-linking
description: Verified deep links and URL schemes for Talon apps — assetlinks.json, scheme registration, expo-linking, and the difference between "scheme link" and "verified app link". Load when the build needs to open from a URL or be linked from another app.
version: "Android App Links 2025+; expo-linking@55"
autoAttach: false
triggers: [content:linking, content:deep.?link, content:scheme, content:universal.?link, content:assetlinks, import:expo-linking, import:react-native-app-link]
---

# App linking on the Talon

A Talon app can be opened from a URL in **two** ways, and they are
not the same:

- **Custom scheme link** — `myapp://path`. Trivial to set up. No
  domain verification. Any other app can claim the same scheme.
  Fine for *internal* navigation between two apps you control.
- **Verified app link (Android App Links)** —
  `https://yourdomain.com/path`. Requires a published
  `assetlinks.json` at `/.well-known/assetlinks.json` proving you
  control the domain and the package. The OS will open your app
  instead of the browser, with no chooser dialog. **This is what
  you want for production.**

Pick **one** and document it. Mixing both is a source of bugs.

## Custom scheme

- Register the scheme in `app.json`:
  ```json
  { "expo": { "scheme": ["myapp"] } }
  ```
- `expo-linking` exposes `Linking.openURL('myapp://path')` and
  `useURL()` to read the URL that opened the app.
- Test by long-pressing a link in a browser or running
  `adb shell am start -W -a android.intent.action.VIEW -d "myapp://path"`.

## Verified app link

1. **Choose a domain you control.** A subdomain is fine
   (`link.example.com`).
2. **Host `assetlinks.json`** at
   `https://yourdomain.com/.well-known/assetlinks.json`:
   ```json
   [{
     "relation": ["delegate_permission/common.handle_all_urls"],
     "target": {
       "namespace": "android_app",
       "package_name": "com.yourcompany.yourapp",
       "sha256_cert_fingerprints": ["<your release SHA-256>"]
     }
   }]
   ```
3. **Add the intent filter** in `app.json`:
   ```json
   { "expo": { "android": { "intentFilters": [{
     "action": "VIEW",
     "autoVerify": true,
     "data": [{ "scheme": "https", "host": "yourdomain.com" }]
   }] } } }
   ```
4. **Re-run `expo prebuild`** so the native intent filter is
   written into `AndroidManifest.xml`.
5. **Verify** with
   `adb shell pm verify-app-links --re-verify com.yourcompany.yourapp`.

The release SHA-256 must match the keystore the user actually
installed. A debug build's fingerprint will not be verified.

## Common failures

- **`assetlinks.json` returns a redirect.** The OS will not follow
  it. The file must be served with the exact content-type
  `application/json` at the exact path.
- **Wrong SHA-256.** Compute it correctly:
  `keytool -list -v -keystore release.keystore | grep SHA256`.
  Strip colons, lowercase.
- **Mixed content.** `assetlinks.json` over HTTPS only. Plain
  HTTP is rejected.
- **The app re-prompts the user to choose between browser and
  app.** Auto-verify failed. The OS will fall back to the
  chooser, which is the symptom that something is wrong with the
  assetlinks file or the intent filter.
- **The path is opened by the wrong app.** Another app declared
  the same `intent-filter` for the same host. App links are
  exclusive once verified; the OS picks the first verified
  app.

## Testing flow

- `expo prebuild --clean` to regenerate native code.
- `npx expo run:android --variant release` (a release build is
  the only one whose SHA matches the assetlinks file).
- `adb shell pm get-app-links com.yourcompany.yourapp` to see
  which domains are verified.
- `adb shell am start -W -a android.intent.action.VIEW -d "https://yourdomain.com/path"` to open it.
