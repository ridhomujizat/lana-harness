package agent

import (
	"log/slog"
	"sync"

	"github.com/ridhomujizat/lana-harness/core"
)

// Hub sends live frames per session.
type Hub struct {
	mu      sync.Mutex
	closed  bool
	subs    map[string][]*core.Subscription[core.LiveFrame]
	stream  map[string]*core.Observe[core.LiveFrame]
	parents map[string]string
}

// NewHub creates a Hub.
func NewHub() *Hub {
	return &Hub{
		subs:    map[string][]*core.Subscription[core.LiveFrame]{},
		stream:  map[string]*core.Observe[core.LiveFrame]{},
		parents: map[string]string{},
	}
}

// Link records childID as a child of parentID so that the parent's subscribers
// also receive the child's live frames. Linking the same pair twice has no
// effect.
func (h *Hub) Link(parentID, childID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.parents == nil {
		h.parents = map[string]string{}
	}
	h.parents[childID] = parentID
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

// Publish sends a frame without blocking. The frame reaches the session's
// subscribers, and a linked child's frame also reaches the subscribers of its
// parent and grandparent.
func (h *Hub) Publish(sessionID string, frame core.LiveFrame) {
	h.mu.Lock()
	// Walk the parent chain up to the grandparent, stopping at cycles.
	sessions := []string{sessionID}
	visited := map[string]bool{sessionID: true}
	for depth, current := 0, sessionID; depth < 2; depth++ {
		parentID, ok := h.parents[current]
		if !ok || parentID == "" || visited[parentID] {
			break
		}
		visited[parentID] = true
		sessions = append(sessions, parentID)
		current = parentID
	}
	type target struct {
		sessionID string
		obs       *core.Observe[core.LiveFrame]
		subs      []*core.Subscription[core.LiveFrame]
	}
	targets := make([]target, 0, len(sessions))
	for _, id := range sessions {
		obs := h.stream[id]
		if obs == nil {
			continue
		}
		targets = append(targets, target{
			sessionID: id,
			obs:       obs,
			subs:      append([]*core.Subscription[core.LiveFrame](nil), h.subs[id]...),
		})
	}
	h.mu.Unlock()
	for _, target := range targets {
		for _, sub := range target.subs {
			if sub.Len() >= core.ObserveBuffer {
				slog.Warn("live frame dropped", "session_id", target.sessionID)
			}
		}
	}
	for _, target := range targets {
		target.obs.Publish(frame)
	}
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
