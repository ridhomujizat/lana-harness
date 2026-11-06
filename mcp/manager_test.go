package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/tools"
)

func TestMain(m *testing.M) {
	if os.Getenv("LANA_MCP_TEST_SERVER") == "1" {
		runStdioServer()
		return
	}
	os.Exit(m.Run())
}

// runStdioServer is the subprocess side of the real stdio test.
func runStdioServer() {
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	addEchoTool(server)
	addEnvTool(server)
	_ = server.Run(context.Background(), &sdk.StdioTransport{})
	os.Exit(0)
}

type echoInput struct {
	Text string `json:"text"`
}

func objectSchema() map[string]any {
	return map[string]any{"type": "object"}
}

func addEchoTool(server *sdk.Server) {
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
}

func addEnvTool(server *sdk.Server) {
	sdk.AddTool(server, &sdk.Tool{
		Name:        "env",
		Description: "Report the server environment.",
		InputSchema: objectSchema(),
	}, func(_ context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
		wd, _ := os.Getwd()
		text := fmt.Sprintf("PROBE=%s\nPATH=%s\nPWD=%s", os.Getenv("PROBE"), os.Getenv("PATH"), wd)
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}, nil, nil
	})
}

func addBoomTool(server *sdk.Server) {
	sdk.AddTool(server, &sdk.Tool{
		Name:        "boom",
		Description: "Return a tool error.",
		InputSchema: objectSchema(),
	}, func(_ context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{
			Content: []sdk.Content{&sdk.TextContent{Text: "bad"}},
			IsError: true,
		}, nil, nil
	})
}

func addImageTool(server *sdk.Server) {
	sdk.AddTool(server, &sdk.Tool{
		Name:        "img",
		Description: "Return text and an image.",
		InputSchema: objectSchema(),
	}, func(_ context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{
			&sdk.TextContent{Text: "ok"},
			&sdk.ImageContent{Data: []byte("not-an-image"), MIMEType: "image/png"},
		}}, nil, nil
	})
}

func addSlowTool(server *sdk.Server) {
	sdk.AddTool(server, &sdk.Tool{
		Name:        "slow",
		Description: "Block until the call is cancelled.",
		InputSchema: objectSchema(),
	}, func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	})
}

func newInMemoryServer() *sdk.Server {
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	addEchoTool(server)
	addBoomTool(server)
	addImageTool(server)
	addSlowTool(server)
	return server
}

// newTestManager wires a manager whose connections use in-memory transports.
func newTestManager(t *testing.T, server *sdk.Server, cfg map[string]core.MCPConfig) (*Manager, core.ToolRegistry, *atomic.Int32) {
	t.Helper()
	registry := tools.NewRegistry()
	manager := NewManager(cfg, t.TempDir(), registry)
	var connects atomic.Int32
	manager.connect = func(ctx context.Context, _ string, _ core.MCPConfig) (sdk.Transport, error) {
		connects.Add(1)
		serverTransport, clientTransport := sdk.NewInMemoryTransports()
		if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
			return nil, err
		}
		return clientTransport, nil
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager, registry, &connects
}

func callTool(t *testing.T, registry core.ToolRegistry, name, input string) core.ToolOutput {
	t.Helper()
	tool, ok := registry.Get(name)
	if !ok {
		t.Fatalf("tool %q is not registered", name)
	}
	out, err := tool.Call(context.Background(), core.ToolCtx{}, json.RawMessage(input))
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return out
}

func TestEnsureRegistersTools(t *testing.T) {
	server := newInMemoryServer()
	manager, registry, _ := newTestManager(t, server, map[string]core.MCPConfig{"test": {}})

	got, unavailable := manager.Ensure(context.Background(), []string{"test"})
	if len(unavailable) != 0 {
		t.Fatalf("unavailable=%v", unavailable)
	}
	want := []string{"mcp__test__boom", "mcp__test__echo", "mcp__test__img", "mcp__test__slow"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tools=%v want %v", got, want)
	}
	for _, name := range want {
		if _, ok := registry.Get(name); !ok {
			t.Fatalf("tool %q is not registered", name)
		}
	}
}

