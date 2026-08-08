package ws

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mylogger42/mylogger42/internal/domain"
	"github.com/mylogger42/mylogger42/internal/store"
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
	// firstFrameWait bounds how long we wait for the client's first
	// filter message after the WS upgrade succeeds. Without it, a stuck
	// client could hold the connection open indefinitely with no filter.
	firstFrameWait = 10 * time.Second
)

// upgrader is permissive on origin because the SPA may run on a
// different port during development (Vite dev server). Pre-upgrade
// session auth (handled by the API layer before ServeWS is called) is
// the actual access control here.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(*http.Request) bool { return true },
}

// clientFilterFrame is the wire shape of the client's filter message.
// Projects is a slice of slugs — the server resolves them to project
// IDs before storing on the Client.
type clientFilterFrame struct {
	Projects []string         `json:"projects,omitempty"`
	Services []string         `json:"services,omitempty"`
	Levels   []domain.LogLevel `json:"levels,omitempty"`
}

// Client represents a single live-tail subscriber. Each client owns one
// reader and one writer goroutine. The reader updates the filter, the
// writer drains send and applies pings.
type Client struct {
	hub      *Hub
	conn     *websocket.Conn
	send     chan domain.LogEntry
	logger   *log.Logger
	projects store.ProjectStore // for slug→ID resolution

	mu     sync.RWMutex
	filter domain.LiveTailFilter

	closeOnce sync.Once
}

// Filter returns a snapshot of the client's current subscription filter.
// The Projects slice carries project IDs (already resolved from slugs).
func (c *Client) Filter() domain.LiveTailFilter {
	c.mu.RLock()
	defer c.mu.RUnlock()
	// Return value-copy so the hub can't mutate live state.
	return domain.LiveTailFilter{
		Projects: append([]string(nil), c.filter.Projects...),
		Services: append([]string(nil), c.filter.Services...),
		Levels:   append([]domain.LogLevel(nil), c.filter.Levels...),
	}
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
// /api/v1/logs/stream route AFTER the session-auth middleware has
// validated the request — the upgrade itself doesn't repeat that check.
//
// First-frame protocol: the client must send a clientFilterFrame within
// firstFrameWait. The frame's `projects` slugs are resolved to project
// IDs against the supplied ProjectStore. An empty `projects` slice or
// any unresolved slug closes the connection with code 1007 (invalid
// frame data) — opting into a global firehose is an explicit
// out-of-band admin action, not a default.
//
// Subsequent filter messages follow the same shape; on validation
// errors the previous filter is retained and the client is logged.
func ServeWS(hub *Hub, projects store.ProjectStore, logger *log.Logger, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote an HTTP response.
		return
	}
	c := &Client{
		hub:      hub,
		conn:     conn,
		send:     make(chan domain.LogEntry, 256),
		logger:   logger,
		projects: projects,
	}

	// Read the first filter frame synchronously before registering the
	// client with the hub. If the client doesn't send one, or sends an
	// invalid one, we close cleanly without ever joining the broadcast
	// set — no chance of a "client gets entries before they specified a
	// filter" race.
	if err := c.readFirstFilter(r.Context()); err != nil {
		c.logger.Printf("ws: rejecting connection: %v", err)
		_ = conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInvalidFramePayloadData, err.Error()),
		)
		_ = conn.Close()
		return
	}

	hub.Register(c)
	go c.writeLoop()
	go c.readLoop()
}

// readFirstFilter reads and applies exactly one filter message,
// bounded by firstFrameWait. Returns the validation error suitable to
// surface to the client via a CloseInvalidFramePayloadData close frame.
func (c *Client) readFirstFilter(ctx context.Context) error {
	_ = c.conn.SetReadDeadline(time.Now().Add(firstFrameWait))
	c.conn.SetReadLimit(64 * 1024)
	_, msg, err := c.conn.ReadMessage()
	if err != nil {
		return errors.New("read first frame: " + err.Error())
	}
	resolved, err := c.parseAndResolve(ctx, msg)
	if err != nil {
		return err
	}
	c.SetFilter(resolved)
	return nil
}

// parseAndResolve parses the wire-format clientFilterFrame, resolves
// `projects` slugs against the project store, and returns the
// domain.LiveTailFilter the hub matches against.
//
// Validation rules (per spec):
//
//   - JSON must parse.
//   - `projects` must be non-empty.
//   - Every slug must resolve to an existing project (404 in the store
//     → invalid first frame).
//   - Unknown levels are dropped silently (they would never match
//     anything, so rejecting the whole frame is gratuitous).
func (c *Client) parseAndResolve(ctx context.Context, msg []byte) (domain.LiveTailFilter, error) {
	var frame clientFilterFrame
	if err := json.Unmarshal(msg, &frame); err != nil {
		return domain.LiveTailFilter{}, errors.New("invalid filter json: " + err.Error())
	}
	if len(frame.Projects) == 0 {
		return domain.LiveTailFilter{}, errors.New("filter must specify at least one project slug")
	}

	ids := make([]string, 0, len(frame.Projects))
	for _, slug := range frame.Projects {
		if err := domain.ValidateSlug(slug); err != nil {
			return domain.LiveTailFilter{}, errors.New("invalid slug " + slug + ": " + err.Error())
		}
		p, err := c.projects.GetBySlug(ctx, slug)
		if err != nil {
			return domain.LiveTailFilter{}, errors.New("unknown project: " + slug)
		}
		ids = append(ids, p.ID)
	}

	// Drop unknown levels rather than rejecting the whole filter — they
	// can't match anything in domain.LiveTailFilter.Matches anyway.
	cleanLevels := make([]domain.LogLevel, 0, len(frame.Levels))
	for _, l := range frame.Levels {
		if l.Valid() {
			cleanLevels = append(cleanLevels, l)
		}
	}

	return domain.LiveTailFilter{
		Projects: ids,
		Services: append([]string(nil), frame.Services...),
		Levels:   cleanLevels,
	}, nil
}

// readLoop receives filter updates from the client and resets the read
// deadline on each pong. Validation failures on subsequent frames are
// logged and dropped — the connection stays alive with the previous
// filter, per spec.
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
		// Best-effort context for the slug→ID lookup. If the request's
		// original ctx was cancelled (server shutting down) the hub
		// will shortly close us anyway, so a shallow context is fine.
		resolved, err := c.parseAndResolve(context.Background(), msg)
		if err != nil {
			c.logger.Printf("ws: rejecting filter update from client %p: %v", c, err)
			continue
		}
		c.SetFilter(resolved)
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
