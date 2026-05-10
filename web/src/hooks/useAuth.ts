import { useCallback, useEffect, useState } from 'react';
import { AuthExpiredError, fetchMe, login as apiLogin, logout as apiLogout } from '../api/client';
import type { AuthMe } from '../api/types';

type Status = 'unknown' | 'authed' | 'anon';

interface UseAuthReturn {
  user: AuthMe | null;
  status: Status;
  login: (username: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  refresh: () => void;
}

// useAuth resolves the current session status by calling /auth/me on
// mount. Status starts at 'unknown' so the shell can show a brief
// loading state before deciding to render the app or redirect to
// /login.
//
// The login/logout helpers wrap the API calls and update local state
// on success. AuthExpiredError surfaces from any other API call also
// drives the status back to 'anon' via the global event listener
// installed in App.tsx.
export function useAuth(): UseAuthReturn {
  const [user, setUser] = useState<AuthMe | null>(null);
  const [status, setStatus] = useState<Status>('unknown');
  const [tick, setTick] = useState(0);

  useEffect(() => {
    let cancelled = false;
    fetchMe()
      .then((u) => {
        if (cancelled) return;
        setUser(u);
        setStatus('authed');
      })
      .catch((err: Error) => {
        if (cancelled) return;
        if (err instanceof AuthExpiredError) {
          setUser(null);
          setStatus('anon');
          return;
        }
        // Any other failure (server down, 503 no-admin, network) is
        // also rendered as 'anon' for the shell — login page is the
        // safest fallback when /me can't be served.
        setUser(null);
        setStatus('anon');
      });
    return () => {
      cancelled = true;
    };
  }, [tick]);

  const login = useCallback(async (username: string, password: string) => {
    await apiLogin({ username, password });
    const me = await fetchMe();
    setUser(me);
    setStatus('authed');
  }, []);

  const logout = useCallback(async () => {
    try {
      await apiLogout();
    } catch {
      // Even if the server logout fails (network), clear local state —
      // the user's intent is unambiguous.
    }
    setUser(null);
    setStatus('anon');
  }, []);

  const refresh = useCallback(() => setTick((t) => t + 1), []);

  return { user, status, login, logout, refresh };
}
