package lana

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
)

func newTestRuntime(t *testing.T, baseURL string, mutate func(string) string) *Runtime {
	t.Helper()
	cfg := fmt.Sprintf("[providers.local]\nkind = \"openai_compat\"\nbase_url = %q\n\n[agents.assistant]\nprovider = \"local\"\nmodel = \"m\"\n\n[[routes]]\nchannel = \"library\"\nagent = \"assistant\"\n", baseURL)
	if mutate != nil {
		cfg = mutate(cfg)
	}
	rt, err := New(context.Background(), Options{Layers: []config.Layer{{Name: "test", TOML: []byte(cfg)}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func TestNewMinimal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	rt := newTestRuntime(t, srv.URL, nil)
	if rt.coreCtx.LLM == nil || rt.coreCtx.Tools == nil || rt.coreCtx.Sessions == nil || rt.coreCtx.Agents == nil {
		t.Fatalf("service incomplete: %+v", rt.coreCtx)
	}
	found := false
	for _, row := range rt.result.Config.Modules {
		if row.ID == "store" {
			found = true
			if row.Module != "memstore" {
				t.Fatalf("store module=%q", row.Module)
			}
		}
	}
	if !found {
		t.Fatal("store row missing")
	}
}

type dummyTool struct{ name string }

func (d dummyTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: d.name, InputSchema: json.RawMessage(`{"type":"object"}`)}
}
func (d dummyTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }
func (d dummyTool) Call(context.Context, core.ToolCtx, json.RawMessage) (core.ToolOutput, error) {
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: "ok"}, nil
}

func TestNewToolConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	cfg := fmt.Sprintf("[providers.local]\nkind = \"openai_compat\"\nbase_url = %q\n\n[agents.assistant]\nprovider = \"local\"\nmodel = \"m\"\n", srv.URL)
	_, err := New(context.Background(), Options{
		Layers: []config.Layer{{Name: "test", TOML: []byte(cfg)}},
		Tools:  []core.Tool{dummyTool{name: "read_file"}},
	})
	if err == nil || !strings.Contains(err.Error(), "read_file") {
		t.Fatalf("err=%v", err)
	}
}

// REQ-TB-004, REQ-TB-008: an agent that lists a tool no package registered
// still starts the runtime. The missing tool is a warning, not a fatal error,
// and the turn simply does not offer it until something registers it.
func TestNewUnknownAgentToolWarns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	capture := &logCapture{}
	previous := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(previous) })

	cfg := fmt.Sprintf("[providers.local]\nkind = \"openai_compat\"\nbase_url = %q\n\n[agents.assistant]\nprovider = \"local\"\nmodel = \"m\"\ntools = [\"missing_tool\"]\n", srv.URL)
	rt, err := New(context.Background(), Options{Layers: []config.Layer{{Name: "test", TOML: []byte(cfg)}}})
	if err != nil {
		t.Fatalf("New with an unknown agent tool: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	if log := capture.String(); !strings.Contains(log, "missing_tool") || !strings.Contains(log, "assistant") {
		t.Fatalf("no warning for the unknown agent tool, log=%q", log)
	}
}

// logCapture is a slog.Handler that keeps every record in one buffer. It is
// safe for concurrent use, so it can back slog.Default during a test.
type logCapture struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (c *logCapture) Enabled(context.Context, slog.Level) bool { return true }

func (c *logCapture) Handle(_ context.Context, record slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf.WriteString(record.Level.String())
	c.buf.WriteString(" ")
	c.buf.WriteString(record.Message)
	record.Attrs(func(attr slog.Attr) bool {
		c.buf.WriteString(" ")
		c.buf.WriteString(attr.Key)
		c.buf.WriteString("=")
		c.buf.WriteString(attr.Value.String())
		return true
	})
	c.buf.WriteString("\n")
	return nil
}

func (c *logCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *logCapture) WithGroup(string) slog.Handler      { return c }

func (c *logCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func TestProfileRows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	rt := newTestRuntime(t, srv.URL, nil)
	modules := map[string]string{}
	for _, row := range rt.result.Config.Modules {
		modules[row.ID] = row.Module
	}
	want := map[string]string{"llm": "llm", "tools": "tools", "store": "memstore", "agent": "agent"}
	for id, module := range want {
		if modules[id] != module {
			t.Fatalf("modules=%v want %v", modules, want)
		}
	}
}

func TestRunEmptyPrompt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	rt := newTestRuntime(t, srv.URL, nil)
	if _, err := rt.Run(context.Background(), Request{Prompt: "  "}); !errors.Is(err, ErrEmptyPrompt) {
		t.Fatalf("err=%v", err)
	}
}

func TestNoRoute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	cfg := fmt.Sprintf("[providers.local]\nkind = \"openai_compat\"\nbase_url = %q\n\n[agents.assistant]\nprovider = \"local\"\nmodel = \"m\"\n", srv.URL)
	rt, err := New(context.Background(), Options{Layers: []config.Layer{{Name: "test", TOML: []byte(cfg)}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()
	if _, err := rt.Run(context.Background(), Request{Prompt: "hi"}); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("err=%v", err)
	}
}

func TestSessionChannel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"ok"},"finish_reason":null}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`))
	}))
	defer srv.Close()
	rt := newTestRuntime(t, srv.URL, nil)
	result, err := rt.Run(context.Background(), Request{Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := rt.store.Info(context.Background(), result.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if info.Channel != "library" {
		t.Fatalf("channel=%q", info.Channel)
	}
	// A legacy provider + model agent keeps the "provider:model" display.
	if result.Model != "local:m" {
		t.Fatalf("model=%q", result.Model)
	}
}
