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
)

// cmdChat menjalankan TUI chat interaktif.
func cmdChat(args []string) int {
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	agentFlag := fs.String("agent", "", "agent")
	yolo := fs.Bool("yolo", false, "policy trust")
	cont := fs.Bool("continue", false, "lanjutkan session terakhir untuk direktori ini")
	resume := fs.String("resume", "", "id atau prefix session")
	verbose := fs.Bool("verbose", false, "mode verbose")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !isTTY(os.Stdin) || !isTTY(os.Stdout) {
		fmt.Fprintln(os.Stderr, "error: lana chat butuh terminal; pakai 'lana run' untuk non-interaktif")
		return 2
	}
	home, err := homeDir()
	if err != nil {
		return fail(err, 1)
	}
	ctx := context.Background()
	agentName := *agentFlag
	if agentName == "" {
		if result, loadErr := config.Load(ctx, config.Options{Home: home}); loadErr == nil {
			for _, route := range result.Config.Routes {
				if route.Channel == "cli" {
					agentName = route.Agent
					break
				}
			}
		}
		if agentName == "" {
			agentName = "default"
		}
	}

	approver := tui.NewApprover()
	rt, providerName, _, err := runtimeFor(ctx, home, agentName, approver.Ask)
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
	agentName = info.Agent
	model := rt.AgentModel(agentName)
	if providerName == "mock" {
		if notice != "" {
			notice += " · "
		}
		notice += "provider belum siap; memakai MockLlm. Isi API key lalu jalankan ulang untuk model sungguhan."
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
		Backend:   chatBackend{rt: rt, agent: agentName},
		Approver:  approver,
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

// chatSession membuka atau membuat session chat.
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
			return core.SessionInfo{}, nil, "", fmt.Errorf("session %q tidak ditemukan: %w", resume, err)
		}
		if cwd != "" && info.Address != "" && info.Address != cwd {
			return core.SessionInfo{}, nil, "", fmt.Errorf("session %s dibuat di %s; jalankan chat dari direktori itu", info.ID, info.Address)
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
		notice = "· tidak ada session sebelumnya; session baru dibuat"
	}
	return info, nil, notice, nil
}

// restoreEvents mengambil sebagian event terakhir untuk ditampilkan ulang.
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

// chatBackend mengadaptasi *lana.Runtime ke port TUI.
type chatBackend struct {
	rt    *lana.Runtime
	agent string
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
	result, err := b.rt.Run(ctx, lana.Request{Agent: b.agent, Prompt: prompt, SessionID: sessionID})
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

func (b chatBackend) AgentNames() []string { return b.rt.AgentNames() }

func (b chatBackend) AgentModel(agent string) string { return b.rt.AgentModel(agent) }

func (b chatBackend) AgentPolicy(agent string) string { return b.rt.AgentPolicy(agent) }

var _ tui.Backend = chatBackend{}
