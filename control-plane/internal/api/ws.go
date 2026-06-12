package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"nhooyr.io/websocket"
)

// Hub fans PushEvents out to all connected WebSocket clients. V1 keeps it
// simple: every connected client receives every event; per-user filtering can
// come later.
type Hub struct {
	log *slog.Logger

	mu      sync.Mutex
	clients map[*client]struct{}
}

type client struct {
	conn *websocket.Conn
}

func NewHub(log *slog.Logger) *Hub {
	return &Hub{log: log, clients: make(map[*client]struct{})}
}

func (h *Hub) add(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
}

// Broadcast marshals ev and writes it to every connected client. Failed writes
// are logged; the client is reaped by its own read loop on disconnect.
func (h *Hub) Broadcast(ctx context.Context, ev PushEvent) {
	data, err := json.Marshal(ev)
	if err != nil {
		h.log.Error("marshal push event", "err", err)
		return
	}
	h.mu.Lock()
	conns := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		conns = append(conns, c)
	}
	h.mu.Unlock()

	for _, c := range conns {
		if err := c.conn.Write(ctx, websocket.MessageText, data); err != nil {
			h.log.Warn("ws write failed", "err", err)
		}
	}
}

// ServeHTTP upgrades the connection and blocks until the client disconnects.
// The skeleton has no inbound client messages, so the read loop just drains
// until close.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		h.log.Warn("ws accept failed", "err", err)
		return
	}
	c := &client{conn: conn}
	h.add(c)
	defer func() {
		h.remove(c)
		conn.Close(websocket.StatusNormalClosure, "")
	}()

	ctx := r.Context()
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			return
		}
	}
}
