import { useMemo, useState } from 'react';
import { LEVEL_COLORS } from './LevelBadge';
import type { LevelBucket, LogLevel } from '../api/types';

interface Props {
  series: LevelBucket[];
  bucketSeconds: number;
  // Called when a column is clicked, with that bucket's [start, end).
  // The page turns it into the from/to filter.
  onSelectRange: (from: Date, to: Date) => void;
}

// Stack order, top of the column down. Severity descending, so the
// levels people are hunting for sit at the top of every bar and the
// Verbose/Debug noise sinks to the bottom.
const STACK_ORDER: LogLevel[] = [
  'Fatal',
  'Error',
  'Warning',
  'Event',
  'Info',
  'Debug',
  'Verbose',
];

// Fatal's badge colour (#7c1d6f) only reaches 1.84:1 against the row
// background — as a badge it carries white text, but as a bare fill it
// disappears. The chart uses a brighter step of the same hue; everything
// else is the shared LEVEL_COLORS map so a colour means one thing across
// badges, chips and bars.
const CHART_LEVEL_COLORS: Record<LogLevel, string> = {
  ...(Object.fromEntries(
    Object.entries(LEVEL_COLORS).map(([lvl, c]) => [lvl, c.bg]),
  ) as Record<LogLevel, string>),
  Fatal: '#c026d3',
};

// MAX_FILLED_COLUMNS caps gap filling. Sparse buckets come back from the
// server with the empty ones omitted; we re-insert them so the time axis
// is linear, but a filter spanning a year at 1m buckets would be half a
// million columns. Past the cap we render only the buckets that have
// data and say so — the shape is still readable, the spacing isn't
// linear.
const MAX_FILLED_COLUMNS = 400;

// MAX_COLUMNS is how many bars fit across the panel before they stop
// being readable: 12h at the 1m bucket is 720 columns, which at a 2px
// minimum width plus gaps overflows the page sideways. Past the cap we
// merge adjacent buckets by an integer factor and label the axis with
// the effective bucket size, rather than scrolling the chart or letting
// the columns collapse into a smear.
const MAX_COLUMNS = 180;

// LevelHistogram renders counts-per-bucket as stacked bars. Plain
// divs, not SVG: the bars are proportional rectangles, so CSS
// percentages do the layout for free and the chart is responsive with no
// viewBox arithmetic.
export function LevelHistogram({ series, bucketSeconds, onSelectRange }: Props) {
  const [hover, setHover] = useState<number | null>(null);

  const { columns, gapsFilled, step, truncated } = useMemo(() => {
    const filled = fillGaps(series, bucketSeconds);
    if (!filled.gapsFilled) {
      // Buckets aren't contiguous, so merging adjacent ones would fuse
      // instants that are hours apart and hand the click a range the
      // column doesn't describe. Show the newest columns instead.
      const columns = filled.columns.slice(-MAX_COLUMNS);
      return {
        ...filled,
        columns,
        step: bucketSeconds,
        truncated: columns.length < filled.columns.length,
      };
    }
    const merged = mergeToFit(filled.columns, bucketSeconds);
    return { ...filled, ...merged, truncated: false };
  }, [series, bucketSeconds]);

  const totals = columns.map((c) => sumCounts(c));
  const peak = Math.max(1, ...totals);

  if (columns.length === 0) {
    return <div className="hist-empty">No entries in this range.</div>;
  }

  const active = hover === null ? null : columns[hover];

  return (
    <div className="hist">
      <div className="hist-plot" onMouseLeave={() => setHover(null)}>
        <span className="hist-peak" aria-hidden="true">
          {peak.toLocaleString()}
        </span>
        {columns.map((col, i) => {
          const total = totals[i];
          const start = new Date(col.start);
          const end = new Date(start.getTime() + step * 1000);
          return (
            <button
              key={col.start}
              type="button"
              className={`hist-col ${hover === i ? 'hist-col-hover' : ''}`}
              onMouseEnter={() => setHover(i)}
              onFocus={() => setHover(i)}
              onClick={() => onSelectRange(start, end)}
              // The visual is colour-only; the label is how a screen
              // reader (and a keyboard user tabbing the chart) gets it.
              aria-label={`${formatBucket(start, step)}: ${total} entries. Filter to this window.`}
            >
              <span className="hist-stack">
                {STACK_ORDER.map((level) => {
                  const n = col.counts[level] ?? 0;
                  if (n === 0) return null;
                  return (
                    <span
                      key={level}
                      className="hist-seg"
                      style={{
                        height: `${(n / peak) * 100}%`,
                        background: CHART_LEVEL_COLORS[level],
                      }}
                    />
                  );
                })}
              </span>
            </button>
          );
        })}
        {active ? (
          <div
            className="hist-tip"
            style={{
              left: `clamp(80px, ${((hover! + 0.5) / columns.length) * 100}%, calc(100% - 80px))`,
            }}
            role="status"
          >
            <div className="hist-tip-time">
              {formatBucket(new Date(active.start), step)}
            </div>
            {STACK_ORDER.filter((l) => (active.counts[l] ?? 0) > 0).map((l) => (
              <div key={l} className="hist-tip-row">
                <span
                  className="hist-tip-dot"
                  style={{ background: CHART_LEVEL_COLORS[l] }}
                />
                <span className="hist-tip-level">{l}</span>
                <span className="hist-tip-count">
                  {(active.counts[l] ?? 0).toLocaleString()}
                </span>
              </div>
            ))}
            <div className="hist-tip-total">
              {sumCounts(active).toLocaleString()} total
            </div>
          </div>
        ) : null}
      </div>

      <div className="hist-axis">
        <span>{formatBucket(new Date(columns[0].start), step)}</span>
        <span className="hist-axis-note">
          {gapsFilled ? null : 'gaps omitted — spacing is not linear'}
          {truncated ? ` · newest ${MAX_COLUMNS} shown` : null}
          {step !== bucketSeconds ? ` merged to ${stepLabel(step)} columns` : null}
        </span>
        <span>
          {formatBucket(new Date(columns[columns.length - 1].start), step)}
        </span>
      </div>
    </div>
  );
}

