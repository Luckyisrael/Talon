import * as SecureStore from 'expo-secure-store';

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

export function secureStoreFault(): string | null {
  return lastReadError;
}

export async function getToken(): Promise<string | null> {
  try {
    return await SecureStore.getItemAsync(TOKEN_KEY);
  } catch (e) {
    lastReadError = e instanceof Error ? e.message : String(e);
    return null;
  }
}

export async function setToken(token: string): Promise<void> {
  await SecureStore.setItemAsync(TOKEN_KEY, token);
  // Read the write back from the same store the next cold start will read.
  // A commit() that reports success while leaving nothing on disk looks
  // exactly like a sign-out, and this is the only place that can tell the
  // two apart.
  const back = await SecureStore.getItemAsync(TOKEN_KEY);
  if (back !== token) {
    throw new Error('the session did not persist on this device. Set a device lock screen, then sign in again.');
  }
}

/** The long-lived session credential. Rotated on every refresh, revoked
 *  on sign-out. Same SecureStore treatment as the access token. */
export async function getRefreshToken(): Promise<string | null> {
  try {
    return await SecureStore.getItemAsync(REFRESH_KEY);
  } catch (e) {
    lastReadError = e instanceof Error ? e.message : String(e);
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
