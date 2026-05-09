import { useCallback, useEffect, useRef, useState } from 'react';
import { wsUrl } from '../api/client';
import type { LiveTailFilter, LogEntry } from '../api/types';

// MAX_BUFFER caps the in-memory entry buffer so a noisy stream doesn't
// blow up the renderer. Older entries are evicted in FIFO order.
const MAX_BUFFER = 1000;

type ConnectionState = 'idle' | 'connecting' | 'open' | 'closed' | 'error';

interface UseLiveTailReturn {
  entries: LogEntry[];
  state: ConnectionState;
  connect: () => void;
  disconnect: () => void;
  clear: () => void;
  setFilter: (f: LiveTailFilter) => void;
}

// useLiveTail manages the WebSocket connection lifecycle and exposes a
// rolling buffer of received entries. Caller controls connect/disconnect
// explicitly so the page can show a "connect" button.
export function useLiveTail(initialFilter: LiveTailFilter = {}): UseLiveTailReturn {
  const [entries, setEntries] = useState<LogEntry[]>([]);
  const [state, setState] = useState<ConnectionState>('idle');
  const wsRef = useRef<WebSocket | null>(null);
  const filterRef = useRef<LiveTailFilter>(initialFilter);

  const sendFilter = useCallback((f: LiveTailFilter) => {
    filterRef.current = f;
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(f));
    }
  }, []);

  const connect = useCallback(() => {
    if (wsRef.current) return;
    setState('connecting');
    const ws = new WebSocket(wsUrl('/api/v1/logs/stream'));
    wsRef.current = ws;

    ws.onopen = () => {
      setState('open');
      // Server expects an immediate filter message after connect.
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
    ws.onclose = () => {
      wsRef.current = null;
      setState('closed');
    };
  }, []);

  const disconnect = useCallback(() => {
    wsRef.current?.close();
    wsRef.current = null;
    setState('closed');
  }, []);

  const clear = useCallback(() => setEntries([]), []);

  // Tear down on unmount so we don't leak sockets across page changes.
  useEffect(() => {
    return () => {
      wsRef.current?.close();
      wsRef.current = null;
    };
  }, []);

  return { entries, state, connect, disconnect, clear, setFilter: sendFilter };
}
