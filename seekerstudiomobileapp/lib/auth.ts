import * as SecureStore from 'expo-secure-store';

// JWT storage. SecureStore — not AsyncStorage: the token is a bearer
// credential, and this is the one place AsyncStorage isn't honest.
const TOKEN_KEY = 'seeker.jwt';

export async function getToken(): Promise<string | null> {
  try {
    return await SecureStore.getItemAsync(TOKEN_KEY);
  } catch {
    return null;
  }
}

export async function setToken(token: string): Promise<void> {
  await SecureStore.setItemAsync(TOKEN_KEY, token);
}

export async function clearToken(): Promise<void> {
  try {
    await SecureStore.deleteItemAsync(TOKEN_KEY);
  } catch {
    // Already gone — sign-out is idempotent.
  }
}

export async function isAuthenticated(): Promise<boolean> {
  return (await getToken()) !== null;
}
