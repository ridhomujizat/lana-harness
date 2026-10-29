package lana

import (
	"context"
	"os"
	"path/filepath"

	"github.com/ridhomujizat/lana-harness/core"
)

// channelSessionScan is how many recent sessions of an address are searched for an agent.
const channelSessionScan = 50

// ActiveAgent returns the agent of the most recently updated session for the channel address, or "".
func (rt *Runtime) ActiveAgent(ctx context.Context, channel, address string) (string, error) {
	if err := rt.closedError(); err != nil {
		return "", err
	}
	list, err := rt.store.List(ctx, core.SessionFilter{Channel: channel, Address: address, Limit: 1})
	if err != nil || len(list) == 0 {
		return "", err
	}
	return list[0].Agent, nil
}

// ChannelSession returns the most recent session of the agent for the channel address,
// creating one when none exists.
func (rt *Runtime) ChannelSession(ctx context.Context, channel, address, agentName string) (core.SessionInfo, error) {
	if err := rt.closedError(); err != nil {
		return core.SessionInfo{}, err
	}
	list, err := rt.store.List(ctx, core.SessionFilter{Channel: channel, Address: address, Limit: channelSessionScan})
	if err != nil {
		return core.SessionInfo{}, err
	}
	for _, info := range list {
		if info.Agent == agentName {
			return info, nil
		}
	}
	return rt.CreateChannelSession(ctx, channel, address, agentName)
}

// CreateChannelSession creates a new session for the channel address. The workspace is the
// agent's workspace, else $LANA_HOME/workspaces/<agent>/.
func (rt *Runtime) CreateChannelSession(ctx context.Context, channel, address, agentName string) (core.SessionInfo, error) {
	if err := rt.closedError(); err != nil {
		return core.SessionInfo{}, err
	}
	if _, err := rt.routeAgent(agentName); err != nil {
		return core.SessionInfo{}, err
	}
	workspace := rt.result.Config.Agents[agentName].Workspace
	if workspace == "" && rt.home != "" {
		workspace = filepath.Join(rt.home, "workspaces", agentName)
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			return core.SessionInfo{}, err
		}
	}
	if workspace == "" {
		workspace = "."
	}
	workspace, err := normalizeWorkspace(workspace)
	if err != nil {
		return core.SessionInfo{}, err
	}
	return rt.store.Create(ctx, core.NewSession{Agent: agentName, Channel: channel, Address: address, Workspace: workspace})
}

// LeaseSession takes the session lease so no other process can use the session.
// The returned function releases it.
func (rt *Runtime) LeaseSession(ctx context.Context, sessionID string) (func() error, error) {
	if err := rt.closedError(); err != nil {
		return nil, err
	}
	return rt.store.TryLease(ctx, sessionID)
}
