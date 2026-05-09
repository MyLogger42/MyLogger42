import type { LogLevel } from '../api/types';

// Documented colour map — kept in one place so badges, filter chips and
// the live-tail row backgrounds stay visually consistent.
export const LEVEL_COLORS: Record<LogLevel, { bg: string; fg: string }> = {
  Verbose: { bg: '#9ca3af', fg: '#111827' }, // gray
  Debug: { bg: '#3b82f6', fg: '#ffffff' }, // blue
  Info: { bg: '#16a34a', fg: '#ffffff' }, // green
  Event: { bg: '#06b6d4', fg: '#062a2e' }, // cyan
  Warning: { bg: '#eab308', fg: '#1c1917' }, // yellow
  Error: { bg: '#dc2626', fg: '#ffffff' }, // red
  Fatal: { bg: '#7c1d6f', fg: '#ffffff' }, // purple/dark-red
};

interface Props {
  level: LogLevel;
}

export function LevelBadge({ level }: Props) {
  const c = LEVEL_COLORS[level] ?? { bg: '#6b7280', fg: '#fff' };
  return (
    <span
      className="level-badge"
      style={{ backgroundColor: c.bg, color: c.fg }}
      aria-label={`level ${level}`}
    >
      {level}
    </span>
  );
}
