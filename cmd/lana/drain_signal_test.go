package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/core"
)

// setDrainTimeout shortens the drain wait for one test.
func setDrainTimeout(t *testing.T, timeout time.Duration) {
	t.Helper()
	old := daemonDrainTimeout
	daemonDrainTimeout = timeout
	t.Cleanup(func() { daemonDrainTimeout = old })
}

// gatedLLM is a fake provider that holds the answer until release or until the
// turn is canceled.
type gatedLLM struct {
	srv     *httptest.Server
	started chan struct{}

	mu       sync.Mutex
	released bool
	release  chan struct{}
}

func newGatedLLM(t *testing.T) *gatedLLM {
	t.Helper()
	sse, err := os.ReadFile(filepath.Join("..", "..", "providers", "openai", "testdata", "text.sse"))
	if err != nil {
		t.Fatal(err)
	}
	g := &gatedLLM{started: make(chan struct{}, 1), release: make(chan struct{})}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if skipModelList(w, r) {
			return
		}
		select {
		case g.started <- struct{}{}:
		default:
		}
		select {
		case <-g.release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(sse)
	}))
	t.Cleanup(func() {
		g.releaseNow()
		g.srv.CloseClientConnections()
		g.srv.Close()
	})
	return g
}

// releaseNow lets the pending answer through. It is safe to call twice.
func (g *gatedLLM) releaseNow() {
	g.mu.Lock()
	if !g.released {
		g.released = true
		close(g.release)
	}
	g.mu.Unlock()
}

func waitDaemonExit(t *testing.T, done <-chan int, limit time.Duration) {
	t.Helper()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("daemon exit = %d, want 0", code)
		}
	case <-time.After(limit):
		t.Fatal("daemon did not exit")
	}
}

func lastTurnEnd(t *testing.T, home string) core.TurnEnd {
	t.Helper()
	rt, err := lana.New(context.Background(), lana.Options{Home: home})
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	defer func() { _ = rt.Close() }()
	sessions, err := rt.ListAllSessions(context.Background(), core.SessionFilter{})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	for _, info := range sessions {
		events, err := rt.History(context.Background(), info.ID)
		if err != nil {
			continue
		}
		var end core.TurnEnd
		found := false
		for _, event := range events {
			if event.Kind != core.KindTurnEnd {
				continue
			}
			if err := json.Unmarshal(event.Payload, &end); err != nil {
				t.Fatal(err)
			}
			found = true
		}
		if found {
			return end
		}
	}
	t.Fatal("no turn/end in the store")
	return core.TurnEnd{}
}

// startSignalDaemon starts the daemon with a fake provider and Telegram API.
func startSignalDaemon(t *testing.T, llm *gatedLLM, message string) (chan os.Signal, chan int, *syncWriter, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	tg := newTGServer(t, 111, message)
	writeDaemonConfig(t, home, llm.srv.URL, tg.srv.URL)

	signals := make(chan os.Signal, 2)
	stderr := &syncWriter{}
	done := make(chan int, 1)
	go func() { done <- runDaemonSignals(context.Background(), stderr, signals) }()
	select {
	case <-llm.started:
	case <-time.After(5 * time.Second):
		t.Fatalf("turn did not start; stderr=%q", stderr.String())
	}
	return signals, done, stderr, home
}

// TestDaemonSignalWaitsForTurn covers AC-TFB-002: the first SIGTERM drains, a
// turn that finishes on its own ends end_turn, and the daemon exits after it.
func TestDaemonSignalWaitsForTurn(t *testing.T) {
	setDrainTimeout(t, 5*time.Second)
	llm := newGatedLLM(t)
	signals, done, _, home := startSignalDaemon(t, llm, "slow question")

	signals <- syscall.SIGTERM
	select {
	case code := <-done:
		t.Fatalf("daemon exited before the turn finished: %d", code)
	case <-time.After(100 * time.Millisecond):
	}
	llm.releaseNow()
	waitDaemonExit(t, done, shutdownGrace+time.Second)

	if end := lastTurnEnd(t, home); end.Reason != "end_turn" {
		t.Fatalf("turn/end = %+v, want end_turn", end)
	}
}

// TestDaemonSignalDeadlineInterrupts covers AC-TFB-003 and API-TFB-001: a turn
// that outlives the drain deadline ends interrupted with the stop message.
func TestDaemonSignalDeadlineInterrupts(t *testing.T) {
	setDrainTimeout(t, 100*time.Millisecond)
	llm := newGatedLLM(t)
	signals, done, _, home := startSignalDaemon(t, llm, "never finishes")

	signals <- syscall.SIGTERM
	waitDaemonExit(t, done, shutdownGrace+time.Second)

	end := lastTurnEnd(t, home)
	if end.Reason != "interrupted" {
		t.Fatalf("turn/end reason = %q, want interrupted", end.Reason)
	}
	if end.Error == nil || end.Error.Message != "lana stopped while this turn was running" {
		t.Fatalf("turn/end error = %+v", end.Error)
	}
}

// TestDaemonSecondSignalForces covers the second SIGTERM: it skips the wait and
// interrupts the running turn now.
func TestDaemonSecondSignalForces(t *testing.T) {
	setDrainTimeout(t, time.Minute)
	llm := newGatedLLM(t)
	signals, done, _, _ := startSignalDaemon(t, llm, "never finishes")

	signals <- syscall.SIGTERM
	time.Sleep(100 * time.Millisecond)
	select {
	case code := <-done:
		t.Fatalf("daemon exited after the first signal with a long drain: %d", code)
	default:
	}
	signals <- syscall.SIGTERM
	waitDaemonExit(t, done, shutdownGrace+time.Second)
}
