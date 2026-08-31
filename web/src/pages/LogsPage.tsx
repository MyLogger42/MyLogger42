import { useMemo, useState } from 'react';
import { useParams } from 'react-router-dom';
import {
  FilterBar,
  localToRfc3339,
  rfc3339ToLocal,
  type FilterValues,
} from '../components/FilterBar';
import { LogList } from '../components/LogList';
import { LogStats } from '../components/LogStats';
import { useLogs } from '../hooks/useLogs';
import { useStats } from '../hooks/useStats';
import { useSources } from '../hooks/useSources';
import type { LogLevel, LogsQuery, MessageGroup, StatsQuery } from '../api/types';

const PAGE_SIZE = 50;

const initialFilter: FilterValues = {
  service: '',
  level: '',
  search: '',
  from: '',
  to: '',
  levels: [],
};

// LogsPage is the per-project history view: the stats panel on top
// (histogram / level totals / most-repeated), the filtered list below.
//
// `filter` is the single source of truth for both. It is passed back into
// FilterBar as `initial`, which is what lets the stats panel act as a set
// of filter controls — clicking a histogram bar or a level chip writes
// into the same state the form edits.
export function LogsPage() {
  const { slug = '' } = useParams();
  const { sources } = useSources(slug);
  const [filter, setFilter] = useState<FilterValues>(initialFilter);
  const [bucket, setBucket] = useState(3600);

  const query = useMemo<LogsQuery>(() => {
    return {
      project: slug,
      service: filter.service || undefined,
      level: filter.level || undefined,
      search: filter.search || undefined,
      from: localToRfc3339(filter.from) || undefined,
      to: localToRfc3339(filter.to) || undefined,
      limit: PAGE_SIZE,
      offset: 0,
      sort: 'desc',
    };
  }, [slug, filter]);

  // Same filter, no paging: the panels describe the whole matched range.
  const statsQuery = useMemo<StatsQuery>(() => {
    const { limit: _l, offset: _o, sort: _s, ...rest } = query;
    return { ...rest, bucket };
  }, [query, bucket]);

  const { items, total, limit, offset, loading, error, setOffset } = useLogs(query);
  const { stats, loading: statsLoading, error: statsError } = useStats(statsQuery);

  const totalPages = limit > 0 ? Math.max(1, Math.ceil(total / limit)) : 1;
  const currentPage = limit > 0 ? Math.floor(offset / limit) + 1 : 1;

  return (
    <section className="page page-logs">
      <FilterBar
        sources={sources}
        initial={filter}
        onChange={setFilter}
        busy={loading}
      />

      {error ? <div className="banner banner-error">Error: {error}</div> : null}
      {statsError ? (
        <div className="banner banner-error">Stats error: {statsError}</div>
      ) : null}

      <LogStats
        stats={stats}
        loading={statsLoading}
        bucketSeconds={bucket}
        onBucketChange={setBucket}
        onSelectRange={(from, to) =>
          setFilter((f) => ({
            ...f,
            from: rfc3339ToLocal(from),
            to: rfc3339ToLocal(to),
          }))
        }
        onSelectLevel={(level: LogLevel) =>
          // Clicking the active level again clears it — the chip is a
          // toggle, otherwise there's no way back to "all levels"
          // without hunting for the dropdown.
          setFilter((f) => ({ ...f, level: f.level === level ? '' : level }))
        }
        onSelectMessage={(g: MessageGroup) =>
          setFilter((f) => ({
            ...f,
            service: g.service,
            level: g.level,
            search: g.message,
          }))
        }
      />

      <div className="logs-meta">
        <span>{total} entries match</span>
        <span>
          Page {currentPage} of {totalPages}
        </span>
      </div>

      <LogList items={items} />

      <div className="pager">
        <button
          type="button"
          disabled={offset === 0 || loading}
          onClick={() => setOffset(Math.max(0, offset - limit))}
        >
          Previous
        </button>
        <button
          type="button"
          disabled={offset + limit >= total || loading}
          onClick={() => setOffset(offset + limit)}
        >
          Next
        </button>
      </div>
    </section>
  );
}
