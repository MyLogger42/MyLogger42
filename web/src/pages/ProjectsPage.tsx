import { useState } from 'react';
import { Link } from 'react-router-dom';
import { KeyRevealModal } from '../components/KeyRevealModal';
import { useCapabilities } from '../hooks/useCapabilities';
import { useProjects } from '../hooks/useProjects';
import type { CreateProjectResponse, Project } from '../api/types';

// ProjectsPage is the post-login landing place. Renders an empty state
// the first time you land here (no projects yet) or a list of active
// projects + an archived section. The "+ New project" CTA opens an
// inline form panel rather than a modal — same visual weight as
// editing existing settings.
export function ProjectsPage() {
  const { projects, loading, error, create } = useProjects();
  const [creating, setCreating] = useState(false);
  const [revealed, setRevealed] = useState<CreateProjectResponse | null>(null);

  const active = projects.filter((p) => p.archivedAt === null);
  const archived = projects.filter((p) => p.archivedAt !== null);

  if (loading && projects.length === 0) {
    return <div className="page page-projects"><p>Loading projects...</p></div>;
  }
  if (error) {
    return (
      <div className="page page-projects">
        <div className="banner banner-error">Error: {error}</div>
      </div>
    );
  }

  return (
    <section className="page page-projects">
      {projects.length === 0 ? (
        <EmptyState onCreate={() => setCreating(true)} />
      ) : (
        <>
          <header className="projects-header">
            <h1>Projects</h1>
            <button type="button" onClick={() => setCreating(true)}>
              + New project
            </button>
          </header>

          <ProjectList title="Active" projects={active} />
          {archived.length > 0 ? (
            <ProjectList title="Archived" projects={archived} />
          ) : null}
        </>
      )}

      {creating ? (
        <CreateProjectForm
          onCancel={() => setCreating(false)}
          onCreated={(res) => {
            setCreating(false);
            // Show the issued key once via the same reveal modal used
            // on the Settings page's issue-key flow.
            setRevealed(res);
          }}
          onCreate={create}
        />
      ) : null}

      {revealed ? (
        <KeyRevealModal issued={revealed.key} onClose={() => setRevealed(null)} />
      ) : null}
    </section>
  );
}

function EmptyState({ onCreate }: { onCreate: () => void }) {
  return (
    <div className="empty-state">
      <h1>No projects yet</h1>
      <p>
        A project is an isolated bucket of log entries with its own
        ingest keys and retention window. Create one to start logging.
      </p>
      <button type="button" onClick={onCreate}>
        Create your first project
      </button>
    </div>
  );
}

function ProjectList({ title, projects }: { title: string; projects: Project[] }) {
  return (
    <section className="projects-section">
      <h2>{title}</h2>
      <ul className="projects-list">
        {projects.map((p) => (
          <li key={p.slug}>
            <Link
              to={`/projects/${encodeURIComponent(p.slug)}/logs`}
              className={`project-card ${p.archivedAt ? 'project-card-archived' : ''}`}
            >
              <div className="project-card-name">{p.name}</div>
              <div className="project-card-slug">{p.slug}</div>
              <div className="project-card-meta">
                {p.retentionDays === null
                  ? 'inherits global retention'
                  : `${p.retentionDays} day retention`}
              </div>
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}

interface CreateFormProps {
  onCancel: () => void;
  onCreated: (res: CreateProjectResponse) => void;
  onCreate: (req: {
    slug: string;
    name: string;
    retentionDays?: number | null;
  }) => Promise<CreateProjectResponse>;
}

function CreateProjectForm({ onCancel, onCreated, onCreate }: CreateFormProps) {
  const { perProjectRetention, storage } = useCapabilities();
  const [slug, setSlug] = useState('');
  const [name, setName] = useState('');
  const [retentionDays, setRetentionDays] = useState<string>('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const r = retentionDays.trim();
      const retention = r === '' ? null : Number(r);
      if (r !== '' && (Number.isNaN(retention) || (retention as number) < 0)) {
        throw new Error('retention must be a non-negative integer');
      }
      const res = await onCreate({ slug: slug.trim(), name: name.trim(), retentionDays: retention });
      onCreated(res);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'create failed');
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="modal-backdrop">
      <form className="modal" onSubmit={onSubmit}>
        <h2 className="modal-title">New project</h2>
        <label className="filter-field">
          <span>Slug</span>
          <input
            type="text"
            placeholder="auth-service"
            value={slug}
            onChange={(e) => setSlug(e.target.value)}
            autoFocus
            required
          />
          <small>Lowercase letters, digits, dashes. Starts with a letter. 2-63 chars.</small>
        </label>
        <label className="filter-field">
          <span>Name</span>
          <input
            type="text"
            placeholder="Auth Service"
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
          />
        </label>
        <label className="filter-field">
          <span>Retention (days, blank = inherit global default)</span>
          <input
            type="number"
            min={0}
            placeholder="14"
            value={retentionDays}
            onChange={(e) => setRetentionDays(e.target.value)}
          />
          {perProjectRetention ? null : (
            <small className="filter-busy">
              Not enforced on the {storage} backend &mdash; RETENTION_DAYS applies globally.
            </small>
          )}
        </label>

        {error ? <div className="banner banner-error">{error}</div> : null}

        <div className="modal-actions">
          <button type="button" onClick={onCancel} disabled={busy}>
            Cancel
          </button>
          <button type="submit" disabled={busy}>
            {busy ? 'Creating...' : 'Create project'}
          </button>
        </div>
      </form>
    </div>
  );
}
