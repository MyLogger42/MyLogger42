import { useNavigate, useParams } from 'react-router-dom';
import type { Project } from '../api/types';

interface Props {
  projects: Project[];
  // pageSection is the trailing path segment ("logs" / "live" /
  // "settings"). Switching projects preserves the current section so a
  // user looking at settings for one project lands on settings for the
  // next. Per the v2 design.
  pageSection: string;
}

// ProjectSwitcher is the top-bar dropdown for picking the active
// project. Lists active (non-archived) projects only — archived
// projects are reachable via /projects but not from the daily-driver
// switcher. Bottom item is "+ New project" which navigates to the
// projects page where the create modal lives.
export function ProjectSwitcher({ projects, pageSection }: Props) {
  const navigate = useNavigate();
  const params = useParams();
  const currentSlug = params.slug ?? '';

  const active = projects.filter((p) => p.archivedAt === null);

  return (
    <select
      className="project-switcher"
      value={currentSlug}
      onChange={(e) => {
        const v = e.target.value;
        if (v === '__new__') {
          navigate('/projects');
          return;
        }
        navigate(`/projects/${encodeURIComponent(v)}/${pageSection}`);
        // localStorage so the root-redirect can land here next time.
        try {
          window.localStorage.setItem('mylogger42:lastProject', v);
        } catch {
          // private-mode / quota — best-effort
        }
      }}
    >
      {currentSlug === '' || !active.some((p) => p.slug === currentSlug) ? (
        <option value="" disabled>
          (select a project)
        </option>
      ) : null}
      {active.map((p) => (
        <option key={p.slug} value={p.slug}>
          {p.name}
        </option>
      ))}
      <option value="__new__">+ New project</option>
    </select>
  );
}
