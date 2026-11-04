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
	"sync"
	"syscall"
	"time"

	"github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/channels/telegram"
	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
	"github.com/ridhomujizat/lana-harness/internal/browser"
	"github.com/ridhomujizat/lana-harness/internal/logring"
	"github.com/ridhomujizat/lana-harness/web"
)

// shutdownGrace is how long the daemon waits for channels after a stop signal.
const shutdownGrace = 10 * time.Second

// logBufferSize is the number of WARN+ log records the daemon keeps in memory
// for the dashboard (REQ-DSH-009).
const logBufferSize = 500

// openBrowser opens the sign-in link on start (REQ-WLG-001). It is a variable
// so tests can replace it and never start a real browser.
var openBrowser = browser.Open

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
	// The buffer keeps the newest WARN+ records in memory for the dashboard
	// while the text handler still writes every record to stderr (REQ-DSH-009).
	logBuffer := logring.New(logBufferSize)
	slog.SetDefault(slog.New(logring.Handler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}), logBuffer)))
	slog.Info("lana daemon starting", "version", version, "commit", commit, "build_date", buildDate)
	// The first SIGTERM/SIGINT starts a stop drain; the next forces it
	// (REQ-TFB-002).
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	return runDaemonWithLogs(context.Background(), os.Stderr, signals, logBuffer)
}

// runDaemon runs the daemon until ctx ends and returns the exit code
// (API-DMN-001). A caller that cancels ctx stops the daemon at once; the
// signal-driven drain lives in cmdDaemon (REQ-TFB-002).
func runDaemon(ctx context.Context, stderr io.Writer) int {
	return runDaemonWithLogs(ctx, stderr, nil, nil)
}

// runDaemonSignals runs the daemon without a log buffer. signals is the
// shutdown channel of cmdDaemon; nil means the caller cancels ctx instead.
func runDaemonSignals(ctx context.Context, stderr io.Writer, signals <-chan os.Signal) int {
	return runDaemonWithLogs(ctx, stderr, signals, nil)
}

