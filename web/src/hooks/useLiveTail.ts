import { useCallback, useEffect, useRef, useState } from 'react';
import { wsUrl } from '../api/client';
import type { LiveTailFilter, LogEntry } from '../api/types';

// MAX_BUFFER caps the in-memory entry buffer so a noisy stream doesn't
// blow up the renderer. Older entries are evicted in FIFO order.
const MAX_BUFFER = 1000;
const INITIAL_RECONNECT_DELAY_MS = 1000;
const MAX_RECONNECT_DELAY_MS = 30000;

export type ConnectionState =
  | 'idle'
  | 'connecting'
  | 'open'
  | 'reconnecting'
  | 'closed'
  | 'error';

export interface CloseInfo {
  code: number;
  reason: string;
}

interface UseLiveTailReturn {
  entries: LogEntry[];
  state: ConnectionState;
  closeInfo: CloseInfo | null;
  reconnectAttempt: number;
  connect: () => void;
  disconnect: () => void;
  clear: () => void;
  setFilter: (f: LiveTailFilter) => void;
}

// useLiveTail manages the WebSocket connection lifecycle and exposes a
// rolling buffer of received entries. Caller controls connect/disconnect
// explicitly so the page can show a "connect" button.
//
// initialFilter MUST carry at least one project slug — the v2 server
// closes the connection (1007) if the first frame's `projects` array
// is empty. Caller is responsible for supplying it.
export function useLiveTail(initialFilter: LiveTailFilter): UseLiveTailReturn {
  const [entries, setEntries] = useState<LogEntry[]>([]);
  const [state, setState] = useState<ConnectionState>('idle');
  const [closeInfo, setCloseInfo] = useState<CloseInfo | null>(null);
  const [reconnectAttempt, setReconnectAttempt] = useState(0);
  const wsRef = useRef<WebSocket | null>(null);
  const filterRef = useRef<LiveTailFilter>(initialFilter);
  const manualDisconnectRef = useRef(false);
  const reconnectTimerRef = useRef<number | null>(null);
  const reconnectAttemptRef = useRef(0);

  const clearReconnectTimer = useCallback(() => {
    if (reconnectTimerRef.current !== null) {
      window.clearTimeout(reconnectTimerRef.current);
      reconnectTimerRef.current = null;
    }
  }, []);

  const sendFilter = useCallback((f: LiveTailFilter) => {
    filterRef.current = f;
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(f));
    }
  }, []);

  const openSocket = useCallback(() => {
    if (wsRef.current) return;
    // The browser sends the session cookie automatically on
    // same-origin WS upgrades — no extra credentials option needed.
    const ws = new WebSocket(wsUrl('/api/v1/logs/stream'));
    wsRef.current = ws;

    ws.onopen = () => {
      reconnectAttemptRef.current = 0;
      setReconnectAttempt(0);
      setCloseInfo(null);
      setState('open');
      // Server expects an immediate filter message after connect. The
      // filter must carry at least one project slug.
      ws.send(JSON.stringify(filterRef.current));
    };
    ws.onmessage = (ev) => {
      try {
        const entry = JSON.parse(ev.data) as LogEntry;
        setEntries((prev) => {
          const next = [...prev, entry];
          if (next.length > MAX_BUFFER) {
            next.splice(0, next.length - MAX_BUFFER);
          }
          return next;
        });
      } catch {
        // Ignore malformed frames — server should never send these.
      }
    };
    ws.onerror = () => setState('error');
    ws.onclose = (ev) => {
      wsRef.current = null;
      if (manualDisconnectRef.current) {
        manualDisconnectRef.current = false;
        setState('closed');
        return;
      }

      setCloseInfo({ code: ev.code, reason: ev.reason });
      const attempt = reconnectAttemptRef.current;
      const delay = Math.min(
        INITIAL_RECONNECT_DELAY_MS * 2 ** attempt,
        MAX_RECONNECT_DELAY_MS,
      );
      reconnectAttemptRef.current = attempt + 1;
      setReconnectAttempt(attempt + 1);
      setState('reconnecting');
      clearReconnectTimer();
      reconnectTimerRef.current = window.setTimeout(() => {
        reconnectTimerRef.current = null;
        if (!manualDisconnectRef.current) {
          setState('connecting');
          openSocket();
        }
      }, delay);
    };
  }, [clearReconnectTimer]);

  const connect = useCallback(() => {
    clearReconnectTimer();
    manualDisconnectRef.current = false;
    reconnectAttemptRef.current = 0;
    setReconnectAttempt(0);
    setCloseInfo(null);
    if (wsRef.current) return;
    setState('connecting');
    openSocket();
  }, [clearReconnectTimer, openSocket]);

  const disconnect = useCallback(() => {
    clearReconnectTimer();
    manualDisconnectRef.current = true;
    reconnectAttemptRef.current = 0;
    setReconnectAttempt(0);
    setCloseInfo(null);
    wsRef.current?.close();
    wsRef.current = null;
    setState('closed');
  }, [clearReconnectTimer]);

  const clear = useCallback(() => setEntries([]), []);

  // Tear down on unmount so we don't leak sockets or timers across page changes.
  useEffect(() => {
    return () => {
      clearReconnectTimer();
      manualDisconnectRef.current = true;
      wsRef.current?.close();
      wsRef.current = null;
    };
  }, [clearReconnectTimer]);

  return {
    entries,
    state,
    closeInfo,
    reconnectAttempt,
    connect,
    disconnect,
    clear,
    setFilter: sendFilter,
  };
}
