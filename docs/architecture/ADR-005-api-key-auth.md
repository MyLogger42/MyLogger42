# ADR-005: API Key Auth for Ingest, Open UI

> **Status:** Superseded by [ADR-007](./ADR-007-per-project-keys-and-session-auth.md) (v2 multi-project). The single-key, open-UI model below was the v1 design; v2 replaces both halves.

## Context

MyLogger42 needs to prevent arbitrary internet traffic from injecting fake log events. The web UI does not need login — it is accessed by the developer only, behind a reverse proxy or VPN. Auth must be simple to configure in any HTTP client (curl, SDKs, etc.).

## Options Considered

| Option | Pros | Cons |
|--------|------|------|
| **Static API key (header)** | Zero client SDK dependency; works in every language; easy to rotate by changing `.env` | Single key — no per-client granularity; key in env means restart to rotate |
| JWT | Per-client identity; expiry built in | Requires token issuance flow; overkill for solo use |
| mTLS | Very strong; certificate-based | Complex to set up in dev clients; certificate management overhead |
| No auth | Simplest | Anyone can inject or read logs if port is exposed |

## Decision

**Static API key in the `X-API-Key` HTTP header, required on all ingest endpoints. Web UI is open (no auth).**

The VPS is assumed to be behind a firewall or reverse proxy (Nginx/Caddy) that restricts the UI to trusted IPs or a VPN. The ingest endpoint is internet-accessible so clients can push from anywhere — the API key is the sole protection.

Key is stored in `.env` as `INGEST_API_KEY` and read at startup. A missing or wrong key returns `401 Unauthorized`.

## Consequences

- Middleware on the ingest route checks `X-API-Key` header against `INGEST_API_KEY` env var
- Query and stream endpoints (read-only) do not require auth — UI is trusted
- `.env.example` documents the key; `.env` is gitignored
- To rotate: update `INGEST_API_KEY` in `.env` and restart the container
- Future: could add multiple keys (map of name→key) without changing the interface contract