func TestRisk(t *testing.T) {
	server := newInMemoryServer()
	manager, registry, _ := newTestManager(t, server, map[string]core.MCPConfig{"test": {}})
	if _, unavailable := manager.Ensure(context.Background(), []string{"test"}); len(unavailable) != 0 {
		t.Fatalf("unavailable=%v", unavailable)
	}

	echo, _ := registry.Get("mcp__test__echo")
	if got := echo.Risk(nil); got != core.RiskRead {
		t.Fatalf("echo risk=%q want read", got)
	}
	boom, _ := registry.Get("mcp__test__boom")
	if got := boom.Risk(nil); got != core.RiskExec {
		t.Fatalf("boom risk=%q want exec", got)
	}
}

func TestCallContent(t *testing.T) {
	server := newInMemoryServer()
	manager, registry, _ := newTestManager(t, server, map[string]core.MCPConfig{"test": {}})
	if _, unavailable := manager.Ensure(context.Background(), []string{"test"}); len(unavailable) != 0 {
		t.Fatalf("unavailable=%v", unavailable)
	}

	echo := callTool(t, registry, "mcp__test__echo", `{"text":"hello"}`)
	if echo.Status != core.ToolStatusSuccess || echo.Output != "hello" {
		t.Fatalf("echo=%+v", echo)
	}

	img := callTool(t, registry, "mcp__test__img", `{}`)
	if img.Status != core.ToolStatusSuccess || img.Output != "ok\n[image omitted]" {
		t.Fatalf("img=%+v", img)
	}

	boom := callTool(t, registry, "mcp__test__boom", `{}`)
	if boom.Status != core.ToolStatusError || boom.Output != "bad" {
		t.Fatalf("boom=%+v", boom)
	}
}

func TestCallTimeout(t *testing.T) {
	server := newInMemoryServer()
	manager, registry, _ := newTestManager(t, server, map[string]core.MCPConfig{"test": {}})
	manager.CallTimeout = 50 * time.Millisecond
	if _, unavailable := manager.Ensure(context.Background(), []string{"test"}); len(unavailable) != 0 {
		t.Fatalf("unavailable=%v", unavailable)
	}

	out := callTool(t, registry, "mcp__test__slow", `{}`)
	if out.Status != core.ToolStatusError || !strings.HasPrefix(out.Output, "timed out after") {
		t.Fatalf("slow=%+v", out)
	}
}

func TestUnavailableCommand(t *testing.T) {
	manager := NewManager(map[string]core.MCPConfig{
		"bad": {Command: "definitely-not-a-command-xyz"},
	}, t.TempDir(), tools.NewRegistry())
	defer func() { _ = manager.Close() }()

	_, unavailable := manager.Ensure(context.Background(), []string{"bad", "missing"})
	if unavailable["bad"] != "command not found" {
		t.Fatalf("bad reason=%q", unavailable["bad"])
	}
	if unavailable["missing"] != "not configured" {
		t.Fatalf("missing reason=%q", unavailable["missing"])
	}

	status := manager.Status([]string{"bad", "missing"})
	if status[0].State != stateUnavailable || status[0].Reason != "command not found" {
		t.Fatalf("bad status=%+v", status[0])
	}
	if status[1].State != stateUnavailable || status[1].Reason != "not configured" {
		t.Fatalf("missing status=%+v", status[1])
	}
}

