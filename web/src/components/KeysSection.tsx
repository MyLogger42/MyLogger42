import { useState, type ReactNode } from 'react';
import type { IssuedKey, ProjectKey } from '../api/types';

// KeysSection is the credential table + issue form shared by per-project
// ingest keys (SettingsPage) and global MCP access tokens (ProjectsPage).
// The plaintext value of a new credential is surfaced by the caller via
// KeyRevealModal; this component only ever sees prefixes.
interface KeysSectionProps {
  keys: ProjectKey[];
  onIssue: (label: string) => Promise<IssuedKey>;
  onRevoke: (keyId: string) => Promise<void>;
  title?: string;
  emptyText?: string;
  labelPlaceholder?: string;
  // MCP tokens don't track usage, so the column would always read "—".
  showLastUsed?: boolean;
  children?: ReactNode;
}

export function KeysSection({
  keys,
  onIssue,
  onRevoke,
  title = 'Ingest keys',
  emptyText = 'No keys yet. Issue one to start ingesting.',
  labelPlaceholder = 'prod-deploy',
  showLastUsed = true,
  children,
}: KeysSectionProps) {
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
      <h2>{title}</h2>
      {children}
      {keys.length === 0 ? (
        <p>{emptyText}</p>
      ) : (
        <table className="keys-table">
          <thead>
            <tr>
              <th>Prefix</th>
              <th>Label</th>
              <th>Created</th>
              {showLastUsed ? <th>Last used</th> : null}
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
                {showLastUsed ? (
                  <td>{k.lastUsedAt ? new Date(k.lastUsedAt).toLocaleString() : '—'}</td>
                ) : null}
                <td>{k.revokedAt ? 'Revoked' : 'Active'}</td>
                <td>
                  {k.revokedAt ? null : (
                    <button
                      type="button"
                      className="button-danger"
                      onClick={() => {
                        if (window.confirm(`Revoke key ${k.prefix}? Anything using it will start getting 401.`)) {
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
              placeholder={labelPlaceholder}
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
