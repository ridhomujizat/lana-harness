package lana

import (
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
)

type sseResponse struct {
	body  string
	delay time.Duration
	code  int
}

type sseServer struct {
	server   *httptest.Server
	mu       sync.Mutex
	queue    []sseResponse
	started  chan struct{}
	once     sync.Once
	requests atomic.Int32
}

func newSSEServer(t *testing.T, responses ...sseResponse) *sseServer {
	t.Helper()
	s := &sseServer{queue: responses, started: make(chan struct{})}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		s.once.Do(func() { close(s.started) })
		s.mu.Lock()
		if len(s.queue) == 0 {
			s.mu.Unlock()
			http.Error(w, "habis", http.StatusInternalServerError)
			return
		}
		resp := s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()
		if resp.delay > 0 {
			select {
			case <-time.After(resp.delay):
			case <-r.Context().Done():
				return
			}
		}
		if resp.code != 0 {
			w.WriteHeader(resp.code)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(resp.body))
	}))
	t.Cleanup(s.server.Close)
	return s
}

func textSSE(text string) string {
	return fmt.Sprintf(`data: {"choices":[{"delta":{"content":%q},"finish_reason":null}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`, text)
}

func toolSSE(name, args string) string {
	return fmt.Sprintf(`data: {"choices":[{"delta":{"content":"membaca"},"finish_reason":null}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":%q,"arguments":%q}}]},"finish_reason":null}]}

data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]

`, name, args)
}

