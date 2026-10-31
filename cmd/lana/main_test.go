package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMain(m *testing.M) {
	if os.Getenv("LANA_MCP_TEST_SERVER") == "1" {
		runMCPTestServer()
		os.Exit(0)
	}
	// Daemon tests start the web server with a fresh home; never open a real
	// browser tab from a test run. Tests that check the call replace it again.
	openBrowser = func(string) error { return nil }
	os.Exit(m.Run())
}

// runMCPTestServer is the subprocess side of the real stdio test in mcp_test.go.
func runMCPTestServer() {
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	sdk.AddTool(server, &sdk.Tool{
		Name:        "echo",
		Description: "Echo the given text.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"text": map[string]any{"type": "string"}},
			"required":   []string{"text"},
		},
	}, func(_ context.Context, _ *sdk.CallToolRequest, in echoInput) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: in.Text}}}, nil, nil
	})
	_ = server.Run(context.Background(), &sdk.StdioTransport{})
}

type echoInput struct {
	Text string `json:"text"`
}

func TestVersionExit(t *testing.T) {
	if code := run([]string{"--version"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if code := run([]string{"-V"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
}

func TestHelpExit(t *testing.T) {
	if code := run([]string{"--help"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
}

func TestUnknownCommand(t *testing.T) {
	if code := run([]string{"no-such-command"}); code != 2 {
		t.Fatalf("exit=%d", code)
	}
}

// TestRunWithoutAskReturnsNoResponder covers AC-ASK-032: without --ask the
// ask_user tool returns no_responder at once and the run does not wait.
func TestRunWithoutAskReturnsNoResponder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)

	var mu sync.Mutex
	var bodies []string
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		first := len(bodies) == 1
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		if first {
			args := `{"questions":[{"question":"Where should I save it?","options":[{"label":"research/"},{"label":"inbox/"}]}]}`
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"ask_user\",\"arguments\":%s}}]},\"finish_reason\":null}]}\n\n", strconv.Quote(args))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"))
			return
		}
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer llm.Close()

	cfg := fmt.Sprintf(`[providers.local]
kind = "openai_compat"
base_url = %q

[agents.helper]
provider = "local"
model = "m"
tools = ["ask_user"]

[[routes]]
channel = "library"
agent = "helper"
`, llm.URL)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	out, code := captureStdout(t, func() int { return cmdRun([]string{"--output", "json", "ask me"}) })
	if code != 0 {
		t.Fatalf("exit = %d, output = %q", code, out)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("run took %v, want no wait for a question", elapsed)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) < 2 {
		t.Fatalf("model requests = %d, want a follow-up with the tool output", len(bodies))
	}
	for _, body := range bodies[1:] {
		if strings.Contains(body, "no_responder") {
			return
		}
	}
	t.Fatalf("tool output in follow-up requests has no no_responder: %q", bodies[1:])
}
