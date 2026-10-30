package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/channels/telegram"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/web"
)

// tgServer is a minimal fake Bot API for daemon tests.
type tgServer struct {
	srv   *httptest.Server
	mu    sync.Mutex
	texts []string
	sent  bool
	calls int
}

func newTGServer(t *testing.T, chatID int64, text string) *tgServer {
	s := &tgServer{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.calls++
		s.mu.Unlock()
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		var params map[string]any
		_ = json.NewDecoder(r.Body).Decode(&params)
		var result any = true
		switch method {
		case "getMe":
			result = map[string]any{"id": 999, "is_bot": true, "username": "lana_bot"}
		case "getUpdates":
			s.mu.Lock()
			first := !s.sent
			s.sent = true
			s.mu.Unlock()
			if first {
				result = []map[string]any{{"update_id": 1, "message": map[string]any{
					"message_id": 10, "from": map[string]any{"id": chatID, "first_name": "Ridho"},
					"chat": map[string]any{"id": chatID, "type": "private"}, "text": text,
				}}}
			} else {
				select {
				case <-r.Context().Done():
				case <-time.After(50 * time.Millisecond):
				}
				result = []any{}
			}
		case "sendMessage", "editMessageText":
			s.mu.Lock()
			s.texts = append(s.texts, fmt.Sprint(params["text"]))
			s.mu.Unlock()
			result = map[string]any{"message_id": 20}
		}
		data, _ := json.Marshal(map[string]any{"ok": true, "result": result})
		_, _ = w.Write(data)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *tgServer) waitText(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		for _, text := range s.texts {
			if strings.Contains(text, want) {
				s.mu.Unlock()
				return
			}
		}
		s.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t.Fatalf("no message contains %q; got %q", want, s.texts)
}

func (s *tgServer) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func writeDaemonConfig(t *testing.T, home, llmURL, tgURL string) {
	t.Helper()
	cfg := fmt.Sprintf(`[providers.local]
kind = "openai_compat"
base_url = %q

[agents.helper]
provider = "local"
model = "m"

[web]
enabled = false

[channels.telegram]
api_base = %q

[[routes]]
channel = "telegram"
match = { chat_id = 111 }
agent = "helper"
`, llmURL, tgURL)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeDaemonTelegramConfig writes a minimal config for the Telegram state
// tests. The route is optional so enabled = false can also be tested without
// one (REQ-DMN-005).
func writeDaemonTelegramConfig(t *testing.T, home, tgURL string, web, telegramEnabled, withRoute bool) {
	t.Helper()
	var cfg strings.Builder
	fmt.Fprintf(&cfg, `[providers.local]
kind = "openai_compat"
base_url = "http://127.0.0.1:1"

[agents.helper]
provider = "local"
model = "m"

[web]
enabled = %t
addr = "127.0.0.1:0"

[channels.telegram]
enabled = %t
api_base = %q
`, web, telegramEnabled, tgURL)
	if withRoute {
		cfg.WriteString(`
[[routes]]
channel = "telegram"
match = { chat_id = 111 }
agent = "helper"
`)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// waitStderr waits until the daemon wrote want to stderr.
func waitStderr(t *testing.T, w *syncWriter, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(w.String(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("stderr = %q, want %q", w.String(), want)
}

// getWebRoot checks that the web view is serving the dashboard.
func getWebRoot(t *testing.T, addr string) {
	t.Helper()
	resp, err := (&http.Client{Timeout: time.Second}).Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d", resp.StatusCode)
	}
}

type syncWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func TestDaemonConfigErrors(t *testing.T) {
	home := initHome(t)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[web]\nenabled = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr syncWriter
	if code := runDaemon(context.Background(), &stderr); code != 2 || !strings.Contains(stderr.String(), "no channel configured; add [channels.telegram] to config.toml") {
		t.Fatalf("no channel: code=%d stderr=%q", code, stderr.String())
	}
}

// TestDaemonTelegramDisabledWebOnly covers AC-DMN-005 and AC-TG-027: a
// disabled channel makes no Bot API call, the daemon keeps serving the web
// view, the status is off, and the routes stay in config.toml.
func TestDaemonTelegramDisabledWebOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	// A token is set on purpose: a disabled channel must not read or use it.
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	tg := newTGServer(t, 111, "hi")
	writeDaemonTelegramConfig(t, home, tg.srv.URL, true, false, true)

	ctx, cancel := context.WithCancel(context.Background())
	var stderr syncWriter
	done := make(chan int, 1)
	go func() { done <- runDaemon(ctx, &stderr) }()
	getWebRoot(t, webAddressFromStderr(t, &stderr))

	time.Sleep(100 * time.Millisecond)
	if got := tg.callCount(); got != 0 {
		t.Fatalf("disabled channel made %d Bot API calls", got)
	}
	if strings.Contains(stderr.String(), "telegram: polling") {
		t.Fatalf("disabled channel started polling: %q", stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[[routes]]") {
		t.Fatalf("routes were removed from config.toml: %q", data)
	}
	status := telegramStatusFunc(telegram.Config{Enabled: false, Routes: map[int64]string{111: "helper"}}, false, nil)()
	if status.State != telegram.StateOff || status.BotUsername != "" || status.PollingSince != nil {
		t.Fatalf("disabled status = %+v", status)
	}

	cancel()
	if code := waitDaemon(t, done); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}

// TestDaemonTelegramDisabledWebOff covers the "no channel enabled" row of
// API-DMN-001 for enabled = false with the web view off.
func TestDaemonTelegramDisabledWebOff(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	tg := newTGServer(t, 111, "hi")
	writeDaemonTelegramConfig(t, home, tg.srv.URL, false, false, false)

	var stderr syncWriter
	if code := runDaemon(context.Background(), &stderr); code != 2 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "error: no channel enabled; set [channels.telegram] enabled = true or enable [web]") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if got := tg.callCount(); got != 0 {
		t.Fatalf("disabled channel made %d Bot API calls", got)
	}
}

// TestDaemonTelegramTokenMissingWebKeepsRunning covers AC-DMN-006: the warning,
// the missing token status, and the daemon staying up with the web view.
func TestDaemonTelegramTokenMissingWebKeepsRunning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	tg := newTGServer(t, 111, "hi")
	writeDaemonTelegramConfig(t, home, tg.srv.URL, true, true, true)

	ctx, cancel := context.WithCancel(context.Background())
	var stderr syncWriter
	done := make(chan int, 1)
	go func() { done <- runDaemon(ctx, &stderr) }()
	getWebRoot(t, webAddressFromStderr(t, &stderr))
	waitStderr(t, &stderr, "telegram: TELEGRAM_BOT_TOKEN is not set; channel stays off until the daemon restarts with the token")

	select {
	case code := <-done:
		t.Fatalf("daemon exited with %d although the web view is on", code)
	case <-time.After(100 * time.Millisecond):
	}
	if got := tg.callCount(); got != 0 {
		t.Fatalf("missing token made %d Bot API calls", got)
	}
	status := telegramStatusFunc(telegram.Config{Enabled: true, Routes: map[int64]string{111: "helper"}}, false, nil)()
	if status.State != telegram.StateTokenMissing || status.BotUsername != "" || status.PollingSince != nil {
		t.Fatalf("token-missing status = %+v", status)
	}

	cancel()
	if code := waitDaemon(t, done); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}

// TestDaemonTelegramTokenMissingWebOff covers AC-DMN-007: without the web view
// a missing token is still an exit 2.
func TestDaemonTelegramTokenMissingWebOff(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	tg := newTGServer(t, 111, "hi")
	writeDaemonTelegramConfig(t, home, tg.srv.URL, false, true, true)

	var stderr syncWriter
	if code := runDaemon(context.Background(), &stderr); code != 2 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "error: telegram: TELEGRAM_BOT_TOKEN is not set") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if got := tg.callCount(); got != 0 {
		t.Fatalf("missing token made %d Bot API calls", got)
	}
}

// TestDaemonTelegramStatusFunc covers the snapshot the daemon hands to the web
// view: off, token_missing, and the connected field conversion.
func TestDaemonTelegramStatusFunc(t *testing.T) {
	off := telegramStatusFunc(telegram.Config{Enabled: false, Routes: map[int64]string{111: "helper"}, OwnerIDs: map[int64]bool{7: true}}, false, nil)()
	if off.State != telegram.StateOff || off.BotUsername != "" || off.PollingSince != nil {
		t.Fatalf("off status = %+v", off)
	}
	if off.ChatCount != 1 || off.OwnerCount != 1 {
		t.Fatalf("off counts = %d/%d, want 1/1", off.ChatCount, off.OwnerCount)
	}
	missing := telegramStatusFunc(telegram.Config{Enabled: true}, false, nil)()
	if missing.State != telegram.StateTokenMissing {
		t.Fatalf("missing state = %q, want %q", missing.State, telegram.StateTokenMissing)
	}

	since, last := int64(1000), int64(2000)
	connected := webTelegramStatus(telegram.Status{
		State:         telegram.StateConnected,
		BotUsername:   "lana_bot",
		ChatCount:     2,
		OwnerCount:    1,
		PollingSince:  &since,
		LastMessageAt: &last,
		PendingChats: []telegram.PendingChat{{
			ChatID: 333, Type: "private", Name: "Budi", UserID: 333,
			Username: "budi", FirstSeen: 1000, LastSeen: 2000, Messages: 3,
		}},
	})
	if connected.BotUsername != "lana_bot" || connected.PollingSince == nil || *connected.PollingSince != since {
		t.Fatalf("connected = %+v", connected)
	}
	if len(connected.PendingChats) != 1 || connected.PendingChats[0].Name != "Budi" || connected.PendingChats[0].Messages != 3 {
		t.Fatalf("pending_chats = %+v", connected.PendingChats)
	}
}

// TestQuestionRouterTelegramOnly covers AC-ASK-006: a telegram session goes to
// the Telegram responder and never to the web one.
func TestQuestionRouterTelegramOnly(t *testing.T) {
	sessions := map[string]core.SessionInfo{
		"telegram-session": {ID: "telegram-session", Channel: "telegram"},
		"web-session":      {ID: "web-session", Channel: "web"},
		"cli-session":      {ID: "cli-session", Channel: "cli"},
	}
	telegramCalls, webCalls := 0, 0
	router := &QuestionRouter{
		sessionInfo: func(_ context.Context, sessionID string) (core.SessionInfo, error) {
			info, ok := sessions[sessionID]
			if !ok {
				return core.SessionInfo{}, errors.New("session not found")
			}
			return info, nil
		},
		telegram: func(_ context.Context, req core.QuestionRequest) core.QuestionOutcome {
			telegramCalls++
			if req.SessionID != "telegram-session" {
				t.Errorf("telegram route got session %q", req.SessionID)
			}
			return core.QuestionOutcome{Status: core.QuestionStatusAnswered, Answers: []core.QuestionAnswer{{Question: "Where?", Selected: []string{"a"}}}}
		},
		web: func(context.Context, core.QuestionRequest) core.QuestionOutcome {
			webCalls++
			return core.QuestionOutcome{Status: core.QuestionStatusAnswered}
		},
	}

	got := router.Ask(context.Background(), core.QuestionRequest{SessionID: "telegram-session"})
	if got.Status != core.QuestionStatusAnswered {
		t.Fatalf("outcome = %+v, want answered", got)
	}
	if telegramCalls != 1 || webCalls != 0 {
		t.Fatalf("telegram calls = %d, web calls = %d, want 1 and 0", telegramCalls, webCalls)
	}
	if got := router.Ask(context.Background(), core.QuestionRequest{SessionID: "cli-session"}); got.Status != core.QuestionStatusUnavailable || got.Reason != core.QuestionReasonNoResponder {
		t.Fatalf("cli outcome = %+v, want unavailable no_responder", got)
	}
}

// TestQuestionRouterWebBroker covers the daemon wiring: a web-channel
// session's question reaches the web broker, which waits for an answer and
// closes on the context deadline.
func TestQuestionRouterWebBroker(t *testing.T) {
	server, err := web.New(web.Config{Enabled: false})
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	router := &QuestionRouter{
		sessionInfo: func(context.Context, string) (core.SessionInfo, error) {
			return core.SessionInfo{ID: "web-session", Channel: "web"}, nil
		},
		web: webQuestionResponder(server.Questions()),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	outcome := router.Ask(ctx, core.QuestionRequest{
		SessionID: "web-session",
		Questions: []core.Question{{Text: "Where?", Options: []core.QuestionOption{{Label: "here"}}}},
	})
	if outcome.Status != core.QuestionStatusUnavailable || outcome.Reason != core.QuestionReasonTimeout {
		t.Fatalf("outcome = %+v, want unavailable timeout from the web broker", outcome)
	}
}

func TestWebQuestionResponderError(t *testing.T) {
	router := &QuestionRouter{
		sessionInfo: func(context.Context, string) (core.SessionInfo, error) {
			return core.SessionInfo{ID: "web-session", Channel: "web"}, nil
		},
		web: webQuestionResponder(failingQuestionBroker{}),
	}
	got := router.Ask(context.Background(), core.QuestionRequest{SessionID: "web-session"})
	if got.Status != core.QuestionStatusUnavailable || got.Reason != core.QuestionReasonNoResponder {
		t.Fatalf("outcome = %+v, want unavailable no_responder", got)
	}
}

type failingQuestionBroker struct{}

func (failingQuestionBroker) Ask(context.Context, core.QuestionRequest) (core.QuestionOutcome, error) {
	return core.QuestionOutcome{}, errors.New("broker failed")
}

func TestDaemonSingleInstance(t *testing.T) {
	home := t.TempDir()
	unlock, err := lockDaemon(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockDaemon(home); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("daemon already running (pid %d)", os.Getpid())) {
		t.Fatalf("second lock err = %v", err)
	}
	unlock()
	again, err := lockDaemon(home)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	again()
}

func TestDaemonAnswersAndStops(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := os.ReadFile(filepath.Join("..", "..", "providers", "openai", "testdata", "text.sse"))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(data)
	}))
	defer llm.Close()
	tg := newTGServer(t, 111, "hi")
	writeDaemonConfig(t, home, llm.URL, tg.srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	var stderr syncWriter
	done := make(chan int, 1)
	go func() { done <- runDaemon(ctx, &stderr) }()
	tg.waitText(t, "Hello world!")
	if !strings.Contains(stderr.String(), "telegram: polling as @lana_bot") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit = %d, stderr=%q", code, stderr.String())
		}
	case <-time.After(shutdownGrace + time.Second):
		t.Fatal("daemon did not stop")
	}
}

func TestDaemonCancelsRunningTurn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	started := make(chan struct{}, 1)
	stop := make(chan struct{})
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-stop:
		}
	}))
	defer llm.Close()
	defer close(stop)
	tg := newTGServer(t, 111, "slow question")
	writeDaemonConfig(t, home, llm.URL, tg.srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	var stderr syncWriter
	done := make(chan int, 1)
	go func() { done <- runDaemon(ctx, &stderr) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatalf("turn did not start; stderr=%q", stderr.String())
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit = %d", code)
		}
	case <-time.After(shutdownGrace + time.Second):
		t.Fatal("daemon did not stop")
	}
	tg.waitText(t, "⊘ cancelled")
	if _, err := os.Stat(filepath.Join(home, "daemon.lock")); !os.IsNotExist(err) {
		t.Fatalf("lock file left: %v", err)
	}
}
