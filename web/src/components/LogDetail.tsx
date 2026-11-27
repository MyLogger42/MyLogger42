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

  const tsMs = Date.parse(entry.timestamp);
  const receivedMs = Date.parse(entry.receivedAt);
  const ingestLagMs =
    Number.isFinite(tsMs) && Number.isFinite(receivedMs) ? receivedMs - tsMs : null;

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
        <dd>
          {entry.timestamp}
          <RelativeTime iso={entry.timestamp} />
        </dd>
        <dt>received</dt>
        <dd>
          {entry.receivedAt}
          <RelativeTime iso={entry.receivedAt} />
        </dd>
        {ingestLagMs !== null && ingestLagMs > 1000 ? (
          <>
            <dt>ingest lag</dt>
            <dd className="log-detail-lag">{formatLag(ingestLagMs)}</dd>
          </>
        ) : null}
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

function RelativeTime({ iso }: { iso: string }) {
  const rel = formatRelativeTime(iso);
  if (!rel) return null;
  return <span className="log-detail-rel"> ({rel})</span>;
}

function formatRelativeTime(iso: string, nowMs = Date.now()): string | null {
  const ts = Date.parse(iso);
  if (!Number.isFinite(ts)) return null;
  const diffMs = nowMs - ts;
  if (diffMs < 0) {
    const aheadSec = Math.floor(-diffMs / 1000);
    return aheadSec < 1 ? 'just now' : `in ${aheadSec}s`;
  }
  if (diffMs < 1000) return 'just now';
  const sec = Math.floor(diffMs / 1000);
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const days = Math.floor(hr / 24);
  return `${days}d ago`;
}

function formatLag(lagMs: number): string {
  if (lagMs < 60_000) {
    return `+${(lagMs / 1000).toFixed(1)}s`;
  }
  const totalSec = Math.floor(lagMs / 1000);
  const min = Math.floor(totalSec / 60);
  const sec = totalSec % 60;
  if (min < 60) {
    return `+${min}m ${sec}s`;
  }
  const hr = Math.floor(min / 60);
  const remMin = min % 60;
  return `+${hr}h ${remMin}m`;
}
