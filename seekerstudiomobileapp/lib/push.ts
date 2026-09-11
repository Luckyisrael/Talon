// Push registration: OneSignal SDK init + backend device binding.
//
// Laws:
//   - Dark until configured: EXPO_PUBLIC_ONESIGNAL_APP_ID absent (or the
//     native module missing on this build) means push is off — the backend
//     still records the in-app history, and this module stays silent.
//   - Registration happens once per signed-in session, after the JWT is
//     stored — the backend needs the Bearer token to bind the device.
//   - The OneSignal subscription id is the external id the backend pushes
//     to (POST /v1/users/push-token).

import { OneSignal } from 'react-native-onesignal';

const APP_ID = process.env.EXPO_PUBLIC_ONESIGNAL_APP_ID ?? '';

let inited = false;

/** True when push is wired on this build (app id present + native module). */
export function pushAvailable(): boolean {
  return APP_ID !== '' && !!OneSignal;
}

/**
 * Initialize OneSignal and request push permission. Safe to call once per
 * app start; a missing app id is a no-op, not an error — the same dark
 * honesty the backend's Noop notifier has.
 */
export function initPush(): void {
  if (inited || !pushAvailable()) return;
  inited = true;

  // OneSignal 5.x: initialize + request. Requires privacy consent off in
  // dev unless the app collects it — this app does not track.
  OneSignal.initialize(APP_ID);
  OneSignal.Notifications.requestPermission(false).catch(() => {
    // Permission denied is a user choice, not an error to surface.
  });
}

/**
 * Bind this device to the signed-in user on the backend. Called after the
 * OAuth exchange stores the JWT. Returns the external id (or '' when push
 * is dark / the id is not ready yet — OneSignal delivers it async).
 */
export async function registerPushWithBackend(): Promise<string> {
  if (!pushAvailable()) return '';
  try {
    // The subscription can lag the init by a moment; the device observer
    // resolves once OneSignal has it (or immediately if cached).
    const device = await OneSignal.User.getOnesignalId();
    if (!device) return '';
    const { meApi } = await import('./api');
    const { getToken } = await import('./auth');
    if (!(await getToken())) return ''; // signed out: nothing to bind
    await meApi.setPushToken(device);
    return device;
  } catch {
    // Registration is a courtesy — a failure here never blocks the app.
    return '';
  }
}
