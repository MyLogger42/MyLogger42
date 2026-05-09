package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/silverbaq/mylogger42/internal/domain"
)

const (
	// pingInterval is the cadence at which the server sends WS pings to
	// each client. Spec: every 30s.
	pingInterval = 30 * time.Second
	// pongWait is how long we wait for a pong before treating the
	// connection as dead. Must exceed pingInterval.
	pongWait = 60 * time.Second
	// writeWait bounds individual write operations.
	writeWait = 10 * time.Second
)

// upgrader is permissive on origin because UI auth is out of scope for
// v1; only the ingest endpoint requires a key.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(*http.Request) bool { return true },
}

// Client represents a single live-tail subscriber. Each client owns one
// reader and one writer goroutine. The reader updates the filter, the
// writer drains send and applies pings.
type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	send   chan domain.LogEntry
	logger *log.Logger

	mu     sync.RWMutex
	filter domain.LiveTailFilter

	closeOnce sync.Once
}

// Filter returns a snapshot of the client's current subscription filter.
func (c *Client) Filter() domain.LiveTailFilter {
	c.mu.RLock()
	defer c.mu.RUnlock()
	// Return value-copy so the hub can't mutate live state.
	out := domain.LiveTailFilter{
		Services: append([]string(nil), c.filter.Services...),
		Levels:   append([]domain.LogLevel(nil), c.filter.Levels...),
	}
	return out
}

// SetFilter atomically replaces the client's subscription filter.
func (c *Client) SetFilter(f domain.LiveTailFilter) {
	c.mu.Lock()
	c.filter = f
	c.mu.Unlock()
}

// close releases the underlying socket. Safe to call multiple times.
func (c *Client) close() {
	c.closeOnce.Do(func() {
		_ = c.conn.Close()
		close(c.send)
	})
}

// ServeWS upgrades an HTTP request to a WebSocket and registers a new
// client with the hub. It is meant to be wired as the handler for the
// /api/v1/logs/stream route.
func ServeWS(hub *Hub, logger *log.Logger, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote an HTTP response.
		return
	}
	c := &Client{
		hub:    hub,
		conn:   conn,
		send:   make(chan domain.LogEntry, 256),
		logger: logger,
	}
	hub.Register(c)
	go c.writeLoop()
	go c.readLoop()
}

// readLoop receives filter updates from the client and resets the read
// deadline on each pong, terminating the connection on any error.
func (c *Client) readLoop() {
	defer c.hub.Unregister(c)

	c.conn.SetReadLimit(64 * 1024)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var f domain.LiveTailFilter
		if err := json.Unmarshal(msg, &f); err != nil {
			c.logger.Printf("ws: invalid filter from client: %v", err)
			continue
		}
		// Discard unknown levels rather than rejecting the whole filter
		// update — they'd just match nothing anyway.
		clean := f.Levels[:0]
		for _, l := range f.Levels {
			if l.Valid() {
				clean = append(clean, l)
			}
		}
		f.Levels = clean
		c.SetFilter(f)
	}
}

// writeLoop drains the send channel and emits periodic pings.
func (c *Client) writeLoop() {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case entry, ok := <-c.send:
			if !ok {
				// Hub closed us.
				_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
				_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteJSON(entry); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
