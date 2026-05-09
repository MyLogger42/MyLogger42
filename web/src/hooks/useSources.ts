import { useEffect, useState } from 'react';
import { fetchSources } from '../api/client';
import type { Source } from '../api/types';

interface UseSourcesReturn {
  sources: Source[];
  loading: boolean;
  error: string | null;
  reload: () => void;
}

// useSources fetches the distinct service projection. We don't poll —
// the FilterBar caller can call reload() if needed (e.g. after the user
// has been on the page for a while).
export function useSources(): UseSourcesReturn {
  const [sources, setSources] = useState<Source[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    fetchSources()
      .then((res) => {
        if (cancelled) return;
        setSources(res.sources ?? []);
        setLoading(false);
      })
      .catch((err: Error) => {
        if (cancelled) return;
        setError(err.message);
        setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [tick]);

  return { sources, loading, error, reload: () => setTick((t) => t + 1) };
}
