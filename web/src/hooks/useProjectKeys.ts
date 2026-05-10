import { useCallback, useEffect, useState } from 'react';
import {
  AuthExpiredError,
  issueKey as apiIssue,
  listKeys,
  revokeKey as apiRevoke,
} from '../api/client';
import type { IssuedKey, ProjectKey } from '../api/types';

interface UseProjectKeysReturn {
  keys: ProjectKey[];
  loading: boolean;
  error: string | null;
  reload: () => void;
  issue: (label: string) => Promise<IssuedKey>;
  revoke: (keyId: string) => Promise<void>;
}

// useProjectKeys wraps the per-project key list + issue + revoke
// endpoints. The plaintext value of a fresh key only flows through
// the return value of issue() — it's never stored in the hook's state.
// Callers (SettingsPage) hand it to KeyRevealModal once and discard it.
export function useProjectKeys(slug: string): UseProjectKeysReturn {
  const [keys, setKeys] = useState<ProjectKey[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [tick, setTick] = useState(0);

  const reload = useCallback(() => setTick((t) => t + 1), []);

  useEffect(() => {
    if (!slug) {
      setKeys([]);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    listKeys(slug)
      .then((res) => {
        if (cancelled) return;
        setKeys(res.keys ?? []);
        setLoading(false);
      })
      .catch((err: Error) => {
        if (cancelled) return;
        if (err instanceof AuthExpiredError) return;
        setError(err.message);
        setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [slug, tick]);

  const issue = useCallback(
    async (label: string) => {
      const issued = await apiIssue(slug, { label });
      reload();
      return issued;
    },
    [slug, reload],
  );

  const revoke = useCallback(
    async (keyId: string) => {
      await apiRevoke(slug, keyId);
      reload();
    },
    [slug, reload],
  );

  return { keys, loading, error, reload, issue, revoke };
}
