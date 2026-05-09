import { useState } from 'react';
import type { LogEntry } from '../api/types';
import { LEVEL_COLORS, LevelBadge } from './LevelBadge';
import { LogDetail } from './LogDetail';

interface Props {
  items: LogEntry[];
  emptyMessage?: string;
}

// LogList renders the result rows for the history page or the live
// stream. Clicking a row toggles an inline LogDetail panel for that
// entry; only one row is expanded at a time.
export function LogList({ items, emptyMessage = 'No logs match your filters.' }: Props) {
  const [expandedId, setExpandedId] = useState<string | null>(null);

  if (items.length === 0) {
    return <div className="log-list-empty">{emptyMessage}</div>;
  }

  return (
    <ul className="log-list">
      {items.map((entry) => {
        const isOpen = expandedId === entry.id;
        const accent = LEVEL_COLORS[entry.level]?.bg ?? '#6b7280';
        return (
          <li key={entry.id} className={`log-row ${isOpen ? 'log-row-open' : ''}`}>
            <button
              type="button"
              className="log-row-summary"
              style={{ borderLeftColor: accent }}
              onClick={() => setExpandedId(isOpen ? null : entry.id)}
              aria-expanded={isOpen}
            >
              <span className="log-row-time">{formatTimestamp(entry.timestamp)}</span>
              <LevelBadge level={entry.level} />
              <span className="log-row-service">{entry.service}</span>
              <span className="log-row-message">{entry.message}</span>
            </button>
            {isOpen ? <LogDetail entry={entry} onClose={() => setExpandedId(null)} /> : null}
          </li>
        );
      })}
    </ul>
  );
}

// formatTimestamp displays the entry timestamp in the user's local TZ
// at second precision. The full RFC3339 string is still available in
// the LogDetail panel.
function formatTimestamp(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
}
