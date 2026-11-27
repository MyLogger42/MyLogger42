import { useEffect, useMemo, useRef, useState } from 'react';
import { useParams } from 'react-router-dom';
import { ActiveFilterPills } from '../components/ActiveFilterPills';
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
import { exportLogs, type ExportFormat } from '../api/client';
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
  const [exportOpen, setExportOpen] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState<string | null>(null);
  const [exportTruncated, setExportTruncated] = useState(false);
  const exportRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!exportOpen) return;
    const onDocClick = (e: MouseEvent) => {
      if (exportRef.current && !exportRef.current.contains(e.target as Node)) {
        setExportOpen(false);
      }
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setExportOpen(false);
    };
    document.addEventListener('mousedown', onDocClick);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDocClick);
      document.removeEventListener('keydown', onKey);
    };
  }, [exportOpen]);

  const handleExport = async (format: ExportFormat) => {
    setExportOpen(false);
    setExportError(null);
    setExportTruncated(false);
    setExporting(true);
    try {
      const { limit: _l, offset: _o, ...exportQuery } = query;
      const res = await exportLogs(exportQuery, format);
      setExportTruncated(res.truncated);
    } catch (err) {
      setExportError(err instanceof Error ? err.message : 'export failed');
    } finally {
      setExporting(false);
    }
  };

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
      {exportError ? (
        <div className="banner banner-error">Export error: {exportError}</div>
      ) : null}
      {exportTruncated ? (
        <div className="banner">Export capped at 10,000 rows. Narrow your filters to export all matching entries.</div>
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
        <div className="logs-meta-right">
          <span>
            Page {currentPage} of {totalPages}
          </span>
          <div className="export-dropdown" ref={exportRef}>
            <button
              type="button"
              className="btn-secondary"
              disabled={exporting || loading || total === 0}
              onClick={() => setExportOpen((o) => !o)}
              aria-expanded={exportOpen}
              aria-haspopup="menu"
            >
              {exporting ? 'Exporting…' : 'Export ▾'}
            </button>
            {exportOpen ? (
              <div className="export-menu" role="menu">
                <button
                  type="button"
                  role="menuitem"
                  onClick={() => void handleExport('ndjson')}
                >
                  Export NDJSON
                </button>
                <button
                  type="button"
                  role="menuitem"
                  onClick={() => void handleExport('csv')}
                >
                  Export CSV
                </button>
              </div>
            ) : null}
          </div>
        </div>
      </div>

      <ActiveFilterPills filter={filter} onChange={setFilter} />

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
