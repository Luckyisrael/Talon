import { useEffect, useState } from 'react';
import { isAuthenticated } from './auth';

// useSession resolves the auth gate for routing: 'checking' while SecureStore
// is read, then 'authed' or 'guest'. Screens mount on it (Phase 1 wires it
// into the root layout) — the hook itself creates no routes.
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
