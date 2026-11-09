import type {
  AccessTokensResponse,
  AuthMe,
  Health,
  CreateProjectRequest,
  CreateProjectResponse,
  IssueKeyRequest,
  IssuedKey,
  LogEntry,
  LoginRequest,
  LogsQuery,
  LogStats,
  StatsQuery,
  Project,
  ProjectKeysResponse,
  ProjectsResponse,
  QueryResponse,
  SourcesResponse,
  UpdateProjectRequest,
} from './types';

// Base URL for API calls. In dev Vite proxies /api to localhost:8080
// (see vite.config.ts), and in production the SPA is served from the
// same origin as the API by the Go binary, so the empty default works.
// Set VITE_API_URL to point at a different host (e.g. when running the
// SPA against a remote backend during dev).
const baseUrl: string = (import.meta.env.VITE_API_URL ?? '').replace(/\/$/, '');

function url(path: string): string {
  return baseUrl + path;
}

// wsBaseUrl translates the http(s) base to ws(s) for the live-tail
// WebSocket. When baseUrl is empty (same-origin) we derive from the
// current page's location.
export function wsUrl(path: string): string {
  if (baseUrl) {
    return baseUrl.replace(/^http/, 'ws') + path;
  }
  const proto = window.location.protocol === 'https:' ? 'wss' : 'ws';
  return `${proto}://${window.location.host}${path}`;
}

// authExpiredEvent is fired whenever an API call returns 401, so
// top-level UI code can redirect to /login. We don't import a router
// here — staying decoupled lets the listener live wherever the SPA
// shell sits.
export const AUTH_EXPIRED = 'mylogger42:auth-expired' as const;

// AuthExpiredError is thrown by asJson() when the server returns 401.
// Hooks treat it as a "not signed in" signal and the global event
// listener triggers a navigation to /login.
export class AuthExpiredError extends Error {
  constructor() {
    super('session expired');
    this.name = 'AuthExpiredError';
  }
}

// HttpError carries the parsed server error message and the status
// code so hooks/components can render targeted messaging.
export class HttpError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = 'HttpError';
    this.status = status;
  }
}

async function asJson<T>(res: Response): Promise<T> {
  if (res.status === 401) {
    // Single source of truth for "the cookie is gone." Notify the
    // shell so it can redirect, and throw so the caller stops.
    window.dispatchEvent(new CustomEvent(AUTH_EXPIRED));
    throw new AuthExpiredError();
  }
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = await res.json();
      if (body && typeof body === 'object' && 'error' in body) {
        msg = String((body as { error: unknown }).error);
      }
    } catch {
      // ignore — keep status text fallback
    }
    throw new HttpError(res.status, msg);
  }
  // No body to parse: 204 by spec, or any 2xx that the server emitted
  // with Content-Length: 0 (or no body at all). We used to assume only
  // 204 fell into this bucket, but 200-with-Set-Cookie-and-no-body is
  // an idiom for cookie-issuing endpoints — calling res.json() on
  // those threw "Unexpected end of JSON input." Tolerating both shapes
  // is more robust and costs nothing.
  if (res.status === 204 || res.headers.get('content-length') === '0') {
    return undefined as T;
  }
  // Read the raw text so an unexpectedly-empty body doesn't blow up
  // res.json(); fall through to undefined for callers typing void.
  const text = await res.text();
  if (text === '') {
    return undefined as T;
  }
  return JSON.parse(text) as T;
}

// jsonRequest is the canonical fetch wrapper. credentials:'include' is
// the v2 default — every request that depends on the session cookie
// must include it, and harmlessly carries it on the few that don't.
async function jsonRequest<T>(
  path: string,
  init: RequestInit = {},
): Promise<T> {
  const res = await fetch(url(path), {
    ...init,
    credentials: 'include',
    headers: {
      'Content-Type': 'application/json',
      ...(init.headers ?? {}),
    },
  });
  return asJson<T>(res);
}

// --- read endpoints (project-scoped) ----------------------------------

export async function fetchLogs(query: LogsQuery): Promise<QueryResponse> {
  return jsonRequest<QueryResponse>(`/api/v1/logs?${queryParams(query)}`);
}

// fetchStats is the aggregate sibling of fetchLogs: same filter params,
// counts instead of rows. Backs the histogram / totals / top-messages
// panels in one round trip.
export async function fetchStats(query: StatsQuery): Promise<LogStats> {
  return jsonRequest<LogStats>(`/api/v1/stats?${queryParams(query)}`);
}