// runDaemonWithLogs runs the daemon. logBuffer collects the daemon's WARN+
// records for the dashboard; nil disables the daemon log section.
func runDaemonWithLogs(ctx context.Context, stderr io.Writer, signals <-chan os.Signal, logBuffer *logring.Buffer) int {
	errorf := func(code int, format string, args ...any) int {
		_, _ = fmt.Fprintf(stderr, "error: "+format+"\n", args...)
		return code
	}
	home, err := homeDir()
	if err != nil {
		return errorf(1, "%v", err)
	}
	lock, err := openDaemonLock(home)
	if err != nil {
		return errorf(1, "%v", err)
	}
	defer lock.release()
	restart := newRestartController(lock)

	// runCtx cancels turns and channels when the daemon stops or restarts.
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	result, err := config.Load(runCtx, config.Options{Home: home})
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
	var webServer *web.Server
	if result.Config.Web.Enabled {
		webServer, err = web.New(web.Config{
			Addr:        result.Config.Web.Addr,
			Enabled:     true,
			Home:        home,
			Stderr:      stderr,
			OpenBrowser: result.Config.Web.OpenBrowser,
		})
		if err != nil {
			return errorf(2, "%v", err)
		}
	}
	webStarted := false
	var webOnce sync.Once
	shutdownWeb := func(shutdownCtx context.Context) {
		if webServer == nil || !webStarted {
			return
		}
		webOnce.Do(func() {
			if err := webServer.Shutdown(shutdownCtx); err != nil {
				slog.Warn("daemon: web server did not stop in time", "error", err)
			}
		})
	}

	raw, hasTelegram := result.Config.Channels["telegram"]
	var tgcfg telegram.Config
	if hasTelegram {
		tgcfg, err = telegram.ParseConfig(raw, result.Config.Routes)
		if err != nil {
			return errorf(2, "%v", err)
		}
	}
	// A disabled channel counts as no channel (REQ-DMN-003, REQ-DMN-005).
	telegramEnabled := hasTelegram && tgcfg.Enabled
	if !telegramEnabled && webServer == nil {
		if hasTelegram {
			return errorf(2, "no channel enabled; set [channels.telegram] enabled = true or enable [web]")
		}
		return errorf(2, "no channel configured; add [channels.telegram] to config.toml")
	}

	// The token is checked only when the channel is enabled (REQ-DMN-005). A
	// missing token keeps the web view running unless the web view is off
	// (REQ-DMN-006).
	token := ""
	tokenMissing := false
	if telegramEnabled {
		if secret, ok := result.Secrets.Lookup(tgcfg.TokenEnv); ok {
			token = strings.TrimSpace(secret.Reveal())
		}
		if token == "" {
			if webServer == nil {
				return errorf(2, "telegram: %s is not set", tgcfg.TokenEnv)
			}
			tokenMissing = true
			_, _ = fmt.Fprintf(stderr, "telegram: %s is not set; channel stays off until the daemon restarts with the token\n", tgcfg.TokenEnv)
		}
	}
	telegramActive := telegramEnabled && !tokenMissing

	var channel *telegram.Channel
	var rt *lana.Runtime
	router := &ApprovalRouter{
		sessionInfo: func(ctx context.Context, sessionID string) (core.SessionInfo, error) {
			if rt == nil {
				return core.SessionInfo{}, errors.New("runtime is not ready")
			}
			return rt.SessionInfo(ctx, sessionID)
		},
	}
	if telegramActive {
		router.telegram = func(ctx context.Context, req core.ApprovalRequest) core.ApprovalOutcome {
			if channel == nil {
				return core.OutcomeUnavailable
			}
			return channel.Approve(ctx, req)
		}
	}
	if webServer != nil {
		router.web = webServer.Approvals().Ask
	}
	questionRouter := &QuestionRouter{
		sessionInfo: func(ctx context.Context, sessionID string) (core.SessionInfo, error) {
			if rt == nil {
				return core.SessionInfo{}, errors.New("runtime is not ready")
			}
			return rt.SessionInfo(ctx, sessionID)
		},
	}
	if telegramActive {
		questionRouter.telegram = func(ctx context.Context, req core.QuestionRequest) core.QuestionOutcome {
			if channel == nil {
				return noQuestionResponder()
			}
			return channel.Ask(ctx, req)
		}
	}
	if webServer != nil {
		questionRouter.web = webQuestionResponder(webServer.Questions())
	}
	rt, _, _, _, err = runtimeFor(runCtx, home, "", router.Approve, lana.ApproveChannels("telegram", "web", "workflow"), lana.AskWith(questionRouter.Ask), lana.AskChannels("telegram", "web", "workflow"))
	if err != nil {
		return errorf(2, "%v", err)
	}
	defer func() { _ = rt.Close() }()
	leases := newLeaseSet(rt)
	defer leases.releaseAll()
	// The drain gate refuses new turns while the daemon waits (REQ-TFB-002).
	drain := newDrainController(rt, daemonDrainTimeout)

	if telegramActive {
		channel = telegram.New(tgcfg, token, &daemonBackend{rt: rt, home: home, leases: leases})
		// REQ-WF-005, REQ-WF-008, API-WF-022: the owner answers workflow
		// decisions and commands in Telegram; the workflow notice tells them an
		// approval is waiting on the web.
		channel.SetWorkflowHooks(workflowHooks(rt))
		rt.Workflows().SetNotifier(telegramWorkflowNotifier{ch: channel})
		router.workflowNotice = telegramWorkflowNotice(channel)
		bot, err := channel.Start(runCtx)
		if errors.Is(err, telegram.ErrUnauthorized) {
			return errorf(2, "telegram: invalid bot token")
		}
		if err != nil {
			if runCtx.Err() != nil {
				return 0
			}
			return errorf(1, "telegram: %v", err)
		}
		_, _ = fmt.Fprintf(stderr, "telegram: polling as @%s\n", bot.Username)
	}

	if webServer != nil {
		backend := newWebBackend(rt, home, leases, runCtx)
		backend.SetLogBuffer(logBuffer)
		backend.SetRestartController(restart)
		backend.SetDrainController(drain)
		webServer.SetBackend(backend)
		// REQ-WF-011, API-WF-001: the Workflows page reads the service and lists
		// its approval decisions under Needs attention.
		webServer.SetWorkflows(rt.Workflows())
		webServer.SetDaemonStore(backend)
		webServer.SetSecretStore(backend)
		webServer.SetAgentFileStore(backend)
		webServer.SetSkillStore(backend)
		if telegramActive {
			webServer.SetTelegramStatus(telegramStatusFunc(tgcfg, true, channel))
			webServer.SetTelegramResetSender(newTelegramResetSender(channel, tgcfg))
		} else {
			webServer.SetTelegramStatus(telegramStatusFunc(tgcfg, false, nil))
		}
		if code := startWeb(runCtx, webServer, stderr); code != 0 {
			return code
		}
		// The local control secret lets `lana web reset-login` reach this
		// daemon without a browser session (API-WLG-008, REQ-WLG-006).
		localSecret, err := newLocalSecret()
		if err != nil {
			return errorf(1, "web: %v", err)
		}
		webServer.SetLocalSecret(localSecret)
		if err := writeDaemonSecret(home, webServer.Addr(), localSecret); err != nil {
			return errorf(1, "web: %v", err)
		}
		// REQ-WLG-001: open a sign-in link when no password is set or a reset
		// was requested. A plain restart-in-place satisfies neither and stays
		// quiet.
		webServer.OpenOnStart(openBrowser)
		webStarted = true
		// The secret file is removed on a stop. A restart in place replaces the
		// process image with syscall.Exec, so this defer does not run and the
		// new image rewrites the file.
		defer removeDaemonSecret(home)
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
			defer cancel()
			shutdownWeb(shutdownCtx)
		}()
	}

	// REQ-KNU-011, REQ-KNU-020: the daemon owns the knowledge upkeep loop. It
	// starts after the channels and stops with runCtx when the daemon does.
	rt.StartKnowledgeUpkeep(runCtx)

	// REQ-WF-003, REQ-WF-007: the daemon owns the workflow scheduler, durable
	// resume and event-trigger bridge. They start after the channels and stop
	// with runCtx when the daemon does.
	rt.StartWorkflows(runCtx)

	// restartNow runs the same shutdown as SIGTERM and then replaces the
	// process image. The daemon lock stays held (REQ-WEB-035).
	restartNow := func(done <-chan error) int {
		cancelRun()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		shutdownWeb(shutdownCtx)
		if done != nil {
			select {
			case err = <-done:
			case <-shutdownCtx.Done():
				slog.Warn("daemon: channels did not stop in time")
			}
		}
		cancel()
		leases.releaseAll()
		if err := rt.Close(); err != nil {
			slog.Warn("daemon: runtime did not stop cleanly", "error", err)
		}
		return restartInPlace(lock, stderr)
	}

	// handleSignal starts a stop drain, forces a running one, and turns a
	// pending restart into a stop (REQ-TFB-002).
	handleSignal := func() {
		status := drain.status()
		switch {
		case status.State == drainIdle:
			drain.begin(drainStop)
		case status.Reason == drainStop:
			drain.force()
		default:
			// A restart drain is pending; a stop signal turns it into a stop.
			if drain.cancel() {
				drain.begin(drainStop)
			}
			drain.force()
		}
	}

	var done chan error
	if telegramActive {
		done = make(chan error, 1)
		go func() { done <- channel.Run(runCtx) }()
	}

	// The web server and the channel keep running during a drain so new
	// messages get the draining reply; runCtx stops only after it completes.
