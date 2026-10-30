package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/channels/cli/tui"
	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

// cmdChat runs the interactive TUI chat.
func cmdChat(args []string) int {
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	agentFlag := fs.String("agent", "", "agent")
	yolo := fs.Bool("yolo", false, "trust policy")
	cont := fs.Bool("continue", false, "continue the last session for this directory")
	resume := fs.String("resume", "", "session id or prefix")
	verbose := fs.Bool("verbose", false, "verbose mode")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !isTTY(os.Stdin) || !isTTY(os.Stdout) {
		fmt.Fprintln(os.Stderr, "error: lana chat needs a terminal; use 'lana run' for non-interactive use")
		return 2
	}
	home, err := homeDir()
	if err != nil {
		return fail(err, 1)
	}
	ctx := context.Background()
	defer setupChatLogging(home)()
	agentName, err := loadAgent(ctx, home, *agentFlag)
	if err != nil {
		var errs config.Errors
		if errors.As(err, &errs) {
			for _, item := range errs {
				fmt.Fprintf(os.Stderr, "error: %s\n", item.Error())
			}
			return 2
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}

	approver := tui.NewApprover()
	asker := tui.NewAsker()
	rt, _, _, missingEnv, err := runtimeFor(ctx, home, agentName, approver.Ask, lana.AskWith(asker.Ask), lana.AskChannels("cli"))
	if err != nil {
		var errs config.Errors
		if errors.As(err, &errs) {
			for _, item := range errs {
				fmt.Fprintf(os.Stderr, "error: %s\n", item.Error())
			}
			return 2
		}
		return fail(err, 2)
	}
	defer func() { _ = rt.Close() }()

	info, restore, notice, err := chatSession(ctx, rt, agentName, *cont, *resume)
	if err != nil {
		return fail(err, 2)
	}
	leases := newLeaseSet(rt)
	defer leases.releaseAll()
	if err := leases.ensure(ctx, info.ID); err != nil {
		return fail(err, 2)
	}
	agentName = info.Agent
	model := rt.AgentModel(agentName)
	if missingEnv != "" {
		if notice != "" {
			notice += " · "
		}
		notice += missingEnv + " is not set; using MockLlm. Set the key and run again to use a real model."
	}
	if *yolo {
		if err := rt.SetPolicy(ctx, info.ID, core.PolicyTrust); err != nil {
			return fail(err, 2)
		}
	}
	policy := rt.AgentPolicy(agentName)
	if effective, policyErr := rt.Policy(ctx, info.ID); policyErr == nil {
		policy = string(effective)
	}
	cwd, _ := os.Getwd()
	options := tui.Options{
		Backend:   chatBackend{rt: rt, agent: agentName, home: home, leases: leases},
		Approver:  approver,
		Asker:     asker,
		Agent:     agentName,
		Model:     model,
		Policy:    policy,
		SessionID: info.ID,
		Cwd:       cwd,
		Version:   version,
		Home:      home,
		Notice:    notice,
		Verbose:   *verbose,
		Restore:   restore,
		In:        os.Stdin,
		Out:       os.Stdout,
	}
	if err := tui.Run(ctx, options); err != nil {
		return fail(err, 1)
	}
	return 0
}

// chatSession opens or creates a chat session.
func chatSession(ctx context.Context, rt *lana.Runtime, agentName string, cont bool, resume string) (core.SessionInfo, []core.SessionEvent, string, error) {
	cwd, _ := os.Getwd()
	if cwd != "" {
		if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
			cwd = resolved
		}
	}
	switch {
	case resume != "":
		info, err := rt.SessionInfo(ctx, resume)
		if err != nil {
			return core.SessionInfo{}, nil, "", fmt.Errorf("session %q not found: %w", resume, err)
		}
		if cwd != "" && info.Address != "" && info.Address != cwd {
			return core.SessionInfo{}, nil, "", fmt.Errorf("session %s was created in %s; run chat from that directory", info.ID, info.Address)
		}
		events, _ := rt.History(ctx, info.ID)
		return info, restoreEvents(events), "", nil
	case cont:
		info, err := rt.LastSession(ctx)
		if err == nil {
			events, _ := rt.History(ctx, info.ID)
			return info, restoreEvents(events), "", nil
		}
		if !errors.Is(err, lana.ErrSessionNotFound) {
			return core.SessionInfo{}, nil, "", err
		}
	}
	info, err := rt.CreateSession(ctx, agentName)
	if err != nil {
		return core.SessionInfo{}, nil, "", err
	}
	notice := ""
	if cont {
		notice = "· no previous session; created a new one"
	}
	return info, nil, notice, nil
}

