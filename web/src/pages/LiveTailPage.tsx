import { useEffect, useRef, useState } from 'react';
import { FilterBar, type FilterValues } from '../components/FilterBar';
import { LogList } from '../components/LogList';
import { useLiveTail } from '../hooks/useLiveTail';
import { useSources } from '../hooks/useSources';

const initialFilter: FilterValues = {
  service: '',
  level: '',
  search: '',
  from: '',
  to: '',
  levels: [],
};

// LiveTailPage opens a WebSocket to /api/v1/logs/stream and renders
// every entry the server broadcasts that matches the current filter.
// Connect/disconnect is explicit so the user can pause the stream.
export function LiveTailPage() {
  const { sources } = useSources();
  const [filter, setFilter] = useState<FilterValues>(initialFilter);
  const { entries, state, connect, disconnect, clear, setFilter: pushFilter } = useLiveTail();
  const [autoScroll, setAutoScroll] = useState(true);
  const listEndRef = useRef<HTMLDivElement | null>(null);

  // Whenever the local filter changes, push the new LiveTailFilter to
  // the server. The server applies it for subsequent broadcasts; we
  // also clear the local buffer so the visible list matches the filter.
  useEffect(() => {
    pushFilter({
      services: filter.service ? [filter.service] : undefined,
      levels: filter.levels.length > 0 ? filter.levels : undefined,
    });
  }, [filter, pushFilter]);

  // Auto-scroll to the newest entry when enabled. The user can disable
  // auto-scroll to inspect older rows without being yanked away.
  useEffect(() => {
    if (!autoScroll) return;
    listEndRef.current?.scrollIntoView({ behavior: 'smooth', block: 'end' });
  }, [entries, autoScroll]);

  return (
    <section className="page page-live">
      <FilterBar
        sources={sources}
        initial={initialFilter}
        multiLevel
        onChange={setFilter}
      />

      <div className="live-controls">
        <span className={`live-state live-state-${state}`}>{stateLabel(state)}</span>
        {state === 'open' ? (
          <button type="button" onClick={disconnect}>
            Disconnect
          </button>
        ) : (
          <button type="button" onClick={connect}>
            Connect
          </button>
        )}
        <button type="button" onClick={clear}>
          Clear
        </button>
        <label className="live-autoscroll">
          <input
            type="checkbox"
            checked={autoScroll}
            onChange={(e) => setAutoScroll(e.target.checked)}
          />
          Auto-scroll
        </label>
        <span className="live-count">{entries.length} buffered</span>
      </div>

      <LogList
        items={entries}
        emptyMessage={
          state === 'open'
            ? 'Connected — waiting for matching entries...'
            : 'Click Connect to start streaming.'
        }
      />
      <div ref={listEndRef} />
    </section>
  );
}

function stateLabel(state: string): string {
  switch (state) {
    case 'open':
      return 'Connected';
    case 'connecting':
      return 'Connecting...';
    case 'closed':
      return 'Disconnected';
    case 'error':
      return 'Connection error';
    default:
      return 'Idle';
  }
}
