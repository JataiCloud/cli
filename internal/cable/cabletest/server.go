// Package cabletest é um servidor ActionCable falso para os testes.
package cabletest

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/coder/websocket"
)

// Handler aceita a conexão, manda o welcome e, a cada subscribe, confirma e
// empurra as mensagens configuradas para aquele canal.
type Handler struct {
	Messages                map[string][]map[string]any
	Reject                  map[string]bool
	DropAfterFirstSubscribe bool

	mu         sync.Mutex
	headers    http.Header
	subscribed []string
	dropped    bool
}

func (h *Handler) Headers() http.Header {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.headers
}

func (h *Handler) Subscribed() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string{}, h.subscribed...)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.headers = r.Header.Clone()
	h.mu.Unlock()
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: []string{"actioncable-v1-json"}, InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx := r.Context()
	send(ctx, conn, map[string]any{"type": "welcome"})
	// O Rails manda um ping a cada 3 s; um basta para exercitar o descarte.
	send(ctx, conn, map[string]any{"type": "ping", "message": 1727712000})
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var cmd struct{ Command, Identifier string }
		if json.Unmarshal(data, &cmd) != nil || cmd.Command != "subscribe" {
			continue
		}
		var id struct{ Channel string }
		json.Unmarshal([]byte(cmd.Identifier), &id)
		if h.handleSubscribe(ctx, conn, cmd.Identifier, id.Channel) {
			return
		}
	}
}

// handleSubscribe devolve true quando deve derrubar a conexão.
func (h *Handler) handleSubscribe(ctx context.Context, conn *websocket.Conn, identifier, channel string) bool {
	h.mu.Lock()
	h.subscribed = append(h.subscribed, identifier)
	drop := h.DropAfterFirstSubscribe && !h.dropped
	h.dropped = h.dropped || drop
	h.mu.Unlock()
	if drop {
		return true
	}
	if h.Reject[channel] {
		send(ctx, conn, map[string]any{"type": "reject_subscription", "identifier": identifier})
		return false
	}
	send(ctx, conn, map[string]any{"type": "confirm_subscription", "identifier": identifier})
	for _, m := range h.Messages[channel] {
		send(ctx, conn, map[string]any{"identifier": identifier, "message": m})
	}
	return false
}

func send(ctx context.Context, conn *websocket.Conn, v any) {
	data, _ := json.Marshal(v)
	conn.Write(ctx, websocket.MessageText, data)
}