// restoreEvents takes the last few events to display again.
func restoreEvents(events []core.SessionEvent) []core.SessionEvent {
	const limit = 80
	out := make([]core.SessionEvent, 0, limit)
	for i := len(events) - 1; i >= 0 && len(out) < limit; i-- {
		switch events[i].Kind {
		case core.KindUserMessage, core.KindAssistantMessage:
			out = append(out, events[i])
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// chatBackend adapts *lana.Runtime to the TUI port.
type chatBackend struct {
	rt     *lana.Runtime
	agent  string
	home   string
	leases *leaseSet
}

func (b chatBackend) CreateSession(ctx context.Context, agent string) (core.SessionInfo, error) {
	if agent == "" {
		agent = b.agent
	}
	return b.rt.CreateSession(ctx, agent)
}

func (b chatBackend) LastSession(ctx context.Context) (core.SessionInfo, error) {
	return b.rt.LastSession(ctx)
}

func (b chatBackend) RunTurn(ctx context.Context, sessionID, prompt string) (tui.TurnResult, error) {
	if b.leases != nil {
		if err := b.leases.ensure(ctx, sessionID); err != nil {
			return tui.TurnResult{}, err
		}
	}
	result, err := b.rt.Run(ctx, lana.Request{Prompt: prompt, SessionID: sessionID})
	return tui.TurnResult{
		Text:         result.Text,
		StopReason:   result.StopReason,
		InputTokens:  int64(result.Usage.InputTokens),
		OutputTokens: int64(result.Usage.OutputTokens),
	}, err
}

func (b chatBackend) Send(ctx context.Context, sessionID, text string) error {
	return b.rt.Send(ctx, sessionID, text)
}

func (b chatBackend) Cancel(ctx context.Context, sessionID string) error {
	return b.rt.Cancel(ctx, sessionID)
}

func (b chatBackend) SubscribeLive(ctx context.Context, sessionID string) (*core.Subscription[core.LiveFrame], error) {
	return b.rt.SubscribeLive(ctx, sessionID)
}

func (b chatBackend) Policy(ctx context.Context, sessionID string) (core.Policy, error) {
	return b.rt.Policy(ctx, sessionID)
}

func (b chatBackend) SetPolicy(ctx context.Context, sessionID string, policy core.Policy) error {
	return b.rt.SetPolicy(ctx, sessionID, policy)
}

func (b chatBackend) History(ctx context.Context, sessionID string) ([]core.SessionEvent, error) {
	return b.rt.History(ctx, sessionID)
}

func (b chatBackend) SessionInfo(ctx context.Context, sessionID string) (core.SessionInfo, error) {
	return b.rt.SessionInfo(ctx, sessionID)
}

func (b chatBackend) ListSessions(ctx context.Context, limit int) ([]core.SessionInfo, error) {
	return b.rt.ListSessions(ctx, limit)
}

func (b chatBackend) ForkSession(ctx context.Context, sessionID string) (core.SessionInfo, error) {
	return b.rt.ForkSession(ctx, sessionID)
}

func (b chatBackend) SetAgent(ctx context.Context, sessionID, agent string) error {
	return b.rt.SetAgent(ctx, sessionID, agent)
}

func (b chatBackend) SetModel(ctx context.Context, sessionID, model string) error {
	return b.rt.SetModel(ctx, sessionID, model)
}

func (b chatBackend) Compact(ctx context.Context, sessionID string) (tui.CompactionInfo, error) {
	result, err := b.rt.Compact(ctx, sessionID)
	return tui.CompactionInfo{TokensBefore: result.TokensBefore, TokensAfter: result.TokensAfter}, err
}

// AgentNames returns the agents and teams that /agent accepts.
func (b chatBackend) AgentNames() []string {
	return append(b.rt.AgentNames(), b.rt.TeamNames()...)
}

func (b chatBackend) AgentModel(agent string) string { return b.rt.AgentModel(agent) }

func (b chatBackend) AgentPolicy(agent string) string { return b.rt.AgentPolicy(agent) }

func (b chatBackend) Memory(agent string) ([]string, []string, error) {
	paths := agenthome.Paths(b.home, agent)
	user, err := agenthome.ReadEntries(agenthome.SharedUser(b.home))
	if err != nil {
		return nil, nil, err
	}
	memory, err := agenthome.ReadEntries(paths.Memory)
	return user, memory, err
}

func (b chatBackend) MCPStatus(agent string) []core.MCPServerStatus {
	return b.rt.MCPStatus(agent)
}

func (b chatBackend) Skills(agent string) []agenthome.Skill {
	return b.rt.Skills(agent)
}

var _ tui.Backend = chatBackend{}
