const TOKEN_KEY = "token";
const REFRESH_KEY = "refresh_token";
const EMAIL_KEY = "email";

export function getToken(): string | null {
  if (typeof window === "undefined") return null;
  return localStorage.getItem(TOKEN_KEY);
}

export function setToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token);
}

/** The long-lived session credential. Stored beside the access token,
 *  rotated on every refresh, revoked on sign-out. */
export function getRefreshToken(): string | null {
  if (typeof window === "undefined") return null;
  return localStorage.getItem(REFRESH_KEY);
}

export function setRefreshToken(token: string): void {
  localStorage.setItem(REFRESH_KEY, token);
}

// The authoritative source for "who is signed in" is now GET /v1/me. We still
// cache the sign-in address next to the token so the shell can paint an identity
// instantly on load, before /me resolves — but it is only a cache: a screen that
// shows account state (settings) reads /me and treats that as truth. Never an
// invented placeholder.
export function getEmail(): string | null {
  if (typeof window === "undefined") return null;
  return localStorage.getItem(EMAIL_KEY);
}

export function setEmail(email: string): void {
  localStorage.setItem(EMAIL_KEY, email);
}

export function clearToken(): void {
  localStorage.removeItem(TOKEN_KEY);
  localStorage.removeItem(REFRESH_KEY);
  localStorage.removeItem(EMAIL_KEY);
}

/** Ends the session and forgets the identity. The caller handles navigation. */
export function signOut(): void {
  clearToken();
}

export function isAuthenticated(): boolean {
  return !!getToken();
}