drainLoop:
	for {
		select {
		case err = <-done:
			break drainLoop
		case <-ctx.Done():
			// The caller canceled the daemon without a signal; keep the old
			// immediate stop used by tests and embedders.
			break drainLoop
		case <-restart.requested():
			return restartNow(done)
		case <-drain.stoppingSignaled():
			if drain.status().Reason == drainRestart {
				return restartNow(done)
			}
			break drainLoop
		case <-signals:
			handleSignal()
		}
	}

	cancelRun()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	shutdownWeb(shutdownCtx)
	if telegramActive {
		select {
		case err = <-done:
		case <-shutdownCtx.Done():
			slog.Warn("daemon: channels did not stop in time")
			cancel()
			return 0
		}
	}
	cancel()
	if errors.Is(err, telegram.ErrUnauthorized) {
		return errorf(2, "telegram: invalid bot token")
	}
	if err != nil {
		return errorf(1, "%v", err)
	}
	return 0
}

// telegramStatusFunc builds the reader the daemon gives the web view
// (API-TG-007). A running channel reports its live snapshot; otherwise the
// state comes from the config and whether the token is present.
func telegramStatusFunc(cfg telegram.Config, tokenPresent bool, channel *telegram.Channel) func() web.TelegramStatus {
	return func() web.TelegramStatus {
		if channel != nil {
			return webTelegramStatus(channel.Status())
		}
		return webTelegramStatus(telegram.StatusFor(cfg, tokenPresent))
	}
}

