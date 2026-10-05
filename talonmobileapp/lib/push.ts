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

// The last subscription id we POSTed, so the change listener in initPush()
// only speaks when the id actually moves.
let lastPostedId = '';

// OneSignal hands out two different UUIDs for the same device:
//   - the *user / identity* id (getOnesignalId()) — who the user is, and
//   - the *push subscription* id — the object a send actually targets.
// Only the second one delivers: POST /v1/users/push-token happily stores
// the identity id (204) and every send then fails OneSignal's side with
// "All included players are not subscribed" — accepted by us, rejected by
// them, invisible until a notification never arrives. The subscription id
// can also lag initialize() by a moment, so poll for it rather than
// reading once and shipping whatever came back.
const SUBSCRIPTION_POLL_MS = 15_000;
const SUBSCRIPTION_POLL_INTERVAL_MS = 1_000;

/** The push subscription id, or null when the window closes without one. */
async function getSubscriptionId(): Promise<string | null> {
  const deadline = Date.now() + SUBSCRIPTION_POLL_MS;
  for (;;) {
    try {
      const id = await OneSignal.User.pushSubscription.getIdAsync();
      if (id) return id;
    } catch {
      // The SDK can throw while it is still coming up; keep polling.
    }
    if (Date.now() >= deadline) return null;
    await new Promise((resolve) => setTimeout(resolve, SUBSCRIPTION_POLL_INTERVAL_MS));
  }
}

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

  // Late subscription ids: when the id appears — first FCM token, or it
  // rotates after a reinstall / cleared data — bind it then, instead of
  // leaving the backend on a dead id until the next cold start.
  OneSignal.User.pushSubscription.addEventListener('change', (state) => {
    const id = state.current.id ?? '';
    if (id && id !== lastPostedId) void registerPushWithBackend();
  });
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
    // The id the backend (and include_player_ids) needs: the push
    // subscription id — never the identity id from getOnesignalId().
    const subscription = await getSubscriptionId();
    if (!subscription) {
      // Not fatal: the change listener posts it when it lands.
      console.warn('[push] subscription id not ready in 15s — not posting');
      return '';
    }
    const { meApi } = await import('./api');
    const { getToken } = await import('./auth');
    if (!(await getToken())) return ''; // signed out: nothing to bind
    await meApi.setPushToken(subscription);
    lastPostedId = subscription;
    return subscription;
  } catch (error) {
    // Registration is a courtesy, a failure here never blocks the app —
    // but it is logged: silence here is what hid the wrong-id bug.
    console.warn('[push] registration failed', error);
    return '';
  }
}
