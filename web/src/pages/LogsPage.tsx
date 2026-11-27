import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useParams, useSearchParams } from 'react-router-dom';
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
import {
  ALL_LEVELS,
  BUCKET_OPTIONS,
  type LogLevel,
  type LogsQuery,
  type MessageGroup,
  type StatsQuery,
} from '../api/types';

const PAGE_SIZE = 50;
const DEFAULT_BUCKET = 3600;

function parseTimeParam(raw: string | null): string {
  if (!raw) return '';
  const d = new Date(raw);
  return Number.isNaN(d.getTime()) ? '' : rfc3339ToLocal(d);
}

function parseLevelParam(raw: string | null): LogLevel | '' {
  if (!raw) return '';
  return ALL_LEVELS.includes(raw as LogLevel) ? (raw as LogLevel) : '';
}

function parseBucketParam(raw: string | null): number {
  if (!raw) return DEFAULT_BUCKET;
  const n = Number(raw);
  return BUCKET_OPTIONS.some((o) => o.seconds === n) ? n : DEFAULT_BUCKET;
}

function parseOffsetParam(raw: string | null): number {
  if (!raw) return 0;
  const n = Number(raw);
  return Number.isInteger(n) && n > 0 ? n : 0;
}

// LogsPage is the per-project history view: the stats panel on top
// (histogram / level totals / most-repeated), the filtered list below.
//
// URL search params are the single source of truth for filters, bucket
// size, and pagination offset so refreshes, back/forward, and shared
// links preserve the view.
export function LogsPage() {
  const { slug = '' } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const { sources } = useSources(slug);

  const filter = useMemo<FilterValues>(() => {
    return {
      service: searchParams.get('service') ?? '',
      level: parseLevelParam(searchParams.get('level')),
      search: searchParams.get('search') ?? '',
      from: parseTimeParam(searchParams.get('from')),
      to: parseTimeParam(searchParams.get('to')),
      levels: [],
    };
  }, [searchParams]);

  const bucket = useMemo(
    () => parseBucketParam(searchParams.get('bucket')),
    [searchParams],
  );
  const pageOffset = useMemo(
    () => parseOffsetParam(searchParams.get('offset')),
    [searchParams],
  );

  const updateParams = useCallback(
    (mutator: (next: URLSearchParams) => void) => {
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev);
        mutator(next);
        return next.toString() === prev.toString() ? prev : next;
      });
    },
    [setSearchParams],
  );

  const handleFilterChange = useCallback(
    (next: FilterValues) => {
      if (
        next.service === filter.service &&
        next.level === filter.level &&
        next.search === filter.search &&
        next.from === filter.from &&
        next.to === filter.to
      ) {
        return;
      }
      updateParams((p) => {
        if (next.service) p.set('service', next.service);
        else p.delete('service');

        if (next.level) p.set('level', next.level);
        else p.delete('level');

        if (next.search) p.set('search', next.search);
        else p.delete('search');

        const fromIso = localToRfc3339(next.from);
        if (fromIso) p.set('from', fromIso);
        else p.delete('from');

        const toIso = localToRfc3339(next.to);
        if (toIso) p.set('to', toIso);
        else p.delete('to');

        p.delete('offset');
      });
    },
    [filter, updateParams],
  );

  const query = useMemo<LogsQuery>(() => {
    return {
      project: slug,
      service: filter.service || undefined,
      level: filter.level || undefined,
      search: filter.search || undefined,
      from: localToRfc3339(filter.from) || undefined,
      to: localToRfc3339(filter.to) || undefined,
      limit: PAGE_SIZE,
      offset: pageOffset,
      sort: 'desc',
    };
  }, [slug, filter, pageOffset]);

  // Same filter, no paging: the panels describe the whole matched range.
  const statsQuery = useMemo<StatsQuery>(() => {
    const { limit: _l, offset: _o, sort: _s, ...rest } = query;
    return { ...rest, bucket };
  }, [query, bucket]);

  const { items, total, limit, offset, loading, error } = useLogs(query);
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

  const setOffset = useCallback(
    (nextOffset: number) => {
      updateParams((p) => {
        if (nextOffset <= 0) p.delete('offset');
        else p.set('offset', String(nextOffset));
      });
    },
    [updateParams],
  );

  return (
    <section className="page page-logs">
      <FilterBar
        sources={sources}
        initial={filter}
        timePresets
        onChange={handleFilterChange}
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
        onBucketChange={(seconds) =>
          updateParams((p) => {
            if (seconds === DEFAULT_BUCKET) p.delete('bucket');
            else p.set('bucket', String(seconds));
          })
        }
        onSelectRange={(from, to) =>
          updateParams((p) => {
            p.set('from', from.toISOString());
            p.set('to', to.toISOString());
            p.delete('offset');
          })
        }
        onSelectLevel={(level: LogLevel) =>
          // Clicking the active level again clears it — the chip is a
          // toggle, otherwise there's no way back to "all levels"
          // without hunting for the dropdown.
          updateParams((p) => {
            if (filter.level === level) p.delete('level');
            else p.set('level', level);
            p.delete('offset');
          })
        }
        onSelectMessage={(g: MessageGroup) =>
          updateParams((p) => {
            p.set('service', g.service);
            p.set('level', g.level);
            p.set('search', g.message);
            p.delete('offset');
          })
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

      <ActiveFilterPills filter={filter} onChange={handleFilterChange} />

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