// webTelegramStatus converts the channel snapshot into the web view model.
func webTelegramStatus(status telegram.Status) web.TelegramStatus {
	converted := web.TelegramStatus{
		State:         status.State,
		BotUsername:   status.BotUsername,
		ChatCount:     status.ChatCount,
		OwnerCount:    status.OwnerCount,
		PollingSince:  status.PollingSince,
		LastMessageAt: status.LastMessageAt,
		PendingChats:  make([]web.TelegramPendingChat, 0, len(status.PendingChats)),
	}
	for _, chat := range status.PendingChats {
		converted.PendingChats = append(converted.PendingChats, web.TelegramPendingChat{
			ChatID:    chat.ChatID,
			Type:      chat.Type,
			Name:      chat.Name,
			UserID:    chat.UserID,
			Username:  chat.Username,
			FirstSeen: chat.FirstSeen,
			LastSeen:  chat.LastSeen,
			Messages:  chat.Messages,
		})
	}
	return converted
}

// startWeb binds the web listener and prints the contract start line.
func startWeb(ctx context.Context, server *web.Server, stderr io.Writer) int {
	if err := server.Start(ctx); err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			_, _ = fmt.Fprintln(stderr, "error: web: address already in use")
		} else {
			_, _ = fmt.Fprintf(stderr, "error: web: %v\n", err)
		}
		return 1
	}
	_, _ = fmt.Fprintf(stderr, "web: listening on http://%s\n", server.Addr())
	return 0
}

// daemonLock is the open $LANA_HOME/daemon.lock. Its flock keeps one daemon per
// home, and the descriptor survives an in-place restart (REQ-WEB-035).
type daemonLock struct {
	file *os.File
	path string
}

// lockDaemon takes $LANA_HOME/daemon.lock and writes the PID into it. It is the
// acquire-only form used by tests; the daemon uses openDaemonLock.
func lockDaemon(home string) (func(), error) {
	lock, err := acquireDaemonLock(home)
	if err != nil {
		return nil, err
	}
	return lock.release, nil
}

// openDaemonLock adopts the descriptor inherited across an in-place restart
// when LANA_RESTART_LOCK_FD is set, or takes daemon.lock otherwise.
func openDaemonLock(home string) (*daemonLock, error) {
	raw := os.Getenv(restartLockFDEnv)
	if raw == "" {
		return acquireDaemonLock(home)
	}
	// The new image owns the descriptor. Unset the variable so child processes
	// do not inherit it (design.md, Restart in place).
	_ = os.Unsetenv(restartLockFDEnv)
	fd, err := strconv.Atoi(raw)
	if err != nil || fd < 3 {
		return nil, fmt.Errorf("invalid %s %q", restartLockFDEnv, raw)
	}
	return adoptDaemonLock(home, fd)
}

// acquireDaemonLock opens and flocks daemon.lock.
func acquireDaemonLock(home string) (*daemonLock, error) {
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
	lock := &daemonLock{file: file, path: path}
	if err := lock.writePID(); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, err
	}
	return lock, nil
}

// adoptDaemonLock wraps the inherited flock descriptor without taking the lock
// again. It restores close-on-exec so tool processes do not inherit the lock.
func adoptDaemonLock(home string, fd int) (*daemonLock, error) {
	path := filepath.Join(home, "daemon.lock")
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		return nil, fmt.Errorf("invalid %s %d", restartLockFDEnv, fd)
	}
	lock := &daemonLock{file: file, path: path}
	if err := lock.check(); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inherited daemon lock: %w", err)
	}
	if err := lock.writePID(); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := lock.setCloseOnExec(true); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inherited daemon lock: %w", err)
	}
	return lock, nil
}

// check reports whether the lock descriptor is still open.
func (l *daemonLock) check() error {
	if l == nil || l.file == nil {
		return errors.New("daemon lock is not held")
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, l.file.Fd(), syscall.F_GETFD, 0); errno != 0 {
		return errno
	}
	return nil
}

// setCloseOnExec toggles FD_CLOEXEC on the lock descriptor.
func (l *daemonLock) setCloseOnExec(on bool) error {
	return setCloseOnExec(l.fd(), on)
}

