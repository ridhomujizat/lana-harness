package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/channels/telegram"
	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

// shutdownGrace is how long the daemon waits for channels after a stop signal.
const shutdownGrace = 10 * time.Second

func cmdDaemon(args []string) int {
	if len(args) > 0 {
		fmt.Fprintln(os.Stderr, "error: use lana daemon")
		return 2
	}
	level := slog.LevelInfo
	switch strings.ToLower(os.Getenv("LANA_LOG")) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runDaemon(ctx, os.Stderr)
}

// runDaemon runs the daemon until ctx ends and returns the exit code (API-DMN-001).
func runDaemon(ctx context.Context, stderr io.Writer) int {
	errorf := func(code int, format string, args ...any) int {
		_, _ = fmt.Fprintf(stderr, "error: "+format+"\n", args...)
		return code
	}
	home, err := homeDir()
	if err != nil {
		return errorf(1, "%v", err)
	}
	unlock, err := lockDaemon(home)
	if err != nil {
		return errorf(1, "%v", err)
	}
	defer unlock()

	result, err := config.Load(ctx, config.Options{Home: home})
	if err != nil {
		var errs config.Errors
		if errors.As(err, &errs) {
			for _, item := range errs {
				_, _ = fmt.Fprintf(stderr, "error: %s\n", item.Error())
			}
			return 2
		}
		return errorf(2, "%v", err)
	}
	raw, ok := result.Config.Channels["telegram"]
	if !ok {
		return errorf(2, "no channel configured; add [channels.telegram] to config.toml")
	}
	tgcfg, err := telegram.ParseConfig(raw, result.Config.Routes)
	if err != nil {
		return errorf(2, "%v", err)
	}
	token := ""
	if secret, ok := result.Secrets.Lookup(tgcfg.TokenEnv); ok {
		token = strings.TrimSpace(secret.Reveal())
	}
	if token == "" {
		return errorf(2, "telegram: %s is not set", tgcfg.TokenEnv)
	}

	var channel *telegram.Channel
	approve := func(ctx context.Context, req core.ApprovalRequest) core.ApprovalOutcome {
		if channel == nil {
			return core.OutcomeUnavailable
		}
		return channel.Approve(ctx, req)
	}
	rt, _, _, _, err := runtimeFor(ctx, home, "", approve, lana.ApproveChannels("telegram"))
	if err != nil {
		return errorf(2, "%v", err)
	}
	defer func() { _ = rt.Close() }()
	leases := newLeaseSet(rt)
	defer leases.releaseAll()

	channel = telegram.New(tgcfg, token, &daemonBackend{rt: rt, home: home, leases: leases})
	bot, err := channel.Start(ctx)
	if errors.Is(err, telegram.ErrUnauthorized) {
		return errorf(2, "telegram: invalid bot token")
	}
	if err != nil {
		if ctx.Err() != nil {
			return 0
		}
		return errorf(1, "telegram: %v", err)
	}
	_, _ = fmt.Fprintf(stderr, "telegram: polling as @%s\n", bot.Username)

	done := make(chan error, 1)
	go func() { done <- channel.Run(ctx) }()
	select {
	case err = <-done:
	case <-ctx.Done():
		select {
		case err = <-done:
		case <-time.After(shutdownGrace):
			slog.Warn("daemon: channels did not stop in time")
			return 0
		}
	}
	if errors.Is(err, telegram.ErrUnauthorized) {
		return errorf(2, "telegram: invalid bot token")
	}
	if err != nil {
		return errorf(1, "%v", err)
	}
	return 0
}

// lockDaemon takes $LANA_HOME/daemon.lock and writes the PID into it.
func lockDaemon(home string) (func(), error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(home, "daemon.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		data, _ := os.ReadFile(path)
		_ = file.Close()
		pid := strings.TrimSpace(string(data))
		if pid == "" {
			pid = "unknown"
		}
		return nil, fmt.Errorf("daemon already running (pid %s)", pid)
	}
	_ = file.Truncate(0)
	_, _ = file.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	return func() {
		_ = os.Remove(path)
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

// daemonBackend adapts *lana.Runtime to the Telegram channel port.
type daemonBackend struct {
	rt     *lana.Runtime
	home   string
	leases *leaseSet
}

func (b *daemonBackend) ActiveAgent(ctx context.Context, address string) (string, error) {
	return b.rt.ActiveAgent(ctx, "telegram", address)
}

func (b *daemonBackend) ChatSession(ctx context.Context, address, agent string) (core.SessionInfo, error) {
	return b.rt.ChannelSession(ctx, "telegram", address, agent)
}

func (b *daemonBackend) NewSession(ctx context.Context, address, agent string) (core.SessionInfo, error) {
	return b.rt.CreateChannelSession(ctx, "telegram", address, agent)
}

func (b *daemonBackend) Lease(ctx context.Context, sessionID string) error {
	return b.leases.ensure(ctx, sessionID)
}

func (b *daemonBackend) SessionInfo(ctx context.Context, sessionID string) (core.SessionInfo, error) {
	return b.rt.SessionInfo(ctx, sessionID)
}

func (b *daemonBackend) RunTurn(ctx context.Context, sessionID, prompt string) (telegram.TurnResult, error) {
	// The runtime turn does not follow ctx; cancel it explicitly when ctx ends (shutdown).
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			_ = b.rt.Cancel(context.Background(), sessionID)
		case <-finished:
		}
	}()
	result, err := b.rt.Run(ctx, lana.Request{Prompt: prompt, SessionID: sessionID})
	return telegram.TurnResult{
		Text: result.Text, StopReason: result.StopReason,
		InputTokens: int64(result.Usage.InputTokens), OutputTokens: int64(result.Usage.OutputTokens),
	}, err
}

func (b *daemonBackend) Send(ctx context.Context, sessionID, text string) error {
	return b.rt.Send(ctx, sessionID, text)
}

func (b *daemonBackend) Cancel(ctx context.Context, sessionID string) error {
	return b.rt.Cancel(ctx, sessionID)
}

func (b *daemonBackend) SubscribeLive(ctx context.Context, sessionID string) (*core.Subscription[core.LiveFrame], error) {
	return b.rt.SubscribeLive(ctx, sessionID)
}

func (b *daemonBackend) Compact(ctx context.Context, sessionID string) (int64, int64, error) {
	result, err := b.rt.Compact(ctx, sessionID)
	return result.TokensBefore, result.TokensAfter, err
}

func (b *daemonBackend) Memory(agent string) ([]string, []string, error) {
	paths := agenthome.Paths(b.home, agent)
	user, err := agenthome.ReadEntries(paths.User)
	if err != nil {
		return nil, nil, err
	}
	memory, err := agenthome.ReadEntries(paths.Memory)
	return user, memory, err
}

func (b *daemonBackend) Policy(ctx context.Context, sessionID string) (string, error) {
	policy, err := b.rt.Policy(ctx, sessionID)
	return string(policy), err
}

func (b *daemonBackend) MCPStatus(agent string) []core.MCPServerStatus {
	return b.rt.MCPStatus(agent)
}

func (b *daemonBackend) Skills(agent string) []agenthome.Skill {
	return b.rt.Skills(agent)
}

func (b *daemonBackend) AgentNames() []string           { return b.rt.AgentNames() }
func (b *daemonBackend) AgentModel(agent string) string { return b.rt.AgentModel(agent) }

var _ telegram.Backend = (*daemonBackend)(nil)
