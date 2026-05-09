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
  service?: string;
  level?: LogLevel;
  from?: string;
  to?: string;
  search?: string;
  limit?: number;
  offset?: number;
  sort?: 'asc' | 'desc';
}

export interface LiveTailFilter {
  services?: string[];
  levels?: LogLevel[];
}
