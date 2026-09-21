// Push registration: OneSignal SDK init + backend device binding.
//
// Laws:
//   - Dark until configured: EXPO_PUBLIC_ONESIGNAL_APP_ID absent (or the
//     native module missing on this build) means push is off, the backend
//     still records the in-app history, and this module stays silent.
//   - Registration happens once per signed-in session, after the JWT is
//     stored, the backend needs the Bearer token to bind the device.
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
 * Initialize OneSignal without prompting. The permission dialog belongs to
 * the setup screen (app/notification-setup.tsx), never to app start: the
 * person grants it knowing why, not as a cold open ambush.
 */
export function initPush(): void {
  if (inited || !pushAvailable()) return;
  inited = true;

  // OneSignal 5.x: initialize only. Requires privacy consent off in
  // dev unless the app collects it, this app does not track.
  OneSignal.initialize(APP_ID);
}

/** Whether the OS permission currently reads granted. False when push is
 *  dark (no app id) so callers show setup, never a dead toggle. */
export async function pushPermissionGranted(): Promise<boolean> {
  if (!pushAvailable()) return false;
  try {
    return await OneSignal.Notifications.getPermissionAsync();
  } catch {
    return false;
  }
}

/**
 * Ask for push permission from the setup screen, then bind the device.
 * Returns true only when the OS granted and the backend holds the token.
 * A denial is a user choice: the in-app history keeps working regardless.
 */
export async function requestPushPermission(): Promise<boolean> {
  if (!pushAvailable()) return false;
  initPush();
  let granted = false;
  try {
    granted = await OneSignal.Notifications.requestPermission(true);
  } catch {
    granted = false;
  }
  if (!granted) return false;
  const id = await registerPushWithBackend();
  return id !== '';
}

/**
 * Bind this device to the signed-in user on the backend. Called after the
 * OAuth exchange stores the JWT. Returns the external id (or '' when push
 * is dark or the id is not ready yet; OneSignal delivers it async).
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
    // Registration is a courtesy, a failure here never blocks the app.
    return '';
  }
}
