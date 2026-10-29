package agent

import (
	"log/slog"
	"sync"

	"github.com/ridhomujizat/lana-harness/core"
)

// Hub mengirim frame hidup per session.
type Hub struct {
	mu     sync.Mutex
	closed bool
	subs   map[string][]*core.Subscription[core.LiveFrame]
	stream map[string]*core.Observe[core.LiveFrame]
}

// NewHub membuat Hub.
func NewHub() *Hub {
	return &Hub{subs: map[string][]*core.Subscription[core.LiveFrame]{}, stream: map[string]*core.Observe[core.LiveFrame]{}}
}

// Subscribe membuat subscription untuk session.
func (h *Hub) Subscribe(sessionID string) *core.Subscription[core.LiveFrame] {
	h.mu.Lock()
	defer h.mu.Unlock()
	obs := h.stream[sessionID]
	if obs == nil {
		obs = core.NewObserve[core.LiveFrame]()
		h.stream[sessionID] = obs
	}
	sub := obs.Subscribe()
	if h.closed {
		obs.Close()
	}
	h.subs[sessionID] = append(h.subs[sessionID], sub)
	return sub
}

// Publish mengirim frame tanpa memblokir.
func (h *Hub) Publish(sessionID string, frame core.LiveFrame) {
	h.mu.Lock()
	obs := h.stream[sessionID]
	subs := append([]*core.Subscription[core.LiveFrame](nil), h.subs[sessionID]...)
	h.mu.Unlock()
	if obs == nil {
		return
	}
	for _, sub := range subs {
		if sub.Len() >= core.ObserveBuffer {
			slog.Warn("frame hidup dibuang", "session_id", sessionID)
		}
	}
	obs.Publish(frame)
}

// Close menutup semua subscriber.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for _, obs := range h.stream {
		obs.Close()
	}
}
