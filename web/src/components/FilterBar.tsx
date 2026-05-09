import { useEffect, useState } from 'react';
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

interface Props {
  sources: Source[];
  initial: FilterValues;
  multiLevel?: boolean;
  onChange: (next: FilterValues) => void;
  busy?: boolean;
}

// FilterBar is shared between the LogsPage and LiveTailPage. The
// history page uses a single-select level dropdown (server contract is
// one level), the live-tail page uses a multi-select via the `levels`
// array (the LiveTailFilter accepts a list).
export function FilterBar({ sources, initial, multiLevel, onChange, busy }: Props) {
  const [v, setV] = useState<FilterValues>(initial);

  // Reset local state when the parent swaps the initial value out.
  useEffect(() => {
    setV(initial);
  }, [initial]);

  // Debounce text/search changes so we don't fire a request per keystroke.
  useEffect(() => {
    const id = window.setTimeout(() => onChange(v), 250);
    return () => window.clearTimeout(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [JSON.stringify(v)]);

  function toggleLevel(level: LogLevel) {
    setV((prev) => ({
      ...prev,
      levels: prev.levels.includes(level)
        ? prev.levels.filter((l) => l !== level)
        : [...prev.levels, level],
    }));
  }

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

      {busy ? <span className="filter-busy">Loading...</span> : null}
    </div>
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
