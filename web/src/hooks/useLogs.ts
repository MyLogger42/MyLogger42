import { useCallback, useEffect, useState } from 'react';
import { fetchLogs } from '../api/client';
import type { LogEntry, LogsQuery } from '../api/types';

interface UseLogsState {
  items: LogEntry[];
  total: number;
  limit: number;
  offset: number;
  loading: boolean;
  error: string | null;
}

interface UseLogsReturn extends UseLogsState {
  reload: () => void;
  setOffset: (offset: number) => void;
}

// useLogs wraps the GET /api/v1/logs endpoint with pagination state.
// The query object is treated as a single dependency — pass a stable
// reference (e.g. memoized at the call site) to avoid request loops.
//
// Pagination semantics: the hook owns its own offset (so the caller can
// page forward without rebuilding the query), and resets that offset to
// 0 whenever the structural shape of `query` changes. This means
// switching service/level/search restarts at the first page, while
// hitting "Next" with the same filters advances the page without a
// caller-side controller.
export function useLogs(query: LogsQuery): UseLogsReturn {
  const queryKey = JSON.stringify(query);

  const [state, setState] = useState<UseLogsState>({
    items: [],
    total: 0,
    limit: query.limit ?? 50,
    offset: query.offset ?? 0,
    loading: false,
    error: null,
  });

  const [tick, setTick] = useState(0);
  const reload = useCallback(() => setTick((t) => t + 1), []);
  const setOffset = useCallback((offset: number) => {
    setState((s) => ({ ...s, offset }));
  }, []);

  // Reset offset to 0 whenever the (non-offset) query shape changes.
  useEffect(() => {
    setState((s) => ({ ...s, offset: 0 }));
  }, [queryKey]);

  useEffect(() => {
    let cancelled = false;
    setState((s) => ({ ...s, loading: true, error: null }));
    fetchLogs({ ...query, offset: state.offset })
      .then((res) => {
        if (cancelled) return;
        setState((s) => ({
          ...s,
          items: res.items,
          total: res.total,
          limit: res.limit,
          offset: res.offset,
          loading: false,
          error: null,
        }));
      })
      .catch((err: Error) => {
        if (cancelled) return;
        setState((s) => ({ ...s, loading: false, error: err.message }));
      });
    return () => {
      cancelled = true;
    };
    // We intentionally serialise the query into the dep list so
    // structurally identical objects don't cause refetches.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [queryKey, state.offset, tick]);

  return { ...state, reload, setOffset };
}
