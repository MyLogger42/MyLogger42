import { useEffect, useState } from 'react';
import { AuthExpiredError, fetchStats } from '../api/client';
import type { LogStats, StatsQuery } from '../api/types';

interface UseStatsReturn {
  stats: LogStats | null;
  loading: boolean;
  error: string | null;
}

// useStats fetches the aggregate projection for the current filter. Like
// useLogs it serialises the query into the dep list, so a structurally
// identical query object doesn't refetch.
//
// It keeps the previous stats on screen while a new request is in flight:
// the histogram is a stable frame around the log list, and blanking it on
// every keystroke made the page jump.
export function useStats(query: StatsQuery): UseStatsReturn {
  const queryKey = JSON.stringify(query);
  const [stats, setStats] = useState<LogStats | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!query.project) {
      // Router still resolving the slug — don't fire a known 400.
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    fetchStats(query)
      .then((res) => {
        if (cancelled) return;
        setStats(res);
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [queryKey]);

  return { stats, loading, error };
}
