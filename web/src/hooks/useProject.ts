import { useCallback, useEffect, useState } from 'react';
import {
  AuthExpiredError,
  getProject,
  HttpError,
  updateProject as apiUpdate,
} from '../api/client';
import type { Project, UpdateProjectRequest } from '../api/types';

type Status = 'loading' | 'ready' | 'not-found' | 'error';

interface UseProjectReturn {
  project: Project | null;
  status: Status;
  error: string | null;
  reload: () => void;
  update: (req: UpdateProjectRequest) => Promise<Project>;
}

// useProject loads a single project by slug. Used by SettingsPage to
// render details + drive the rename/retention update form.
//
// Distinguishes 404 from other errors via Status — 'not-found' lets
// the page render a "project doesn't exist" panel without spamming the
// generic error banner.
export function useProject(slug: string): UseProjectReturn {
  const [project, setProject] = useState<Project | null>(null);
  const [status, setStatus] = useState<Status>('loading');
  const [error, setError] = useState<string | null>(null);
  const [tick, setTick] = useState(0);

  const reload = useCallback(() => setTick((t) => t + 1), []);

  useEffect(() => {
    if (!slug) {
      setStatus('loading');
      return;
    }
    let cancelled = false;
    setStatus('loading');
    setError(null);
    getProject(slug)
      .then((p) => {
        if (cancelled) return;
        setProject(p);
        setStatus('ready');
      })
      .catch((err: Error) => {
        if (cancelled) return;
        if (err instanceof AuthExpiredError) return;
        if (err instanceof HttpError && err.status === 404) {
          setProject(null);
          setStatus('not-found');
          return;
        }
        setError(err.message);
        setStatus('error');
      });
    return () => {
      cancelled = true;
    };
  }, [slug, tick]);

  const update = useCallback(
    async (req: UpdateProjectRequest) => {
      const updated = await apiUpdate(slug, req);
      setProject(updated);
      return updated;
    },
    [slug],
  );

  return { project, status, error, reload, update };
}
