package lana

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/id"
)

// postBusyTimeout bounds how long PostAgentMessage waits for a running turn
// before it gives up.
const postBusyTimeout = 2 * time.Minute

// postBusyPoll is the interval of the turn-active check while waiting.
var postBusyPoll = 25 * time.Millisecond

// errAgentChatBusy is returned by PostAgentMessage when a turn on the chat did
// not finish within postBusyTimeout.
var errAgentChatBusy = errors.New("the agent chat is busy")

// PostAgentMessage posts a message into an agent's web chat as the agent,
// outside of any model turn (for example a workflow's Message node delivering a
// report). It finds the agent's current web chat session or creates one, waits
// for a running turn to finish, and appends one synthetic turn: turn/start, an
// assistant/message with Source and SourceName, and turn/end. The message is not
// part of the conversation context; the owner can reply to it, and only a reply
// puts it in context. Because the chat is a root web session whose turn ends
// with assistant text, the desktop reply notification fires like any other web
// reply.
func (rt *Runtime) PostAgentMessage(ctx context.Context, agent, text, source, sourceName string) (string, error) {
	if err := rt.closedError(); err != nil {
		return "", err
	}
	if agent == "" {
		return "", fmt.Errorf("%w: %s", ErrUnknownAgent, agent)
	}
	if _, err := rt.routeAgent(agent); err != nil {
		return "", err
	}
	if strings.TrimSpace(text) == "" {
		return "", ErrEmptyText
	}
	sessionID, err := rt.currentWebSession(ctx, agent)
	if err != nil {
		return "", err
	}
	if err := rt.waitAgentIdle(ctx, sessionID); err != nil {
		return sessionID, err
	}
	turnID := id.New()
	drafts := []core.EventDraft{
		{Kind: core.KindTurnStart, TurnID: turnID, Payload: core.TurnStart{V: core.PayloadVersion, TurnID: turnID}},
		{Kind: core.KindAssistantMessage, TurnID: turnID, Payload: core.AssistantMessage{
			V:          core.PayloadVersion,
			Content:    []core.ContentBlock{{Kind: core.BlockText, Text: text}},
			StopReason: "end_turn",
			Source:     source,
			SourceName: sourceName,
		}},
		{Kind: core.KindTurnEnd, TurnID: turnID, Payload: core.TurnEnd{V: core.PayloadVersion, TurnID: turnID, Reason: "end_turn"}},
	}
	if _, err := rt.store.Append(ctx, sessionID, drafts); err != nil {
		return sessionID, err
	}
	return sessionID, nil
}

// currentWebSession returns the agent's current web chat session: the newest
// top-level session on channel "web" with address "owner", or a new one created
// the same way the web backend creates a room session. The find-or-create is
// serialized per (channel, address, agent), like ChannelSession.
func (rt *Runtime) currentWebSession(ctx context.Context, agentName string) (string, error) {
	unlock := rt.lockChannelSession("web", "owner", agentName)
	defer unlock()
	sessions, err := rt.store.List(ctx, core.SessionFilter{
		Agent:        agentName,
		Channel:      "web",
		Address:      "owner",
		TopLevelOnly: true,
		Limit:        1,
	})
	if err != nil {
		return "", err
	}
	if len(sessions) > 0 {
		return sessions[0].ID, nil
	}
	info, err := rt.CreateChannelSession(ctx, "web", "owner", agentName)
	if err != nil {
		return "", err
	}
	return info.ID, nil
}

// waitAgentIdle waits until no turn runs on the session in this process, up to
// postBusyTimeout, honoring ctx. It returns errAgentChatBusy on the timeout.
func (rt *Runtime) waitAgentIdle(ctx context.Context, sessionID string) error {
	deadline := time.Now().Add(postBusyTimeout)
	for rt.registry != nil && rt.registry.TurnActive(sessionID) {
		if time.Now().After(deadline) {
			return errAgentChatBusy
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(postBusyPoll):
		}
	}
	return nil
}
