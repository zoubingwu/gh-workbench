package server

import (
	"encoding/json"
	"sync"
	"sync/atomic"

	"github.com/zoubingwu/gh-workbench/internal/model"
)

type hub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
	seq     atomic.Uint64
}

func newHub() *hub {
	return &hub{clients: make(map[chan []byte]struct{})}
}

func (h *hub) subscribe() (<-chan []byte, func()) {
	client := make(chan []byte, 1)
	h.mu.Lock()
	h.clients[client] = struct{}{}
	h.mu.Unlock()

	return client, func() {
		h.mu.Lock()
		if _, ok := h.clients[client]; ok {
			delete(h.clients, client)
			close(client)
		}
		h.mu.Unlock()
	}
}

func (h *hub) publish(snapshot model.Snapshot) error {
	payload, err := json.Marshal(struct {
		Type     string         `json:"type"`
		Sequence uint64         `json:"sequence"`
		Snapshot model.Snapshot `json:"snapshot"`
	}{
		Type:     "snapshot.updated",
		Sequence: h.seq.Add(1),
		Snapshot: snapshot,
	})
	if err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	for client := range h.clients {
		select {
		case <-client:
		default:
		}
		client <- payload
	}
	return nil
}
