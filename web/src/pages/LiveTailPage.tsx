import { useEffect, useRef, useState } from 'react';
import { useParams } from 'react-router-dom';
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

// LiveTailPage is the per-project live tail. The slug from the URL
// (/projects/:slug/live) is the WebSocket filter's required project
// component; user-driven service/level filters layer on top.
export function LiveTailPage() {
  const { slug = '' } = useParams();
  const { sources } = useSources(slug);
  const [filter, setFilter] = useState<FilterValues>(initialFilter);
  const { entries, state, connect, disconnect, clear, setFilter: pushFilter } = useLiveTail({
    projects: slug ? [slug] : [],
  });
  const [autoScroll, setAutoScroll] = useState(true);
  const listEndRef = useRef<HTMLDivElement | null>(null);

  // Whenever the local filter changes, push a fresh frame.
  useEffect(() => {
    if (!slug) return;
    pushFilter({
      projects: [slug],
      services: filter.service ? [filter.service] : undefined,
      levels: filter.levels.length > 0 ? filter.levels : undefined,
    });
  }, [slug, filter, pushFilter]);

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
