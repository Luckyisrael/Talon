import { useEffect, useState } from 'react';
import { isAuthenticated } from './auth';
import * as SecureStore from 'expo-secure-store';

// useSession resolves the auth gate for routing: 'checking' while SecureStore
// is read, then 'authed' or 'guest'. Screens mount on it — the hook itself
// creates no routes.
export function useSession(): 'checking' | 'authed' | 'guest' {
  const [state, setState] = useState<'checking' | 'authed' | 'guest'>('checking');
  useEffect(() => {
    let live = true;
    isAuthenticated().then((ok) => {
      if (live) setState(ok ? 'authed' : 'guest');
    });
    return () => {
      live = false;
    };
  }, []);
  return state;
}

// ── Per-project device-session id ──────────────────────
// The backend exposes sessions by id only (the web IDE holds the id in
// client state too); the mobile app persists the id per project so the
// device tab survives restarts. SecureStore: it is a capability — knowing
// it grants the session's screenshot/relay surface.

const sessionKey = (projectId: number) => `talon.session.${projectId}`;

export async function getSessionId(projectId: number): Promise<number | null> {
  try {
    const raw = await SecureStore.getItemAsync(sessionKey(projectId));
    return raw ? Number(raw) : null;
  } catch {
    return null;
  }
}

export async function setSessionId(projectId: number, id: number | null): Promise<void> {
  const key = sessionKey(projectId);
  if (id == null) {
    try {
      await SecureStore.deleteItemAsync(key);
    } catch {
      // Already gone — clearing is idempotent.
    }
    return;
  }
  await SecureStore.setItemAsync(key, String(id));
}
