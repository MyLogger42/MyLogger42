import { useEffect, useState } from 'react';
import { AuthExpiredError, fetchSources } from '../api/client';
import type { Source } from '../api/types';

interface UseSourcesReturn {
  sources: Source[];
  loading: boolean;
  error: string | null;
  reload: () => void;
}

// useSources fetches the distinct service projection scoped to the
// given project slug. We don't poll — the FilterBar caller can call
// reload() if needed (e.g. after the user has been on the page for a
// while).
export function useSources(projectSlug: string): UseSourcesReturn {
  const [sources, setSources] = useState<Source[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    if (!projectSlug) {
      // Caller doesn't have a project yet (e.g. router still resolving).
      // Don't fire an HTTP call we know will 400.
      setSources([]);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    fetchSources(projectSlug)
      .then((res) => {
        if (cancelled) return;
        setSources(res.sources ?? []);
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
  }, [projectSlug, tick]);

  return { sources, loading, error, reload: () => setTick((t) => t + 1) };
}