// fillGaps re-inserts the empty buckets the server omits, so a quiet
// hour reads as a gap rather than being squeezed out of the axis.
// Returns gapsFilled=false when the span is too wide to fill (see
// MAX_FILLED_COLUMNS) and the caller should label the axis as uneven.
function fillGaps(
  series: LevelBucket[],
  bucketSeconds: number,
): { columns: LevelBucket[]; gapsFilled: boolean } {
  if (series.length < 2) return { columns: series, gapsFilled: true };
  const step = bucketSeconds * 1000;
  const first = new Date(series[0].start).getTime();
  const last = new Date(series[series.length - 1].start).getTime();
  const span = Math.floor((last - first) / step) + 1;
  if (span > MAX_FILLED_COLUMNS) return { columns: series, gapsFilled: false };

  const byStart = new Map(
    series.map((b) => [new Date(b.start).getTime(), b] as const),
  );
  const columns: LevelBucket[] = [];
  for (let t = first; t <= last; t += step) {
    columns.push(byStart.get(t) ?? { start: new Date(t).toISOString(), counts: {} });
  }
  return { columns, gapsFilled: true };
}

// mergeToFit folds adjacent columns by an integer factor until they fit
// MAX_COLUMNS, returning the effective bucket size. Merging keeps the
// axis linear — dropping or truncating columns would not. Callers must
// pass a CONTIGUOUS run (post-fillGaps): merging non-adjacent instants
// would produce a column whose click range doesn't hold its own data.
function mergeToFit(
  columns: LevelBucket[],
  bucketSeconds: number,
): { columns: LevelBucket[]; step: number } {
  if (columns.length <= MAX_COLUMNS) return { columns, step: bucketSeconds };
  const factor = Math.ceil(columns.length / MAX_COLUMNS);
  const out: LevelBucket[] = [];
  for (let i = 0; i < columns.length; i += factor) {
    const counts: Partial<Record<LogLevel, number>> = {};
    for (const col of columns.slice(i, i + factor)) {
      for (const [lvl, n] of Object.entries(col.counts)) {
        const level = lvl as LogLevel;
        counts[level] = (counts[level] ?? 0) + (n ?? 0);
      }
    }
    out.push({ start: columns[i].start, counts });
  }
  return { columns: out, step: bucketSeconds * factor };
}

// stepLabel renders a bucket size for the axis note.
function stepLabel(seconds: number): string {
  if (seconds % 86400 === 0) return `${seconds / 86400}d`;
  if (seconds % 3600 === 0) return `${seconds / 3600}h`;
  if (seconds % 60 === 0) return `${seconds / 60}m`;
  return `${seconds}s`;
}

function sumCounts(bucket: LevelBucket): number {
  return Object.values(bucket.counts).reduce((a, b) => a + (b ?? 0), 0);
}

// formatBucket labels a bucket start at the precision the bucket size
// justifies — a day bucket labelled "14:00" would be a lie.
function formatBucket(d: Date, bucketSeconds: number): string {
  if (bucketSeconds >= 86400) {
    return d.toLocaleDateString(undefined, { month: 'short', day: '2-digit' });
  }
  return d.toLocaleString(undefined, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  });
}
