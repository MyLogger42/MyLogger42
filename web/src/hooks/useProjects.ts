import { useCallback, useEffect, useState } from 'react';
import {
  AuthExpiredError,
  archiveProject as apiArchive,
  createProject as apiCreate,
  listProjects,
  unarchiveProject as apiUnarchive,
} from '../api/client';
import type { CreateProjectRequest, CreateProjectResponse, Project } from '../api/types';

interface UseProjectsReturn {
  projects: Project[];
  loading: boolean;
  error: string | null;
  reload: () => void;
  create: (req: CreateProjectRequest) => Promise<CreateProjectResponse>;
  archive: (slug: string) => Promise<Project>;
  unarchive: (slug: string) => Promise<Project>;
}

// useProjects wraps the admin /projects list endpoint. Includes
// archived rows; the consumer (ProjectsPage / ProjectSwitcher) filters
// client-side based on whether they want all or just active.
//
// Mutation helpers (create/archive/unarchive) call reload() on success
// so the list stays in sync without the caller plumbing it manually.
export function useProjects(): UseProjectsReturn {
  const [projects, setProjects] = useState<Project[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [tick, setTick] = useState(0);

  const reload = useCallback(() => setTick((t) => t + 1), []);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    listProjects()
      .then((res) => {
        if (cancelled) return;
        setProjects(res.projects ?? []);
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
  }, [tick]);

  const create = useCallback(
    async (req: CreateProjectRequest) => {
      const res = await apiCreate(req);
      reload();
      return res;
    },
    [reload],
  );

  const archive = useCallback(
    async (slug: string) => {
      const p = await apiArchive(slug);
      reload();
      return p;
    },
    [reload],
  );

  const unarchive = useCallback(
    async (slug: string) => {
      const p = await apiUnarchive(slug);
      reload();
      return p;
    },
    [reload],
  );

  return { projects, loading, error, reload, create, archive, unarchive };
}
