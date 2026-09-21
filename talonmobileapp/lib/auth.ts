import * as SecureStore from 'expo-secure-store';
import AsyncStorage from '@react-native-async-storage/async-storage';

// JWT storage. SecureStore, not AsyncStorage: the token is a bearer
// credential, and this is the one place AsyncStorage isn't honest.
const TOKEN_KEY = 'talon.jwt';
const REFRESH_KEY = 'talon.refresh';

// The last SecureStore failure we saw, kept as a cheap signal for the UI.
// Reads distinguish three outcomes:
//   - a value (signed in)
//   - no value (signed out, the honest case)
//   - a throw (the keystore could not be read)
// The third used to be folded into the second, which turned a platform
// storage fault into a silent sign-out — the user saw the login screen with
// no explanation and no log line. It stays recorded here so the failure is
// at least observable in dev instead of vanishing.
let lastReadError: string | null = null;

// The trace. SecureStore's own file never appears on disk while every other
// prefs file survives, so the failure is somewhere in its write path and
// logcat is not a reliable channel for it. AsyncStorage persists reliably on
// this device (the onboarding flag survives), so the trace rides there: each
// step appends, and the next cold start replays it through getToken, which
// means the evidence outlives the process that produced it.
const TRACE_KEY = 'talon.auth.trace';

async function trace(step: string) {
  console.warn(`[auth] ${step}`);
  try {
    const prev = await AsyncStorage.getItem(TRACE_KEY);
    const line = `${new Date().toISOString()} ${step}`;
    await AsyncStorage.setItem(TRACE_KEY, prev ? `${prev}\n${line}` : line);
  } catch {
    // A trace that cannot record is not worth failing a sign-in over.
  }
}

export async function readTrace(): Promise<string | null> {
  try {
    return await AsyncStorage.getItem(TRACE_KEY);
  } catch {
    return null;
  }
}

export async function clearTrace(): Promise<void> {
  try {
    await AsyncStorage.removeItem(TRACE_KEY);
  } catch {
    // same
  }
}

export function secureStoreFault(): string | null {
  return lastReadError;
}

export async function getToken(): Promise<string | null> {
  try {
    const v = await SecureStore.getItemAsync(TOKEN_KEY);
    lastReadError = null;
    await trace(`getToken → ${v ? 'token present' : 'NULL (no stored value)'}`);
    return v;
  } catch (e) {
    lastReadError = e instanceof Error ? e.message : String(e);
    await trace(`getToken THREW: ${lastReadError}`);
    return null;
  }
}

export async function setToken(token: string): Promise<void> {
  await trace('setToken: calling setItemAsync…');
  let threw: string | null = null;
  try {
    await SecureStore.setItemAsync(TOKEN_KEY, token);
  } catch (e) {
    threw = e instanceof Error ? `${e.message} ${e.stack ?? ''}` : String(e);
    await trace(`setToken setItemAsync THREW: ${threw}`);
    throw e;
  }
  // Verify the write landed in the store the next read will use. A commit()
  // that reports success but leaves nothing on disk is the failure mode
  // that looks exactly like a sign-out, so check it here.
  let back: string | null = null;
  try {
    back = await SecureStore.getItemAsync(TOKEN_KEY);
  } catch (e) {
    await trace(`setToken read-back THREW: ${e instanceof Error ? e.message : String(e)}`);
    throw e;
  }
  await trace(`setToken read-back: ${back === token ? 'OK' : 'MISMATCH — write did not land'}`);
  if (back !== token) {
    throw new Error('the token write did not land in SecureStore');
  }
}

/** The long-lived session credential. Rotated on every refresh, revoked
 *  on sign-out. Same SecureStore treatment as the access token. */
export async function getRefreshToken(): Promise<string | null> {
  try {
    const v = await SecureStore.getItemAsync(REFRESH_KEY);
    lastReadError = null;
    await trace(`getRefreshToken → ${v ? 'present' : 'NULL'}`);
    return v;
  } catch (e) {
    lastReadError = e instanceof Error ? e.message : String(e);
    await trace(`getRefreshToken THREW: ${lastReadError}`);
    return null;
  }
}

export async function setRefreshToken(token: string): Promise<void> {
  await SecureStore.setItemAsync(REFRESH_KEY, token);
}

export async function clearToken(): Promise<void> {
  try {
    await SecureStore.deleteItemAsync(TOKEN_KEY);
  } catch {
    // Already gone, sign-out is idempotent.
  }
  try {
    await SecureStore.deleteItemAsync(REFRESH_KEY);
  } catch {
    // Already gone, sign-out is idempotent.
  }
}

export async function isAuthenticated(): Promise<boolean> {
  return (await getToken()) !== null;
}
