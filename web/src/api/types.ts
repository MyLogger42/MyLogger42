// Shared API/domain types. Mirrors the Go domain package — keep these
// in sync if the wire format changes.

export type LogLevel =
  | 'Verbose'
  | 'Debug'
  | 'Info'
  | 'Event'
  | 'Warning'
  | 'Error'
  | 'Fatal';

export const ALL_LEVELS: LogLevel[] = [
  'Verbose',
  'Debug',
  'Info',
  'Event',
  'Warning',
  'Error',
  'Fatal',
];

export interface LogEntry {
  id: string;
  projectId: string;
  service: string;
  level: LogLevel;
  message: string;
  timestamp: string;
  metadata: Record<string, unknown>;
  receivedAt: string;
}

export interface Source {
  name: string;
  firstSeenAt: string;
  lastSeenAt: string;
  totalEntries: number;
}

// --- stats (GET /api/v1/stats) ----------------------------------------
// Mirrors domain.LogStats. Counts maps are partial: a level with no
// entries in a bucket is absent rather than zero.

export interface LevelBucket {
  start: string;
  counts: Partial<Record<LogLevel, number>>;
}

export interface MessageGroup {
  service: string;
  level: LogLevel;
  message: string;
  count: number;
  firstSeenAt: string;
  lastSeenAt: string;
}

export interface LogStats {
  bucketSeconds: number;
  series: LevelBucket[];
  totals: Partial<Record<LogLevel, number>>;
  topMessages: MessageGroup[];
}

// Bucket sizes the server accepts (domain.StatsBuckets). Anything else
// is a 400 — the value lands in a GROUP BY divisor server-side.
export const BUCKET_OPTIONS: { seconds: number; label: string }[] = [
  { seconds: 60, label: '1m' },
  { seconds: 300, label: '5m' },
  { seconds: 3600, label: '1h' },
  { seconds: 86400, label: '1d' },
];

// StatsQuery is LogsQuery minus paging (stats always describe the whole
// filtered range) plus the histogram bucket size.
export type StatsQuery = Omit<LogsQuery, 'limit' | 'offset' | 'sort'> & {
  bucket?: number;
};

export interface QueryResponse {
  items: LogEntry[];
  total: number;
  limit: number;
  offset: number;
}

export interface SourcesResponse {
  sources: Source[];
}

export interface LogsQuery {
  // Required server-side; the client tags every request with the active
  // project slug. Hooks always pass it.
  project: string;
  service?: string;
  level?: LogLevel;
  from?: string;
  to?: string;
  search?: string;
  limit?: number;
  offset?: number;
  sort?: 'asc' | 'desc';
}

// Wire shape for the WebSocket first-frame filter. The server
// resolves `projects` slugs to project IDs internally.
export interface LiveTailFilter {
  projects: string[]; // slugs; required non-empty
  services?: string[];
  levels?: LogLevel[];
}

// --- v2 admin / auth types --------------------------------------------

export interface AuthMe {
  username: string;
}

export interface LoginRequest {
  username: string;
  password: string;
}

export interface Project {
  id: string;
  slug: string;
  name: string;
  retentionDays: number | null; // null = inherit global default
  createdAt: string;
  archivedAt: string | null; // null = active
}

export interface ProjectsResponse {
  projects: Project[];
}

// ProjectKey is the listing form. The plaintext value is intentionally
// absent — only the prefix is shown after issuance.
export interface ProjectKey {
  id: string;
  prefix: string;
  label: string;
  createdAt: string;
  lastUsedAt: string | null;
  revokedAt: string | null;
}

export interface ProjectKeysResponse {
  keys: ProjectKey[];
}

// IssuedKey is the create-time response, returned by both
// POST /admin/projects (the initial key) and POST /admin/projects/:slug/keys.
// The `value` field is the only place the plaintext mlk_..._... string
// appears; the SPA must show it once and not persist it.
export interface IssuedKey {
  id: string;
  prefix: string;
  label: string;
  value: string;
  createdAt: string;
}

export interface CreateProjectRequest {
  slug: string;
  name: string;
  retentionDays?: number | null;
}

export interface CreateProjectResponse {
  project: Project;
  key: IssuedKey;
}

export interface UpdateProjectRequest {
  name?: string;
  retentionDays?: number | null;
}

export interface IssueKeyRequest {
  label: string;
}

// Health is GET /api/v1/health. capabilities tells the UI which
// per-project settings the active storage backend actually enforces —
// VictoriaLogs, for instance, has one global retention window.
export interface Health {
  status: string;
  storage: string;
  uptime: string;
  projects: number;
  capabilities?: {
    perProjectRetention?: boolean;
  };
}
