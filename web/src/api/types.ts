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