func setCloseOnExec(fd int, on bool) error {
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
	if errno != 0 {
		return errno
	}
	if on {
		flags |= syscall.FD_CLOEXEC
	} else {
		flags &^= syscall.FD_CLOEXEC
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFD, flags); errno != 0 {
		return errno
	}
	return nil
}

func (l *daemonLock) fd() int { return int(l.file.Fd()) }

func (l *daemonLock) writePID() error {
	_ = l.file.Truncate(0)
	_, err := l.file.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	return err
}

func (l *daemonLock) release() {
	if l == nil || l.file == nil {
		return
	}
	_ = os.Remove(l.path)
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
}

// ApprovalRouter routes approval requests by the session's channel (G-2). It
// is the single responder passed to the runtime; a channel that is not
// configured, or whose lookup fails, is unavailable and fails closed.
type ApprovalRouter struct {
	sessionInfo func(context.Context, string) (core.SessionInfo, error)
	telegram    func(context.Context, core.ApprovalRequest) core.ApprovalOutcome
	web         func(context.Context, core.ApprovalRequest) core.ApprovalOutcome
	// workflowNotice sends a Telegram heads-up for a channel "workflow"
	// approval; the answer happens on the web (design §Wiring, REQ-WF-009).
	// It runs in a goroutine and never blocks the approval.
	workflowNotice func(ctx context.Context, req core.ApprovalRequest, info core.SessionInfo)
}

// Approve routes req to the responder of the session's channel.
func (r *ApprovalRouter) Approve(ctx context.Context, req core.ApprovalRequest) core.ApprovalOutcome {
	if r == nil || r.sessionInfo == nil {
		return core.OutcomeUnavailable
	}
	info, err := r.sessionInfo(ctx, req.SessionID)
	if err != nil {
		return core.OutcomeUnavailable
	}
	req.SessionID = info.ID
	switch info.Channel {
	case "telegram":
		if r.telegram != nil {
			return r.telegram(ctx, req)
		}
	case "web":
		if r.web != nil {
			return r.web(ctx, req)
		}
	case "workflow":
		// The owner answers on the web; Telegram only gets a heads-up, sent
		// off the approval path.
		if r.workflowNotice != nil {
			notice, request := r.workflowNotice, req
			go notice(context.WithoutCancel(ctx), request, info)
		}
		if r.web != nil {
			return r.web(ctx, req)
		}
	}
	return core.OutcomeUnavailable
}

// QuestionRouter routes question requests by the session's channel. It is the
// single responder passed to the runtime; a channel that is not configured, or
// whose lookup fails, is unavailable and fails closed.
type QuestionRouter struct {
	sessionInfo func(context.Context, string) (core.SessionInfo, error)
	telegram    func(context.Context, core.QuestionRequest) core.QuestionOutcome
	web         func(context.Context, core.QuestionRequest) core.QuestionOutcome
}

// Ask routes req to the responder of the session's channel.
func (r *QuestionRouter) Ask(ctx context.Context, req core.QuestionRequest) core.QuestionOutcome {
	if r == nil || r.sessionInfo == nil {
		return noQuestionResponder()
	}
	info, err := r.sessionInfo(ctx, req.SessionID)
	if err != nil {
		return noQuestionResponder()
	}
	req.SessionID = info.ID
	switch info.Channel {
	case "telegram":
		if r.telegram != nil {
			return r.telegram(ctx, req)
		}
	case "web", "workflow":
		// A workflow session's question is answered on the web (design §Wiring).
		if r.web != nil {
			return r.web(ctx, req)
		}
	}
	return noQuestionResponder()
}

// questionBroker is the web question broker as the router sees it.
type questionBroker interface {
	Ask(context.Context, core.QuestionRequest) (core.QuestionOutcome, error)
}

