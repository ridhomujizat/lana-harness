package lana

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

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
// creating one when none exists. The lookup and the create are serialized per
// (channel, address, agent) key, so concurrent messages cannot create two
// sessions, and the agent filter lets an older session be found even when the
// address has many newer sessions of other agents.
func (rt *Runtime) ChannelSession(ctx context.Context, channel, address, agentName string) (core.SessionInfo, error) {
	if err := rt.closedError(); err != nil {
		return core.SessionInfo{}, err
	}
	unlock := rt.lockChannelSession(channel, address, agentName)
	defer unlock()
	list, err := rt.store.List(ctx, core.SessionFilter{Channel: channel, Address: address, Agent: agentName, Limit: 1})
	if err != nil {
		return core.SessionInfo{}, err
	}
	if len(list) > 0 {
		return list[0], nil
	}
	return rt.CreateChannelSession(ctx, channel, address, agentName)
}

// lockChannelSession serializes get-or-create for one (channel, address, agent)
// key. The returned function releases the lock. The map entries are tiny and
// stay for the lifetime of the runtime.
func (rt *Runtime) lockChannelSession(channel, address, agentName string) func() {
	key := channel + "\x00" + address + "\x00" + agentName
	rt.sessionMu.Lock()
	if rt.sessionLocks == nil {
		rt.sessionLocks = make(map[string]*sync.Mutex)
	}
	mu, ok := rt.sessionLocks[key]
	if !ok {
		mu = &sync.Mutex{}
		rt.sessionLocks[key] = mu
	}
	rt.sessionMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// CreateChannelSession creates a new session for the channel address. The workspace is the
// agent's workspace, else $LANA_HOME/workspaces/<agent>/.
func (rt *Runtime) CreateChannelSession(ctx context.Context, channel, address, agentName string) (core.SessionInfo, error) {
	return rt.createChannelSession(ctx, channel, address, agentName, "")
}

// createChannelSession is CreateChannelSession with a fixed title. A
// non-empty title is stored with source "system", so the title pass leaves
// it alone (spec 40, REQ-TTL-007).
func (rt *Runtime) createChannelSession(ctx context.Context, channel, address, agentName, title string) (core.SessionInfo, error) {
	if err := rt.closedError(); err != nil {
		return core.SessionInfo{}, err
	}
	// A new session is the first step of a turn; refuse it while draining
	// (REQ-TFB-002, API-TFB-008).
	if err := rt.drainError(); err != nil {
		return core.SessionInfo{}, err
	}
	if _, err := rt.routeAgent(agentName); err != nil {
		return core.SessionInfo{}, err
	}
	workspace := rt.agents()[agentName].Workspace
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
	in := core.NewSession{Agent: agentName, Channel: channel, Address: address, Workspace: workspace}
	if title != "" {
		in.Title, in.TitleSource = title, core.TitleSourceSystem
	}
	return rt.store.Create(ctx, in)
}

// LeaseSession takes the session lease so no other process can use the session.
// The returned function releases it.
//
// Taking the lease also repairs an orphaned turn: a session still "running" or
// "waiting_input" whose turn/end was lost (the previous holder died mid-turn)
// gets an "interrupted" turn/end and goes back to idle, so its next message
// runs instead of queueing forever. The lease rules out a live turn in another
// process that leases; a turn running in this process (workflow, CLI and
// channel turns hold no lease of their own) is never repaired.
func (rt *Runtime) LeaseSession(ctx context.Context, sessionID string) (func() error, error) {
	if err := rt.closedError(); err != nil {
		return nil, err
	}
	release, err := rt.store.TryLease(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if err := rt.repairOrphanedTurn(ctx, sessionID); err != nil {
		_ = release()
		return nil, fmt.Errorf("repair session %s: %w", sessionID, err)
	}
	return release, nil
}

// repairOrphanedTurn closes the session's open turn when no turn for it runs
// in this process. The caller holds the session lease.
func (rt *Runtime) repairOrphanedTurn(ctx context.Context, sessionID string) error {
	info, err := rt.store.Info(ctx, sessionID)
	if err != nil {
		return err
	}
	if info.Status != "running" && info.Status != "waiting_input" {
		return nil
	}
	if rt.registry == nil {
		return nil
	}
	repaired, err := rt.registry.WithoutTurn(sessionID, func() error {
		_, err := rt.store.Open(ctx, sessionID)
		return err
	})
	if err != nil {
		return err
	}
	if repaired {
		slog.Warn("session: closed a turn left open by a dead process", "session", sessionID, "status", info.Status)
	}
	return nil
}

// LeaseHolder reports the PID of the process holding the session lease without
// taking it. held is false when no process holds the lease. A store without a
// lease probe is treated as having no cross-process lease.
func (rt *Runtime) LeaseHolder(ctx context.Context, sessionID string) (pid int, held bool, err error) {
	if err := rt.closedError(); err != nil {
		return 0, false, err
	}
	probe, ok := rt.store.(core.LeaseProbe)
	if !ok {
		return 0, false, nil
	}
	return probe.LeaseHolder(ctx, sessionID)
}
