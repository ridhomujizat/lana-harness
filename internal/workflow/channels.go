package workflow

// This file holds the Message node's channel registry (REQ-WF-005): the
// channels a message can be delivered through, their availability, and the
// dispatch the engine calls. The service registers "telegram" itself, backed by
// the owner notifier; the runtime registers "web".

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// Message channel ids with a fixed place in the listing order.
const (
	channelWeb      = "web"
	channelTelegram = "telegram"

	// defaultMessageChannel is the channel an old message node without a
	// channels param delivers to.
	defaultMessageChannel = channelTelegram
)

// MessageRequest is one Message node delivery through one channel.
type MessageRequest struct {
	Channel      string
	Agent        string
	Text         string
	WorkflowID   string
	WorkflowName string
	RunNumber    int
}

// MessageChannel describes one delivery channel of the Message node.
type MessageChannel struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	NeedsAgent bool   `json:"needs_agent"`
	Available  bool   `json:"available"`
	Reason     string `json:"reason,omitempty"`
}

// messageChannel is one registered channel: its descriptor and its sender.
type messageChannel struct {
	id         string
	label      string
	needsAgent bool
	send       func(ctx context.Context, req MessageRequest) error
	available  func() (bool, string)
}

// RegisterChannel adds or replaces a message channel. send delivers one
// MessageRequest; available, when non-nil, reports whether the channel can
// deliver now and, when it cannot, why. It is safe for concurrent use and
// re-registering an id replaces the earlier channel.
func (s *Service) RegisterChannel(id, label string, needsAgent bool, send func(ctx context.Context, req MessageRequest) error, available func() (bool, string)) {
	s.chanMu.Lock()
	defer s.chanMu.Unlock()
	s.channels[id] = messageChannel{id: id, label: label, needsAgent: needsAgent, send: send, available: available}
}

// MessageChannels lists the registered channels in a stable order: "web"
// first, "telegram" second, then the rest by id.
func (s *Service) MessageChannels() []MessageChannel {
	s.chanMu.RLock()
	entries := make([]messageChannel, 0, len(s.channels))
	for _, entry := range s.channels {
		entries = append(entries, entry)
	}
	s.chanMu.RUnlock()

	out := make([]MessageChannel, 0, len(entries))
	for _, entry := range entries {
		channel := MessageChannel{ID: entry.id, Label: entry.label, NeedsAgent: entry.needsAgent, Available: true}
		if entry.available != nil {
			if ok, reason := entry.available(); !ok {
				channel.Available = false
				channel.Reason = reason
			}
		}
		out = append(out, channel)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := messageChannelOrder(out[i].ID), messageChannelOrder(out[j].ID); a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// messageChannelOrder ranks a channel id for the listing: web, telegram, rest.
func messageChannelOrder(id string) int {
	switch id {
	case channelWeb:
		return 0
	case channelTelegram:
		return 1
	default:
		return 2
	}
}

// dispatchMessage delivers one Message node message through the named channel
// (REQ-WF-005). It is the engine's SendMessage callback: an unknown id or an
// unavailable channel is an error naming the channel.
func (s *Service) dispatchMessage(ctx context.Context, req MessageRequest) error {
	entry, ok := s.messageChannel(req.Channel)
	if !ok {
		return fmt.Errorf("unknown channel %q", req.Channel)
	}
	if entry.available != nil {
		if ok, reason := entry.available(); !ok {
			if reason == "" {
				reason = "the channel is not available"
			}
			return errors.New(reason)
		}
	}
	if entry.send == nil {
		return fmt.Errorf("channel %q cannot send", req.Channel)
	}
	return entry.send(ctx, req)
}

// messageChannel returns one registered channel.
func (s *Service) messageChannel(id string) (messageChannel, bool) {
	s.chanMu.RLock()
	defer s.chanMu.RUnlock()
	entry, ok := s.channels[id]
	return entry, ok
}

// registerTelegramChannel registers the built-in Telegram channel, backed by
// the owner notifier (REQ-WF-005).
func (s *Service) registerTelegramChannel() {
	s.RegisterChannel(channelTelegram, "Telegram", false,
		func(ctx context.Context, req MessageRequest) error { return s.sendOwner(ctx, req.Text) },
		s.telegramChannelStatus)
}

// telegramChannelStatus reports whether the Telegram notifier is connected.
func (s *Service) telegramChannelStatus() (bool, string) {
	if s.notifierRef() == nil {
		return false, "Telegram is not connected"
	}
	return true, ""
}
