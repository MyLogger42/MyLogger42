import { useState } from 'react';
import type { LogEntry } from '../api/types';
import { LEVEL_COLORS, LevelBadge } from './LevelBadge';
import { LogDetail } from './LogDetail';

interface Props {
  items: LogEntry[];
  emptyMessage?: string;
  // Fold consecutive identical entries into one row. Off for the nested
  // list inside an expanded group (that list IS the members).
  collapseRepeats?: boolean;
}

// LogList renders the result rows for the history page or the live
// stream. Clicking a row toggles an inline LogDetail panel for that
// entry; only one row is expanded at a time.
//
// Consecutive entries with an identical (service, level, message) fold
// into a single row carrying a ×N badge — a service retrying the same
// failure 40 times in a row is one line of signal, not 40. Expanding a
// folded row lists its members. Matching is exact and adjacent-only, so
// nothing that isn't literally the same message is ever hidden, and time
// order is never rearranged; the "Most repeated" panel above covers
// repeats that are spread out.
export function LogList({
  items,
  emptyMessage = 'No logs match your filters.',
  collapseRepeats = true,
}: Props) {
  const [expandedId, setExpandedId] = useState<string | null>(null);

  if (items.length === 0) {
    return <div className="log-list-empty">{emptyMessage}</div>;
  }

  const groups = collapseRepeats ? groupAdjacent(items) : items.map((e) => [e]);

  return (
    <ul className="log-list">
      {groups.map((group) => {
        const entry = group[0];
        const isOpen = expandedId === entry.id;
        const accent = LEVEL_COLORS[entry.level]?.bg ?? '#6b7280';
        const repeats = group.length;
        return (
          <li key={entry.id} className={`log-row ${isOpen ? 'log-row-open' : ''}`}>
            <button
              type="button"
              className="log-row-summary"
              style={{ borderLeftColor: accent }}
              onClick={() => setExpandedId(isOpen ? null : entry.id)}
              aria-expanded={isOpen}
            >
              <span className="log-row-time" title={entry.timestamp}>
                {formatTimestamp(entry.timestamp)}
              </span>
              <LevelBadge level={entry.level} />
              <span className="log-row-service">{entry.service}</span>
              <span className="log-row-content">
                <span className="log-row-message">{entry.message}</span>
                <MetadataChips metadata={entry.metadata} />
              </span>
              {repeats > 1 ? (
                <span
                  className="log-row-repeat"
                  aria-label={`repeated ${repeats} times in a row`}
                >
                  ×{repeats}
                </span>
              ) : null}
            </button>
            {isOpen ? (
              repeats > 1 ? (
                <div className="log-row-group">
                  <div className="log-row-group-head">
                    {repeats} consecutive identical entries
                  </div>
                  <LogList items={group} collapseRepeats={false} />
                </div>
              ) : (
                <LogDetail entry={entry} onClose={() => setExpandedId(null)} />
              )
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}

// groupAdjacent folds runs of identical entries. Identity is the exact
// (service, level, message) triple — no normalising of ids or numbers in
// the message, so two different failures never collapse into one.
export function groupAdjacent(items: LogEntry[]): LogEntry[][] {
  const groups: LogEntry[][] = [];
  for (const entry of items) {
    const last = groups[groups.length - 1];
    if (last && sameShape(last[0], entry)) {
      last.push(entry);
    } else {
      groups.push([entry]);
    }
  }
  return groups;
}

function sameShape(a: LogEntry, b: LogEntry): boolean {
  return a.service === b.service && a.level === b.level && a.message === b.message;
}

const PREVIEW_META_KEYS = [
  'requestId',
  'traceId',
  'userId',
  'status',
  'err',
  'duration',
] as const;

function MetadataChips({ metadata }: { metadata: Record<string, unknown> }) {
  if (!metadata) return null;
  const chips: { key: string; value: string }[] = [];
  for (const key of PREVIEW_META_KEYS) {
    if (!Object.prototype.hasOwnProperty.call(metadata, key)) continue;
    const formatted = formatMetaValue(metadata[key]);
    if (formatted !== null) {
      chips.push({ key, value: formatted });
    }
  }
  if (chips.length === 0) return null;

  return (
    <span className="log-row-meta">
      {chips.map(({ key, value }) => (
        <span
          key={key}
          className={`log-meta-chip${key === 'err' ? ' log-meta-chip-err' : ''}`}
          title={`${key}: ${value}`}
        >
          <span className="log-meta-key">{key}</span>
          <span className="log-meta-val">{value}</span>
        </span>
      ))}
    </span>
  );
}

function formatMetaValue(value: unknown): string | null {
  if (value === null || value === undefined) return null;
  if (typeof value === 'string') {
    return value.length > 0 ? value : null;
  }
  if (typeof value === 'number' || typeof value === 'boolean') {
    return String(value);
  }
  try {
    return JSON.stringify(value);
  } catch {
    return null;
  }
}

// formatTimestamp displays the entry timestamp in the user's local TZ
// with millisecond precision (YYYY-MM-DD HH:mm:ss.SSS) so rapid events
// within the same second are distinguishable. The full RFC3339 string
// is available in the title tooltip and the LogDetail panel.
function formatTimestamp(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const yyyy = d.getFullYear();
  const mm = String(d.getMonth() + 1).padStart(2, '0');
  const dd = String(d.getDate()).padStart(2, '0');
  const hh = String(d.getHours()).padStart(2, '0');
  const min = String(d.getMinutes()).padStart(2, '0');
  const ss = String(d.getSeconds()).padStart(2, '0');
  const ms = String(d.getMilliseconds()).padStart(3, '0');
  return `${yyyy}-${mm}-${dd} ${hh}:${min}:${ss}.${ms}`;
}