// queryParams drops empty values so an unset filter doesn't turn into
// `service=` (which the server would read as a real, never-matching
// filter value).
function queryParams(query: LogsQuery | StatsQuery): string {
  const params = new URLSearchParams();
  for (const [k, v] of Object.entries(query as Record<string, unknown>)) {
    if (v === undefined || v === null || v === '') continue;
    params.set(k, String(v));
  }
  return params.toString();
}

export async function fetchLogById(projectSlug: string, id: string): Promise<LogEntry> {
  const params = new URLSearchParams({ project: projectSlug });
  return jsonRequest<LogEntry>(
    `/api/v1/logs/${encodeURIComponent(id)}?${params.toString()}`,
  );
}

export async function fetchSources(projectSlug: string): Promise<SourcesResponse> {
  const params = new URLSearchParams({ project: projectSlug });
  return jsonRequest<SourcesResponse>(`/api/v1/sources?${params.toString()}`);
}

export async function fetchHealth(): Promise<Health> {
  return jsonRequest<Health>('/api/v1/health');
}

// --- auth -------------------------------------------------------------

export async function login(req: LoginRequest): Promise<void> {
  // Bypass jsonRequest so a 401 (wrong credentials) throws HttpError
  // instead of AuthExpiredError — the UI would otherwise show "session
  // expired" for a plain bad-password attempt.
  const res = await fetch(url('/api/v1/auth/login'), {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  });
  if (res.status === 401) {
    throw new HttpError(401, 'invalid credentials');
  }
  return asJson<void>(res);
}

export async function logout(): Promise<void> {
  await jsonRequest<void>('/api/v1/auth/logout', { method: 'POST' });
}

export async function fetchMe(): Promise<AuthMe> {
  return jsonRequest<AuthMe>('/api/v1/auth/me');
}

// --- admin: projects --------------------------------------------------

export async function listProjects(): Promise<ProjectsResponse> {
  return jsonRequest<ProjectsResponse>('/api/v1/admin/projects');
}

export async function getProject(slug: string): Promise<Project> {
  return jsonRequest<Project>(`/api/v1/admin/projects/${encodeURIComponent(slug)}`);
}

export async function createProject(
  req: CreateProjectRequest,
): Promise<CreateProjectResponse> {
  return jsonRequest<CreateProjectResponse>('/api/v1/admin/projects', {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

export async function updateProject(
  slug: string,
  req: UpdateProjectRequest,
): Promise<Project> {
  return jsonRequest<Project>(`/api/v1/admin/projects/${encodeURIComponent(slug)}`, {
    method: 'PATCH',
    body: JSON.stringify(req),
  });
}

export async function archiveProject(slug: string): Promise<Project> {
  return jsonRequest<Project>(
    `/api/v1/admin/projects/${encodeURIComponent(slug)}/archive`,
    { method: 'POST' },
  );
}

export async function unarchiveProject(slug: string): Promise<Project> {
  return jsonRequest<Project>(
    `/api/v1/admin/projects/${encodeURIComponent(slug)}/unarchive`,
    { method: 'POST' },
  );
}

// --- admin: keys ------------------------------------------------------

export async function listKeys(slug: string): Promise<ProjectKeysResponse> {
  return jsonRequest<ProjectKeysResponse>(
    `/api/v1/admin/projects/${encodeURIComponent(slug)}/keys`,
  );
}

export async function issueKey(slug: string, req: IssueKeyRequest): Promise<IssuedKey> {
  return jsonRequest<IssuedKey>(
    `/api/v1/admin/projects/${encodeURIComponent(slug)}/keys`,
    {
      method: 'POST',
      body: JSON.stringify(req),
    },
  );
}

export async function revokeKey(slug: string, keyId: string): Promise<void> {
  await jsonRequest<void>(
    `/api/v1/admin/projects/${encodeURIComponent(slug)}/keys/${encodeURIComponent(keyId)}`,
    { method: 'DELETE' },
  );
}

// --- admin: MCP access tokens -----------------------------------------

export async function listTokens(): Promise<AccessTokensResponse> {
  return jsonRequest<AccessTokensResponse>('/api/v1/admin/tokens');
}

export async function issueToken(req: IssueKeyRequest): Promise<IssuedKey> {
  return jsonRequest<IssuedKey>('/api/v1/admin/tokens', {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

export async function revokeToken(id: string): Promise<void> {
  await jsonRequest<void>(`/api/v1/admin/tokens/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  });
}
