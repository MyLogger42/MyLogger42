// Package ws implements the WebSocket live-tail subsystem.
//
// The Hub fans a single LogEntry stream out to all connected clients,
// honouring each client's per-connection LiveTailFilter. It is mailbox-
// driven (channels) so the publisher path is non-blocking — a slow
// client never blocks ingest.
package ws

import (
	"context"
	"log"

	"github.com/silverbaq/mylogger42/internal/domain"
)

// Hub maintains the set of active clients and broadcasts LogEntries to
// them. All state mutations happen on the single Run() goroutine via
// channels, so no mutex is needed.
type Hub struct {
	register   chan *Client
	unregister chan *Client
	publish    chan domain.LogEntry
	clients    map[*Client]struct{}
	logger     *log.Logger
}

// NewHub constructs a Hub. Call Run in a goroutine before publishing.
func NewHub(logger *log.Logger) *Hub {
	if logger == nil {
		logger = log.Default()
	}
	return &Hub{
		register:   make(chan *Client, 16),
		unregister: make(chan *Client, 16),
		// Publish buffer is generous so a brief stall in Run doesn't back
		// up ingest. Drops are explicit (see Publish).
		publish: make(chan domain.LogEntry, 1024),
		clients: make(map[*Client]struct{}),
		logger:  logger,
	}
}

// Run owns the hub's state. Cancel ctx to stop and close all clients.
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			for c := range h.clients {
				c.close()
			}
			return
		case c := <-h.register:
			h.clients[c] = struct{}{}
		case c := <-h.unregister:
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				c.close()
			}
		case entry := <-h.publish:
			for c := range h.clients {
				if !c.Filter().Matches(entry) {
					continue
				}
				// Non-blocking send: if a client's outbound queue is full
				// we drop rather than stall the whole hub. Live-tail is
				// best-effort by design.
				select {
				case c.send <- entry:
				default:
					h.logger.Printf("ws: dropping entry for slow client %p", c)
				}
			}
		}
	}
}

// Register adds a client to the hub.
func (h *Hub) Register(c *Client) { h.register <- c }

// Unregister removes a client from the hub.
func (h *Hub) Unregister(c *Client) { h.unregister <- c }

// Publish broadcasts an entry to all matching subscribers. It is safe to
// call from any goroutine. If the publish buffer is saturated the entry
// is dropped and a warning logged — ingest must never block.
func (h *Hub) Publish(entry domain.LogEntry) {
	select {
	case h.publish <- entry:
	default:
		h.logger.Printf("ws: publish buffer full, dropping entry %s", entry.ID)
	}
}
