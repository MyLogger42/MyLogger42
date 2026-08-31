import { useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { KeyRevealModal } from '../components/KeyRevealModal';
import { useCapabilities } from '../hooks/useCapabilities';
import { useProject } from '../hooks/useProject';
import { useProjectKeys } from '../hooks/useProjectKeys';
import { useProjects } from '../hooks/useProjects';
import type { IssuedKey, Project, ProjectKey } from '../api/types';

// SettingsPage is a per-project admin view. Three sections stacked:
// project details (name + retention), ingest keys (table + issue),
// and danger zone (archive / unarchive). Hard delete is CLI-only.
export function SettingsPage() {
  const { slug = '' } = useParams();
  const { project, status, update } = useProject(slug);
  const { keys, issue, revoke } = useProjectKeys(slug);
  const { archive, unarchive } = useProjects();
  const [revealed, setRevealed] = useState<IssuedKey | null>(null);

  if (status === 'loading') {
    return <div className="page"><p>Loading...</p></div>;
  }
  if (status === 'not-found') {
    return (
      <div className="page">
        <div className="banner banner-error">Project not found.</div>
      </div>
    );
  }
  if (status === 'error' || !project) {
    return (
      <div className="page">
        <div className="banner banner-error">Couldn't load project.</div>
      </div>
    );
  }

  return (
    <section className="page page-settings">
      <ProjectDetailsForm project={project} onUpdate={update} />
      <KeysSection
        keys={keys}
        onIssue={async (label) => {
          const issued = await issue(label);
          setRevealed(issued);
          return issued;
        }}
        onRevoke={revoke}
      />
      <DangerZone project={project} onArchive={archive} onUnarchive={unarchive} />

      {revealed ? (
        <KeyRevealModal issued={revealed} onClose={() => setRevealed(null)} />
      ) : null}
    </section>
  );
}

interface ProjectDetailsFormProps {
  project: Project;
  onUpdate: (req: { name?: string; retentionDays?: number | null }) => Promise<Project>;
}

function ProjectDetailsForm({ project, onUpdate }: ProjectDetailsFormProps) {
  const { perProjectRetention, storage } = useCapabilities();
  const [name, setName] = useState(project.name);
  const [retention, setRetention] = useState(
    project.retentionDays === null ? '' : String(project.retentionDays),
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    setSaved(false);
    try {
      const r = retention.trim();
      const retentionDays = r === '' ? null : Number(r);
      if (r !== '' && (Number.isNaN(retentionDays) || (retentionDays as number) < 0)) {
        throw new Error('retention must be a non-negative integer');
      }
      await onUpdate({ name: name.trim(), retentionDays });
      setSaved(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'update failed');
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="settings-section">
      <h2>Project details</h2>
      <form className="filter-bar" onSubmit={onSubmit}>
        <label className="filter-field">
          <span>Slug</span>
          <input type="text" value={project.slug} disabled readOnly />
          <small>Slug is immutable.</small>
        </label>
        <label className="filter-field">
          <span>Name</span>
          <input
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
          />
        </label>
        <label className="filter-field">
          <span>Retention (days, blank = inherit global)</span>
          <input
            type="number"
            min={0}
            value={retention}
            onChange={(e) => setRetention(e.target.value)}
          />
          {perProjectRetention ? null : (
            <small className="filter-busy">
              Not enforced on the {storage} backend &mdash; RETENTION_DAYS applies globally.
            </small>
          )}
        </label>
        <div className="filter-field-grow" style={{ display: 'flex', gap: 8, alignItems: 'flex-end' }}>
          <button type="submit" disabled={busy}>
            {busy ? 'Saving...' : 'Save'}
          </button>
          {saved ? <span className="filter-busy">Saved</span> : null}
          {error ? <span className="banner banner-error">{error}</span> : null}
        </div>
      </form>
    </section>
  );
}

interface KeysSectionProps {
  keys: ProjectKey[];
  onIssue: (label: string) => Promise<IssuedKey>;
  onRevoke: (keyId: string) => Promise<void>;
}

function KeysSection({ keys, onIssue, onRevoke }: KeysSectionProps) {
  const [issuing, setIssuing] = useState(false);
  const [label, setLabel] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submitIssue(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await onIssue(label.trim() || 'cli');
      setLabel('');
      setIssuing(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'issue failed');
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="settings-section">
      <h2>Ingest keys</h2>
      {keys.length === 0 ? (
        <p>No keys yet. Issue one to start ingesting.</p>
      ) : (
        <table className="keys-table">
          <thead>
            <tr>
              <th>Prefix</th>
              <th>Label</th>
              <th>Created</th>
              <th>Last used</th>
              <th>Status</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {keys.map((k) => (
              <tr key={k.id} className={k.revokedAt ? 'key-revoked' : ''}>
                <td><code>{k.prefix}</code></td>
                <td>{k.label}</td>
                <td>{new Date(k.createdAt).toLocaleString()}</td>
                <td>{k.lastUsedAt ? new Date(k.lastUsedAt).toLocaleString() : '—'}</td>
                <td>{k.revokedAt ? 'Revoked' : 'Active'}</td>
                <td>
                  {k.revokedAt ? null : (
                    <button
                      type="button"
                      className="button-danger"
                      onClick={() => {
                        if (window.confirm(`Revoke key ${k.prefix}? Clients using it will start getting 401.`)) {
                          void onRevoke(k.id);
                        }
                      }}
                    >
                      Revoke
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {issuing ? (
        <form className="filter-bar" onSubmit={submitIssue}>
          <label className="filter-field filter-field-grow">
            <span>Label</span>
            <input
              type="text"
              placeholder="prod-deploy"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              autoFocus
            />
          </label>
          <div style={{ display: 'flex', gap: 8, alignItems: 'flex-end' }}>
            <button type="submit" disabled={busy}>
              {busy ? 'Issuing...' : 'Issue'}
            </button>
            <button type="button" onClick={() => setIssuing(false)} disabled={busy}>
              Cancel
            </button>
          </div>
          {error ? <span className="banner banner-error">{error}</span> : null}
        </form>
      ) : (
        <button type="button" onClick={() => setIssuing(true)}>
          Issue new key
        </button>
      )}
    </section>
  );
}

interface DangerZoneProps {
  project: Project;
  onArchive: (slug: string) => Promise<Project>;
  onUnarchive: (slug: string) => Promise<Project>;
}

function DangerZone({ project, onArchive, onUnarchive }: DangerZoneProps) {
  const navigate = useNavigate();
  const isArchived = project.archivedAt !== null;
  return (
    <section className="settings-section settings-danger">
      <h2>Danger zone</h2>
      {isArchived ? (
        <>
          <p>
            This project is archived. Ingest is rejected with 403. Existing
            entries remain queryable until retention expires them.
          </p>
          <button
            type="button"
            onClick={() => {
              void onUnarchive(project.slug);
            }}
          >
            Unarchive
          </button>
        </>
      ) : (
        <>
          <p>
            Archiving stops new ingest for this project. Existing entries
            stay readable. Reversible — you can unarchive later.
          </p>
          <button
            type="button"
            className="button-danger"
            onClick={() => {
              if (window.confirm(`Archive project "${project.name}"? Ingest will start returning 403.`)) {
                void onArchive(project.slug).then(() => navigate('/projects'));
              }
            }}
          >
            Archive project
          </button>
        </>
      )}
    </section>
  );
}
