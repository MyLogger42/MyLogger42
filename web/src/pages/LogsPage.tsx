import { useMemo, useState } from 'react';
import { useParams } from 'react-router-dom';
import { FilterBar, localToRfc3339, type FilterValues } from '../components/FilterBar';
import { LogList } from '../components/LogList';
import { useLogs } from '../hooks/useLogs';
import { useSources } from '../hooks/useSources';
import type { LogsQuery } from '../api/types';

const PAGE_SIZE = 50;

const initialFilter: FilterValues = {
  service: '',
  level: '',
  search: '',
  from: '',
  to: '',
  levels: [],
};

// LogsPage is the per-project history view. The active project comes
// from the URL (/projects/:slug/logs); the FilterBar + useLogs +
// LogList stack are unchanged from v1, just scoped.
export function LogsPage() {
  const { slug = '' } = useParams();
  const { sources } = useSources(slug);
  const [filter, setFilter] = useState<FilterValues>(initialFilter);

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

  const { items, total, limit, offset, loading, error, setOffset } = useLogs(query);

  const totalPages = limit > 0 ? Math.max(1, Math.ceil(total / limit)) : 1;
  const currentPage = limit > 0 ? Math.floor(offset / limit) + 1 : 1;

  return (
    <section className="page page-logs">
      <FilterBar
        sources={sources}
        initial={initialFilter}
        onChange={setFilter}
        busy={loading}
      />

      {error ? <div className="banner banner-error">Error: {error}</div> : null}

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
