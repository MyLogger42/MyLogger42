import { LevelHistogram } from './LevelHistogram';
import { LEVEL_COLORS } from './LevelBadge';
import { BUCKET_OPTIONS, type LogLevel, type LogStats as Stats } from '../api/types';
import type { MessageGroup } from '../api/types';

interface Props {
  stats: Stats | null;
  loading: boolean;
  bucketSeconds: number;
  onBucketChange: (seconds: number) => void;
  // Filter hand-offs. Every panel is a filter control: clicking a bar,
  // a level chip or a repeated message narrows the list below.
  onSelectRange: (from: Date, to: Date) => void;
  onSelectLevel: (level: LogLevel) => void;
  onSelectMessage: (group: MessageGroup) => void;
}

// Level order for the totals strip: severity descending, so Error/Fatal
// (what people scan for) lead.
const CHIP_ORDER: LogLevel[] = [
  'Fatal',
  'Error',
  'Warning',
  'Event',
  'Info',
  'Debug',
  'Verbose',
];

// LogStats is the panel above the log list: a level-over-time histogram,
// the per-level totals strip, and the repeated-message ranking. All three
// describe the SAME filtered range as the list, and all three are
// clickable filter controls.
export function LogStats({
  stats,
  loading,
  bucketSeconds,
  onBucketChange,
  onSelectRange,
  onSelectLevel,
  onSelectMessage,
}: Props) {
  const totals = stats?.totals ?? {};
  const present = CHIP_ORDER.filter((l) => (totals[l] ?? 0) > 0);
  const grand = present.reduce((a, l) => a + (totals[l] ?? 0), 0);

  return (
    <section className={`stats ${loading ? 'stats-busy' : ''}`}>
      <header className="stats-head">
        <h2 className="stats-title">Activity</h2>
        <div className="stats-buckets" role="group" aria-label="Histogram bucket size">
          {BUCKET_OPTIONS.map((opt) => (
            <button
              key={opt.seconds}
              type="button"
              className={`bucket-btn ${bucketSeconds === opt.seconds ? 'bucket-btn-on' : ''}`}
              aria-pressed={bucketSeconds === opt.seconds}
              onClick={() => onBucketChange(opt.seconds)}
            >
              {opt.label}
            </button>
          ))}
        </div>
      </header>

      <LevelHistogram
        series={stats?.series ?? []}
        bucketSeconds={stats?.bucketSeconds ?? bucketSeconds}
        onSelectRange={onSelectRange}
      />

      {/* The chips double as the chart's legend — colour never carries
          identity on its own. */}
      <div className="level-chips">
        {present.length === 0 ? (
          <span className="level-chips-empty">No entries match.</span>
        ) : (
          present.map((level) => (
            <button
              key={level}
              type="button"
              className="level-chip"
              onClick={() => onSelectLevel(level)}
              title={`Filter to ${level}`}
            >
              <span
                className="level-chip-dot"
                style={{ background: LEVEL_COLORS[level].bg }}
              />
              <span className="level-chip-name">{level}</span>
              <span className="level-chip-count">
                {(totals[level] ?? 0).toLocaleString()}
              </span>
            </button>
          ))
        )}
        {grand > 0 ? (
          <span className="level-chips-total">{grand.toLocaleString()} total</span>
        ) : null}
      </div>

      <div className="noise">
        <h3 className="noise-title">
          Most repeated
          <span className="noise-sub">
            identical service + level + message in this range
          </span>
        </h3>
        {stats && stats.topMessages.length > 0 ? (
          <ol className="noise-list">
            {stats.topMessages.map((g) => (
              <li key={`${g.service}|${g.level}|${g.message}`}>
                <button
                  type="button"
                  className="noise-row"
                  onClick={() => onSelectMessage(g)}
                  title="Filter the list to this message"
                >
                  <span
                    className="noise-count"
                    style={{ borderLeftColor: LEVEL_COLORS[g.level]?.bg ?? '#6b7280' }}
                  >
                    ×{g.count.toLocaleString()}
                  </span>
                  <span className="noise-service">{g.service}</span>
                  <span className="noise-message">{g.message}</span>
                  <span className="noise-span">{spanLabel(g)}</span>
                </button>
              </li>
            ))}
          </ol>
        ) : (
          <p className="noise-empty">Nothing repeated in this range.</p>
        )}
      </div>
    </section>
  );
}

// spanLabel answers "over what window did this repeat?" — 400 identical
// errors in 30 seconds is a very different story from 400 over a week.
function spanLabel(g: MessageGroup): string {
  const first = new Date(g.firstSeenAt).getTime();
  const last = new Date(g.lastSeenAt).getTime();
  if (!Number.isFinite(first) || !Number.isFinite(last)) return '';
  const secs = Math.max(0, Math.round((last - first) / 1000));
  if (g.count < 2) return 'once';
  if (secs < 60) return `over ${secs}s`;
  if (secs < 3600) return `over ${Math.round(secs / 60)}m`;
  if (secs < 86400) return `over ${Math.round(secs / 3600)}h`;
  return `over ${Math.round(secs / 86400)}d`;
}
