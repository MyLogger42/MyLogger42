import { useEffect, useState } from 'react';
import { AuthExpiredError, fetchHealth } from '../api/client';

interface UseCapabilitiesReturn {
  // perProjectRetention is false when the active storage backend applies
  // one global retention window and ignores the per-project setting
  // (VictoriaLogs). Defaults to true so the retention field renders
  // normally while health is still loading, or if the call fails.
  perProjectRetention: boolean;
  storage: string;
}

// useCapabilities reads the backend's self-reported capabilities from
// /health. It's fetched once per mount — the storage backend can't
// change without a server restart.
export function useCapabilities(): UseCapabilitiesReturn {
  const [perProjectRetention, setPerProjectRetention] = useState(true);
  const [storage, setStorage] = useState('');

  useEffect(() => {
    let cancelled = false;
    fetchHealth()
      .then((res) => {
        if (cancelled) return;
        setStorage(res.storage ?? '');
        setPerProjectRetention(res.capabilities?.perProjectRetention !== false);
      })
      .catch((err: Error) => {
        if (err instanceof AuthExpiredError) return;
        // Health is advisory here; a failure just leaves the defaults.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return { perProjectRetention, storage };
}