func TestIdleReapAndReconnect(t *testing.T) {
	server := newInMemoryServer()
	manager, _, connects := newTestManager(t, server, map[string]core.MCPConfig{"test": {}})
	fake := time.Unix(1000, 0)
	manager.now = func() time.Time { return fake }

	if _, unavailable := manager.Ensure(context.Background(), []string{"test"}); len(unavailable) != 0 {
		t.Fatalf("unavailable=%v", unavailable)
	}
	if status := manager.Status([]string{"test"})[0]; status.State != stateRunning {
		t.Fatalf("state=%q", status.State)
	}

	fake = fake.Add(manager.IdleTimeout + time.Second)
	manager.ReapIdle()
	if status := manager.Status([]string{"test"})[0]; status.State != stateStopped {
		t.Fatalf("state=%q want stopped", status.State)
	}

	if _, unavailable := manager.Ensure(context.Background(), []string{"test"}); len(unavailable) != 0 {
		t.Fatalf("unavailable=%v", unavailable)
	}
	if status := manager.Status([]string{"test"})[0]; status.State != stateRunning {
		t.Fatalf("state=%q want running", status.State)
	}
	if got := connects.Load(); got != 2 {
		t.Fatalf("connects=%d want 2", got)
	}
}

func TestStatus(t *testing.T) {
	server := newInMemoryServer()
	manager, _, _ := newTestManager(t, server, map[string]core.MCPConfig{"test": {}})

	status := manager.Status([]string{"test"})
	if len(status) != 1 || status[0].State != stateStopped {
		t.Fatalf("initial status=%+v", status)
	}

	if _, unavailable := manager.Ensure(context.Background(), []string{"test"}); len(unavailable) != 0 {
		t.Fatalf("unavailable=%v", unavailable)
	}
	status = manager.Status([]string{"test", "missing"})
	if len(status) != 2 {
		t.Fatalf("status=%+v", status)
	}
	if status[0].Name != "test" || status[0].State != stateRunning || status[0].Tools != 4 {
		t.Fatalf("test status=%+v", status[0])
	}
	if status[1].Name != "missing" || status[1].State != stateUnavailable || status[1].Reason != "not configured" {
		t.Fatalf("missing status=%+v", status[1])
	}
}

func TestConcurrentEnsureConnectsOnce(t *testing.T) {
	server := newInMemoryServer()
	manager, _, connects := newTestManager(t, server, map[string]core.MCPConfig{"test": {}})

	const workers = 16
	var wg sync.WaitGroup
	failures := make(chan string, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			names, unavailable := manager.Ensure(context.Background(), []string{"test"})
			if len(unavailable) != 0 {
				failures <- fmt.Sprintf("unavailable=%v", unavailable)
				return
			}
			if len(names) != 4 {
				failures <- fmt.Sprintf("tools=%v", names)
			}
		}()
	}
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	if got := connects.Load(); got != 1 {
		t.Fatalf("connects=%d want 1", got)
	}
}

func TestStdioServer(t *testing.T) {
	t.Setenv("PROBE", "inherited")
	home := t.TempDir()
	manager := NewManager(map[string]core.MCPConfig{
		"test": {
			Command: os.Args[0],
			Args:    []string{"-test.run=^$"},
			Env: map[string]core.Secret{
				"LANA_MCP_TEST_SERVER": core.NewSecret("1"),
				"PROBE":                core.NewSecret("probe-value"),
			},
		},
	}, home, tools.NewRegistry())
	manager.ConnectTimeout = 20 * time.Second
	defer func() { _ = manager.Close() }()

	names, unavailable := manager.Ensure(context.Background(), []string{"test"})
	if len(unavailable) != 0 {
		t.Fatalf("unavailable=%v", unavailable)
	}
	for _, want := range []string{"mcp__test__echo", "mcp__test__env"} {
		found := false
		for _, name := range names {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("tool %q is missing from %v", want, names)
		}
	}

	if out := callTool(t, manager.registry, "mcp__test__echo", `{"text":"stdio-ok"}`); out.Status != core.ToolStatusSuccess || out.Output != "stdio-ok" {
		t.Fatalf("echo=%+v", out)
	}

	env := callTool(t, manager.registry, "mcp__test__env", `{}`)
	values := map[string]string{}
	for _, line := range strings.Split(env.Output, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = value
		}
	}
	if values["PROBE"] != "probe-value" {
		t.Fatalf("PROBE=%q", values["PROBE"])
	}
	if values["PATH"] == "" {
		t.Fatal("PATH is empty")
	}
	resolvedHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	resolvedPWD, err := filepath.EvalSymlinks(values["PWD"])
	if err != nil {
		t.Fatal(err)
	}
	if resolvedPWD != resolvedHome {
		t.Fatalf("cwd=%q want %q", resolvedPWD, resolvedHome)
	}
}

