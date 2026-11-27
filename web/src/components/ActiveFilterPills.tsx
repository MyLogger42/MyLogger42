import type { FilterValues } from './FilterBar';

interface Props {
  filter: FilterValues;
  onChange: (next: FilterValues) => void;
}

interface Pill {
  key: string;
  label: string;
  onRemove: () => void;
}

// ActiveFilterPills displays the currently active filters above the log
// list as removable chips so users can see and clear individual filters
// in one click.
export function ActiveFilterPills({ filter, onChange }: Props) {
  const pills: Pill[] = [];

  if (filter.service) {
    pills.push({
      key: `service:${filter.service}`,
      label: `Service: ${filter.service}`,
      onRemove: () => onChange({ ...filter, service: '' }),
    });
  }

  if (filter.level) {
    pills.push({
      key: `level:${filter.level}`,
      label: `Level: ${filter.level}`,
      onRemove: () => onChange({ ...filter, level: '' }),
    });
  }

  for (const level of filter.levels) {
    pills.push({
      key: `levels:${level}`,
      label: `Level: ${level}`,
      onRemove: () =>
        onChange({
          ...filter,
          levels: filter.levels.filter((l) => l !== level),
        }),
    });
  }

  if (filter.from) {
    pills.push({
      key: `from:${filter.from}`,
      label: `From: ${filter.from.replace('T', ' ')}`,
      onRemove: () => onChange({ ...filter, from: '' }),
    });
  }

  if (filter.to) {
    pills.push({
      key: `to:${filter.to}`,
      label: `To: ${filter.to.replace('T', ' ')}`,
      onRemove: () => onChange({ ...filter, to: '' }),
    });
  }

  if (filter.search) {
    pills.push({
      key: `search:${filter.search}`,
      label: `Search: ${filter.search}`,
      onRemove: () => onChange({ ...filter, search: '' }),
    });
  }

  if (pills.length === 0) {
    return null;
  }

  function clearAll() {
    onChange({
      service: '',
      level: '',
      search: '',
      from: '',
      to: '',
      levels: [],
    });
  }

  return (
    <div className="active-filters" role="region" aria-label="Active filters">
      {pills.map((pill) => (
        <button
          key={pill.key}
          type="button"
          className="filter-pill"
          onClick={pill.onRemove}
          aria-label={`Remove ${pill.label} filter`}
          title={`Remove ${pill.label}`}
        >
          <span className="filter-pill-label">{pill.label}</span>
          <span className="filter-pill-x" aria-hidden="true">
            ×
          </span>
        </button>
      ))}
      {pills.length > 1 ? (
        <button
          type="button"
          className="filter-pill-clear"
          onClick={clearAll}
        >
          Clear all
        </button>
      ) : null}
    </div>
  );
}
