import { useCallback, useEffect, useState } from 'react';
import {
  AuthExpiredError,
  issueToken as apiIssue,
  listTokens,
  revokeToken as apiRevoke,
} from '../api/client';
import type { IssuedKey, ProjectKey } from '../api/types';

// useAccessTokens is useProjectKeys for the global MCP read tokens. As
// there, a fresh token's plaintext only flows through issue()'s return
// value and is never kept in hook state.
export function useAccessTokens() {
  const [tokens, setTokens] = useState<ProjectKey[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [tick, setTick] = useState(0);
  const reload = useCallback(() => setTick((t) => t + 1), []);

  useEffect(() => {
    let cancelled = false;
    listTokens()
      .then((res) => {
        if (cancelled) return;
        setTokens(res.tokens ?? []);
        setError(null);
      })
      .catch((err: Error) => {
        if (cancelled || err instanceof AuthExpiredError) return;
        setError(err.message);
      });
    return () => {
      cancelled = true;
    };
  }, [tick]);

  const issue = useCallback(
    async (label: string): Promise<IssuedKey> => {
      const issued = await apiIssue({ label });
      reload();
      return issued;
    },
    [reload],
  );

  const revoke = useCallback(
    async (id: string) => {
      await apiRevoke(id);
      reload();
    },
    [reload],
  );

  return { tokens, error, issue, revoke };
}
