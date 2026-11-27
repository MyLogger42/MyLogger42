import { useEffect, useRef, useState } from 'react';
import { ALL_LEVELS, type LogLevel, type Source } from '../api/types';

export interface FilterValues {
  service: string;
  // For the history page only one level filter is supported by the
  // server (single ?level=...). For multi-select we pass it through to
  // the live-tail filter object instead.
  level: LogLevel | '';
  search: string;
  from: string;
  to: string;
  // Used by live tail; ignored by the history page.
  levels: LogLevel[];
}

export const EMPTY_FILTER: FilterValues = {
  service: '',
  level: '',
  search: '',
  from: '',
  to: '',
  levels: [],
};

const TIME_PRESETS: { label: string; ms: number }[] = [
  { label: '15m', ms: 15 * 60 * 1000 },
  { label: '1h', ms: 60 * 60 * 1000 },
  { label: '6h', ms: 6 * 60 * 60 * 1000 },
  { label: '24h', ms: 24 * 60 * 60 * 1000 },
  { label: '7d', ms: 7 * 24 * 60 * 60 * 1000 },
];

interface Props {
  sources: Source[];
  initial: FilterValues;
  multiLevel?: boolean;
  timePresets?: boolean;
  onChange: (next: FilterValues) => void;
  busy?: boolean;
}

// FilterBar is shared between the LogsPage and LiveTailPage. The
// history page uses a single-select level dropdown (server contract is
// one level), the live-tail page uses a multi-select via the `levels`
// array (the LiveTailFilter accepts a list).
export function FilterBar({
  sources,
  initial,
  multiLevel,
  timePresets,
  onChange,
  busy,
}: Props) {
  const [v, setV] = useState<FilterValues>(initial);
  const lastEmittedRef = useRef<string>(JSON.stringify(initial));

  // Sync local state when the parent swaps `initial` (URL navigation,
  // histogram click, chip toggle, etc.) without re-emitting onChange.
  useEffect(() => {
    const serialized = JSON.stringify(initial);
    lastEmittedRef.current = serialized;
    setV(initial);
  }, [initial]);

  // Debounce text/search changes so we don't fire a request per keystroke.
  const serializedV = JSON.stringify(v);
  useEffect(() => {
    if (serializedV === lastEmittedRef.current) return;
    const id = window.setTimeout(() => {
      lastEmittedRef.current = serializedV;
      onChange(v);
    }, 250);
    return () => window.clearTimeout(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [serializedV]);

  function applyImmediate(next: FilterValues) {
    lastEmittedRef.current = JSON.stringify(next);
    setV(next);
    onChange(next);
  }

  function applyPreset(ms: number) {
    const from = rfc3339ToLocal(new Date(Date.now() - ms));
    applyImmediate({ ...v, from, to: '' });
  }

  function toggleLevel(level: LogLevel) {
    setV((prev) => ({
      ...prev,
      levels: prev.levels.includes(level)
        ? prev.levels.filter((l) => l !== level)
        : [...prev.levels, level],
    }));
  }

  const hasActiveFilters = Boolean(
    v.service || v.level || v.search || v.from || v.to || v.levels.length > 0,
  );

  return (
    <div className="filter-bar">
      <label className="filter-field">
        <span>Service</span>
        <select
          value={v.service}
          onChange={(e) => setV({ ...v, service: e.target.value })}
        >
          <option value="">All services</option>
          {sources.map((s) => (
            <option key={s.name} value={s.name}>
              {s.name} ({s.totalEntries})
            </option>
          ))}
        </select>
      </label>

      {multiLevel ? (
        <fieldset className="filter-field filter-levels">
          <legend>Levels</legend>
          <div className="filter-level-grid">
            {ALL_LEVELS.map((level) => (
              <label key={level} className="filter-level-item">
                <input
                  type="checkbox"
                  checked={v.levels.includes(level)}
                  onChange={() => toggleLevel(level)}
                />
                <span>{level}</span>
              </label>
            ))}
          </div>
        </fieldset>
      ) : (
        <label className="filter-field">
          <span>Level</span>
          <select
            value={v.level}
            onChange={(e) => setV({ ...v, level: e.target.value as LogLevel | '' })}
          >
            <option value="">All levels</option>
            {ALL_LEVELS.map((level) => (
              <option key={level} value={level}>
                {level}
              </option>
            ))}
          </select>
        </label>
      )}

      <label className="filter-field">
        <span>From</span>
        <input
          type="datetime-local"
          value={v.from}
          onChange={(e) => setV({ ...v, from: e.target.value })}
        />
      </label>

      <label className="filter-field">
        <span>To</span>
        <input
          type="datetime-local"
          value={v.to}
          onChange={(e) => setV({ ...v, to: e.target.value })}
        />
      </label>

      <label className="filter-field filter-field-grow">
        <span>Search message</span>
        <input
          type="search"
          placeholder="substring match"
          value={v.search}
          onChange={(e) => setV({ ...v, search: e.target.value })}
        />
      </label>

      <div className="filter-actions">
        {timePresets ? (
          <div className="filter-presets" role="group" aria-label="Quick time range">
            <span className="filter-presets-label">Range</span>
            {TIME_PRESETS.map((preset) => (
              <button
                key={preset.label}
                type="button"
                className="preset-btn"
                onClick={() => applyPreset(preset.ms)}
              >
                {preset.label}
              </button>
            ))}
          </div>
        ) : (
          <span />
        )}

        <div className="filter-actions-right">
          {busy ? <span className="filter-busy">Loading...</span> : null}
          <button
            type="button"
            className="filter-clear-btn"
            disabled={!hasActiveFilters}
            onClick={() => applyImmediate(EMPTY_FILTER)}
          >
            Clear filters
          </button>
        </div>
      </div>
    </div>
  );
}

// rfc3339ToLocal is the inverse of localToRfc3339: it renders an instant
// as the local-time "YYYY-MM-DDTHH:mm" string an <input
// type="datetime-local"> expects. Used when the histogram hands a bucket
// window back to the filter — toISOString() would set the field to UTC
// and silently shift the range by the user's offset.
export function rfc3339ToLocal(d: Date): string {
  if (Number.isNaN(d.getTime())) return '';
  const pad = (n: number) => String(n).padStart(2, '0');
  return (
    `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}` +
    `T${pad(d.getHours())}:${pad(d.getMinutes())}`
  );
}

// localToRfc3339 turns the value of an <input type="datetime-local"> —
// which is a local-time string with no offset — into an RFC3339 UTC
// string the server can parse. Returns empty string if input is empty.
export function localToRfc3339(local: string): string {
  if (!local) return '';
  const d = new Date(local);
  if (Number.isNaN(d.getTime())) return '';
  return d.toISOString();
}
