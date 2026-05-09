import { useState } from 'react';
import type { LogEntry } from '../api/types';
import { LevelBadge } from './LevelBadge';

interface Props {
  entry: LogEntry;
  onClose?: () => void;
}

// LogDetail renders a single entry with its full metadata JSON. Used as
// an expanded row inside LogList — not a route — to keep navigation in
// the URL focused on the query, not the selection.
export function LogDetail({ entry, onClose }: Props) {
  const [copied, setCopied] = useState(false);

  function copyJson() {
    navigator.clipboard.writeText(JSON.stringify(entry, null, 2)).then(
      () => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1500);
      },
      () => {
        // Clipboard write can fail (insecure context, perms); ignore.
      },
    );
  }

  return (
    <div className="log-detail">
      <header className="log-detail-header">
        <LevelBadge level={entry.level} />
        <code className="log-detail-id">{entry.id}</code>
        <button type="button" onClick={copyJson}>
          {copied ? 'Copied' : 'Copy JSON'}
        </button>
        {onClose ? (
          <button type="button" onClick={onClose}>
            Close
          </button>
        ) : null}
      </header>
      <dl className="log-detail-grid">
        <dt>service</dt>
        <dd>{entry.service}</dd>
        <dt>timestamp</dt>
        <dd>{entry.timestamp}</dd>
        <dt>received</dt>
        <dd>{entry.receivedAt}</dd>
        <dt>message</dt>
        <dd className="log-detail-message">{entry.message}</dd>
      </dl>
      <details open>
        <summary>metadata</summary>
        <pre className="log-detail-json">{JSON.stringify(entry.metadata, null, 2)}</pre>
      </details>
    </div>
  );
}