// webQuestionResponder adapts the web broker to the router's outcome-only
// signature. A broker error, like a missing responder, is no_responder.
func webQuestionResponder(broker questionBroker) func(context.Context, core.QuestionRequest) core.QuestionOutcome {
	return func(ctx context.Context, req core.QuestionRequest) core.QuestionOutcome {
		outcome, err := broker.Ask(ctx, req)
		if err != nil {
			return noQuestionResponder()
		}
		return outcome
	}
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

func (b *daemonBackend) RunTurn(ctx context.Context, sessionID, prompt string, attachments ...string) (telegram.TurnResult, error) {
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
	result, err := b.rt.Run(ctx, lana.Request{Prompt: prompt, SessionID: sessionID, Attachments: attachments})
	return telegram.TurnResult{
		Text: result.Text, StopReason: result.StopReason,
		InputTokens: int64(result.Usage.InputTokens), OutputTokens: int64(result.Usage.OutputTokens),
	}, err
}

func (b *daemonBackend) Send(ctx context.Context, sessionID, text string, attachments ...string) error {
	return b.rt.Send(ctx, sessionID, text, attachments...)
}

// CanContinue reports why a continue turn may not start (API-TFB-003). The
// Telegram callback answers a press with the returned message.
func (b *daemonBackend) CanContinue(ctx context.Context, sessionID string) error {
	return b.rt.CanContinue(ctx, sessionID)
}

// ContinueTurn runs a continue turn for the session and blocks until it ends
// (API-TFB-005).
func (b *daemonBackend) ContinueTurn(ctx context.Context, sessionID string) (telegram.TurnResult, error) {
	// The runtime turn does not follow ctx; cancel it explicitly when ctx ends
	// (shutdown or a /cancel press).
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			_ = b.rt.Cancel(context.Background(), sessionID)
		case <-finished:
		}
	}()
	result, err := b.rt.Continue(ctx, sessionID)
	return telegram.TurnResult{
		Text: result.Text, StopReason: result.StopReason,
		InputTokens: int64(result.Usage.InputTokens), OutputTokens: int64(result.Usage.OutputTokens),
	}, err
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
	user, err := agenthome.ReadEntries(agenthome.SharedUser(b.home))
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

// SaveKnowledge stores a Telegram /save item as a knowledge source and starts
// its compile (REQ-KNU-012, API-KNU-002).
func (b *daemonBackend) SaveKnowledge(ctx context.Context, address, agent string, item telegram.SaveItem) (string, bool, error) {
	result, err := b.rt.IngestKnowledge(ctx, lana.KnowledgeSave{
		Address: address,
		Channel: "telegram",
		Agent:   agent,
		Title:   item.Title,
		Ext:     item.Ext,
		Data:    item.Data,
		URL:     item.URL,
		Text:    item.Text,
	})
	if err != nil {
		return "", false, err
	}
	return result.Path, result.Duplicate, nil
}

func (b *daemonBackend) AgentNames() []string           { return b.rt.AgentNames() }
func (b *daemonBackend) AgentModel(agent string) string { return b.rt.AgentModel(agent) }

// KnowledgeStatus implements web.KnowledgeBackend by delegating to the runtime
// (API-KNU-003, REQ-KNU-040).
func (b *daemonBackend) KnowledgeStatus(ctx context.Context) (core.KnowledgeStatus, error) {
	return b.rt.KnowledgeStatus(ctx)
}

// KnowledgeReport implements web.KnowledgeBackend (API-KNU-004).
func (b *daemonBackend) KnowledgeReport(ctx context.Context) (web.KnowledgeReport, error) {
	report, err := b.rt.KnowledgeReport(ctx)
	if err != nil {
		return web.KnowledgeReport{}, err
	}
	return web.KnowledgeReport{Path: report.Path, Markdown: report.Markdown}, nil
}

// KnowledgeUpload implements web.KnowledgeBackend (API-KNU-005).
func (b *daemonBackend) KnowledgeUpload(ctx context.Context, name string, data []byte) (string, error) {
	return b.rt.KnowledgeUpload(ctx, name, data)
}

// KnowledgeConsolidate implements web.KnowledgeBackend (API-KNU-006).
func (b *daemonBackend) KnowledgeConsolidate(ctx context.Context) error {
	return knowledgeConsolidateError(b.rt.KnowledgeConsolidate(ctx))
}

// Draining reports the runtime gate so the Telegram text path refuses new
// turns while the daemon shuts down (REQ-TFB-002, API-TFB-008).
func (b *daemonBackend) Draining() bool { return b.rt.Draining() }

var _ telegram.Backend = (*daemonBackend)(nil)
var _ telegram.KnowledgeSaver = (*daemonBackend)(nil)
var _ web.KnowledgeBackend = (*daemonBackend)(nil)
