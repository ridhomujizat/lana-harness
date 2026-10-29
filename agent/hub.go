package agent

import (
	"log/slog"
	"sync"

	"github.com/ridhomujizat/lana-harness/core"
)

// Hub sends live frames per session.
type Hub struct {
	mu     sync.Mutex
	closed bool
	subs   map[string][]*core.Subscription[core.LiveFrame]
	stream map[string]*core.Observe[core.LiveFrame]
}

// NewHub creates a Hub.
func NewHub() *Hub {
	return &Hub{subs: map[string][]*core.Subscription[core.LiveFrame]{}, stream: map[string]*core.Observe[core.LiveFrame]{}}
}

// Subscribe creates a subscription for the session.
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

// Publish sends a frame without blocking.
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
			slog.Warn("live frame dropped", "session_id", sessionID)
		}
	}
	obs.Publish(frame)
}

// Close closes all subscribers.
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
