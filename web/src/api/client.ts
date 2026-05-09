import type { LogEntry, LogsQuery, QueryResponse, SourcesResponse } from './types';

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

async function asJson<T>(res: Response): Promise<T> {
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
    throw new Error(msg);
  }
  return (await res.json()) as T;
}

// fetchLogs queries the paginated history endpoint.
export async function fetchLogs(query: LogsQuery): Promise<QueryResponse> {
  const params = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) {
    if (v === undefined || v === null || v === '') continue;
    params.set(k, String(v));
  }
  const qs = params.toString();
  const res = await fetch(url(`/api/v1/logs${qs ? `?${qs}` : ''}`));
  return asJson<QueryResponse>(res);
}

export async function fetchLogById(id: string): Promise<LogEntry> {
  const res = await fetch(url(`/api/v1/logs/${encodeURIComponent(id)}`));
  return asJson<LogEntry>(res);
}

export async function fetchSources(): Promise<SourcesResponse> {
  const res = await fetch(url('/api/v1/sources'));
  return asJson<SourcesResponse>(res);
}