func runtimeWithServer(t *testing.T, srv *sseServer, mutate func(string) string, options ...Option) *Runtime {
	t.Helper()
	cfg := fmt.Sprintf("[providers.local]\nkind = \"openai_compat\"\nbase_url = %q\n\n[agents.asisten]\nprovider = \"local\"\nmodel = \"m\"\n\n[[routes]]\nchannel = \"library\"\nagent = \"asisten\"\n", srv.server.URL)
	if mutate != nil {
		cfg = mutate(cfg)
	}
	rt, err := New(context.Background(), Options{Layers: []config.Layer{{Name: "test", TOML: []byte(cfg)}}}, options...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func TestRunResult(t *testing.T) {
	srv := newSSEServer(t, sseResponse{body: textSSE("halo")})
	rt := runtimeWithServer(t, srv, nil)
	result, err := rt.Run(context.Background(), Request{Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "halo" || result.StopReason != StopEndTurn || result.SessionID == "" {
		t.Fatalf("result=%+v", result)
	}
}

func TestRunBusy(t *testing.T) {
	srv := newSSEServer(t, sseResponse{body: textSSE("a"), delay: 250 * time.Millisecond}, sseResponse{body: textSSE("b")})
	rt := runtimeWithServer(t, srv, nil)
	info, err := rt.store.Create(context.Background(), core.NewSession{Agent: "asisten", Channel: "library", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := rt.Run(context.Background(), Request{Prompt: "first", SessionID: info.ID})
		done <- err
	}()
	select {
	case <-srv.started:
	case <-time.After(2 * time.Second):
		t.Fatal("request tidak mulai")
	}
	_, err = rt.Run(context.Background(), Request{Prompt: "second", SessionID: info.ID})
	if !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("err=%v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRunStopReasons(t *testing.T) {
	srv := newSSEServer(t, sseResponse{body: toolSSE("read_file", `{"path":"lana.go"}`)})
	rt := runtimeWithServer(t, srv, func(cfg string) string {
		return strings.Replace(cfg, "model = \"m\"", "model = \"m\"\nmax_steps = 1\ntools = [\"read_file\"]", 1)
	})
	result, err := rt.Run(context.Background(), Request{Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != StopMaxSteps {
		t.Fatalf("result=%+v", result)
	}
}

func TestRunProviderError(t *testing.T) {
	srv := newSSEServer(t, sseResponse{code: http.StatusUnauthorized})
	rt := runtimeWithServer(t, srv, nil)
	result, err := rt.Run(context.Background(), Request{Prompt: "hi"})
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Class != "auth" {
		t.Fatalf("err=%v", err)
	}
	if result.StopReason != StopError {
		t.Fatalf("result=%+v", result)
	}
}

func TestStreamEvents(t *testing.T) {
	srv := newSSEServer(t,
		sseResponse{body: toolSSE("read_file", `{"path":"a.txt"}`)},
		sseResponse{body: textSSE("selesai")},
	)
	rt := runtimeWithServer(t, srv, func(cfg string) string {
		return strings.Replace(cfg, "model = \"m\"", "model = \"m\"\ntools = [\"read_file\"]", 1)
	})
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("isi"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := rt.store.Create(context.Background(), core.NewSession{Agent: "asisten", Channel: "library", Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	var done *Done
	for event, err := range rt.Stream(context.Background(), Request{Prompt: "hi", SessionID: info.ID}) {
		if err != nil {
			t.Fatal(err)
		}
		switch e := event.(type) {
		case Session:
			kinds = append(kinds, "session")
		case TextDelta:
			kinds = append(kinds, "text")
		case ToolCall:
			kinds = append(kinds, "call")
		case ToolResult:
			kinds = append(kinds, "result")
		case Done:
			d := e
			done = &d
			kinds = append(kinds, "done")
		}
	}
	if done == nil || done.Result.StopReason != StopEndTurn {
		t.Fatalf("done=%+v", done)
	}
	want := []string{"session", "text", "call", "result", "text", "done"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("kinds=%v want %v", kinds, want)
	}
}

func TestStreamCancel(t *testing.T) {
	srv := newSSEServer(t, sseResponse{body: textSSE("lambat"), delay: 2 * time.Second})
	rt := runtimeWithServer(t, srv, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	var done *Done
	for event, err := range rt.Stream(ctx, Request{Prompt: "hi"}) {
		if err != nil {
			t.Fatal(err)
		}
		if d, ok := event.(Done); ok {
			done = &d
		}
	}
	if done == nil || done.Result.StopReason != StopCancelled {
		t.Fatalf("done=%+v", done)
	}
}

func TestCloseCancelsTurn(t *testing.T) {
	srv := newSSEServer(t, sseResponse{body: textSSE("lambat"), delay: 2 * time.Second})
	rt := runtimeWithServer(t, srv, nil)
	done := make(chan error, 1)
	go func() {
		_, err := rt.Run(context.Background(), Request{Prompt: "hi"})
		done <- err
	}()
	select {
	case <-srv.started:
	case <-time.After(2 * time.Second):
		t.Fatal("request tidak mulai")
	}
	start := time.Now()
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Close terlalu lama: %v", elapsed)
	}
	select {
	case err := <-done:
		_ = err
	case <-time.After(3 * time.Second):
		t.Fatal("Run tidak selesai setelah Close")
	}
	if _, err := rt.Run(context.Background(), Request{Prompt: "lagi"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("err=%v", err)
	}
}

type customTool struct {
	called atomic.Int32
}

func (c *customTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "lookup_order", Description: "cari order", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}
}
func (c *customTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }
func (c *customTool) Call(context.Context, core.ToolCtx, json.RawMessage) (core.ToolOutput, error) {
	c.called.Add(1)
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: "order-1"}, nil
}

func TestCustomToolPipeline(t *testing.T) {
	srv := newSSEServer(t,
		sseResponse{body: toolSSE("lookup_order", `{}`)},
		sseResponse{body: textSSE("selesai")},
	)
	tool := &customTool{}
	rt2, err := New(context.Background(), Options{
		Layers: []config.Layer{{Name: "test", TOML: []byte(fmt.Sprintf("[providers.local]\nkind = \"openai_compat\"\nbase_url = %q\n\n[agents.asisten]\nprovider = \"local\"\nmodel = \"m\"\ntools = [\"lookup_order\"]\n\n[[routes]]\nchannel = \"library\"\nagent = \"asisten\"\n", srv.server.URL))}},
		Tools:  []core.Tool{tool},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt2.Close() }()
	result, err := rt2.Run(context.Background(), Request{Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != StopEndTurn || tool.called.Load() != 1 {
		t.Fatalf("result=%+v called=%d", result, tool.called.Load())
	}
}

func TestApprovalNeverDefault(t *testing.T) {
	ws := t.TempDir()
	srv := newSSEServer(t,
		sseResponse{body: toolSSE("write_file", `{"path":"x.txt","content":"x"}`)},
		sseResponse{body: textSSE("selesai")},
	)
	rt := runtimeWithServer(t, srv, func(cfg string) string {
		return strings.Replace(cfg, "model = \"m\"", "model = \"m\"\nworkspace = \""+ws+"\"\ntools = [\"write_file\"]", 1)
	})
	result, err := rt.Run(context.Background(), Request{Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != StopEndTurn {
		t.Fatalf("result=%+v", result)
	}
	if _, err := os.Stat(filepath.Join(ws, "x.txt")); !os.IsNotExist(err) {
		t.Fatalf("file ditulis: %v", err)
	}
}

func TestApprovalTrust(t *testing.T) {
	ws := t.TempDir()
	srv := newSSEServer(t,
		sseResponse{body: toolSSE("write_file", `{"path":"x.txt","content":"x"}`)},
		sseResponse{body: textSSE("selesai")},
	)
	rt := runtimeWithServer(t, srv, func(cfg string) string {
		return strings.Replace(cfg, "model = \"m\"", "model = \"m\"\nworkspace = \""+ws+"\"\ntools = [\"write_file\"]", 1)
	})
	// runtimeWithServer tidak menerima opsi; buat ulang dengan PolicyTrust.
	_ = rt
	rt2, err := New(context.Background(), Options{
		Layers:   []config.Layer{{Name: "test", TOML: []byte(fmt.Sprintf("[providers.local]\nkind = \"openai_compat\"\nbase_url = %q\n\n[agents.asisten]\nprovider = \"local\"\nmodel = \"m\"\nworkspace = %q\ntools = [\"write_file\"]\n\n[[routes]]\nchannel = \"library\"\nagent = \"asisten\"\n", srv.server.URL, ws))}},
		Approval: PolicyTrust,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt2.Close() }()
	result, err := rt2.Run(context.Background(), Request{Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != StopEndTurn {
		t.Fatalf("result=%+v", result)
	}
	if _, err := os.Stat(filepath.Join(ws, "x.txt")); err != nil {
		for _, event := range eventsOf(t, rt2) {
			t.Logf("event %s %s", event.Kind, event.Payload)
		}
		t.Fatalf("file tidak ditulis: %v", err)
	}
}

func TestRouteLibrary(t *testing.T) {
	srv := newSSEServer(t, sseResponse{body: textSSE("ok")})
	rt := runtimeWithServer(t, srv, nil)
	if _, err := rt.Run(context.Background(), Request{Prompt: "hi"}); err != nil {
		t.Fatal(err)
	}
}
func eventsOf(t *testing.T, rt *Runtime) []core.SessionEvent {
	t.Helper()
	infos, err := rt.store.List(context.Background(), core.SessionFilter{})
	if err != nil || len(infos) == 0 {
		return nil
	}
	events, err := rt.store.ChainEvents(context.Background(), infos[0].ID)
	if err != nil {
		return nil
	}
	return events
}
func TestApproveWithLibraryChannel(t *testing.T) {
	ws := t.TempDir()
	srv := newSSEServer(t,
		sseResponse{body: toolSSE("write_file", `{"path":"x.txt","content":"x"}`)},
		sseResponse{body: textSSE("selesai")},
	)
	rt, err := New(context.Background(), Options{
		Layers: []config.Layer{{Name: "test", TOML: []byte(fmt.Sprintf("[providers.local]\nkind = \"openai_compat\"\nbase_url = %q\n\n[agents.asisten]\nprovider = \"local\"\nmodel = \"m\"\nworkspace = %q\ntools = [\"write_file\"]\npermission = \"ask\"\n\n[[routes]]\nchannel = \"library\"\nagent = \"asisten\"\n", srv.server.URL, ws))}},
	}, ApproveWith(func(context.Context, core.ApprovalRequest) core.ApprovalOutcome {
		return core.OutcomeAllowedOnce
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()
	result, err := rt.Run(context.Background(), Request{Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != StopEndTurn {
		t.Fatalf("result=%+v", result)
	}
	if _, err := os.Stat(filepath.Join(ws, "x.txt")); err != nil {
		t.Fatalf("file tidak ditulis: %v", err)
	}
}
