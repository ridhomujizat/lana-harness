package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// tgServer is a minimal fake Bot API for daemon tests.
type tgServer struct {
	srv   *httptest.Server
	mu    sync.Mutex
	texts []string
	sent  bool
}

func newTGServer(t *testing.T, chatID int64, text string) *tgServer {
	s := &tgServer{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func writeDaemonConfig(t *testing.T, home, llmURL, tgURL string) {
	t.Helper()
	cfg := fmt.Sprintf(`[providers.local]
kind = "openai_compat"
base_url = %q

[agents.helper]
provider = "local"
model = "m"

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
	var stderr syncWriter
	if code := runDaemon(context.Background(), &stderr); code != 2 || !strings.Contains(stderr.String(), "no channel configured; add [channels.telegram] to config.toml") {
		t.Fatalf("no channel: code=%d stderr=%q", code, stderr.String())
	}
	writeDaemonConfig(t, home, "http://127.0.0.1:1", "http://127.0.0.1:1")
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	stderr = syncWriter{}
	if code := runDaemon(context.Background(), &stderr); code != 2 || !strings.Contains(stderr.String(), "telegram: TELEGRAM_BOT_TOKEN is not set") {
		t.Fatalf("no token: code=%d stderr=%q", code, stderr.String())
	}
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