func TestCallCancelKeepsServerRunning(t *testing.T) {
	server := newInMemoryServer()
	manager, registry, _ := newTestManager(t, server, map[string]core.MCPConfig{"test": {}})
	if _, unavailable := manager.Ensure(context.Background(), []string{"test"}); len(unavailable) != 0 {
		t.Fatalf("unavailable=%v", unavailable)
	}
	tool, ok := registry.Get("mcp__test__slow")
	if !ok {
		t.Fatal("mcp__test__slow is not registered")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type callResult struct {
		out core.ToolOutput
		err error
	}
	done := make(chan callResult, 1)
	go func() {
		out, err := tool.Call(ctx, core.ToolCtx{}, json.RawMessage(`{}`))
		done <- callResult{out: out, err: err}
	}()
	cancel()

	got := <-done
	if got.err != nil {
		t.Fatalf("call error: %v", got.err)
	}
	if got.out.Status != core.ToolStatusError || got.out.Output != "cancelled" {
		t.Fatalf("output=%+v", got.out)
	}
	if status := manager.Status([]string{"test"})[0]; status.State != stateRunning {
		t.Fatalf("state=%q, want running", status.State)
	}
}

func TestRestartStopsAndStartsClient(t *testing.T) {
	server := newInMemoryServer()
	manager, _, connects := newTestManager(t, server, map[string]core.MCPConfig{"test": {}})

	if _, unavailable := manager.Ensure(context.Background(), []string{"test"}); len(unavailable) != 0 {
		t.Fatalf("unavailable=%v", unavailable)
	}
	status, found := manager.Restart(context.Background(), "test")
	if !found {
		t.Fatal("configured server was reported as missing")
	}
	if status.State != stateRunning || status.Tools != 4 {
		t.Fatalf("status=%+v", status)
	}
	if got := connects.Load(); got != 2 {
		t.Fatalf("connects=%d want 2", got)
	}

	// Each restart stops and starts the client again.
	if _, found := manager.Restart(context.Background(), "test"); !found {
		t.Fatal("second restart lost the server")
	}
	if got := connects.Load(); got != 3 {
		t.Fatalf("connects=%d want 3", got)
	}

	status, found = manager.Restart(context.Background(), "missing")
	if found {
		t.Fatalf("unknown server reported as configured: %+v", status)
	}
	if status.State != stateUnavailable || status.Reason != "not configured" {
		t.Fatalf("unknown status=%+v", status)
	}
}

func TestTestTemporaryClient(t *testing.T) {
	server := newInMemoryServer()
	manager, _, _ := newTestManager(t, server, map[string]core.MCPConfig{})

	count, err := manager.Test(context.Background(), core.MCPConfig{Command: "fake"})
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if count != 4 {
		t.Fatalf("tool count=%d want 4", count)
	}
	// The temporary client is not added to the manager.
	if status := manager.Status([]string{"test"})[0]; status.State != stateUnavailable || status.Reason != "not configured" {
		t.Fatalf("temporary client changed status: %+v", status)
	}

	manager.connect = func(context.Context, string, core.MCPConfig) (sdk.Transport, error) {
		return nil, fmt.Errorf("connect failed")
	}
	if _, err := manager.Test(context.Background(), core.MCPConfig{Command: "fake"}); err == nil || !strings.Contains(err.Error(), "connect failed") {
		t.Fatalf("Test error=%v", err)
	}
}
