package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// applyMCPManager records the hot-apply calls of ApplyConfig and serves Status
// for the ServerStatus tests.
type applyMCPManager struct {
	mu      sync.Mutex
	puts    map[string]core.MCPConfig
	putList []string
	removes []string
	ensures [][]string
	status  map[string]core.MCPServerStatus
}

func newApplyMCPManager() *applyMCPManager {
	return &applyMCPManager{
		puts:   map[string]core.MCPConfig{},
		status: map[string]core.MCPServerStatus{},
	}
}

func (m *applyMCPManager) Ensure(_ context.Context, servers []string) ([]string, map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensures = append(m.ensures, append([]string(nil), servers...))
	return nil, nil
}

func (m *applyMCPManager) Status(servers []string) []core.MCPServerStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]core.MCPServerStatus, 0, len(servers))
	for _, name := range servers {
		status, ok := m.status[name]
		if !ok {
			status = core.MCPServerStatus{Name: name, State: "stopped"}
		}
		out = append(out, status)
	}
	return out
}

func (m *applyMCPManager) TestServer(context.Context, core.MCPConfig) (int, error) { return 0, nil }

func (m *applyMCPManager) Put(name string, cfg core.MCPConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.puts[name] = cfg
	m.putList = append(m.putList, name)
}

func (m *applyMCPManager) Remove(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.puts, name)
	m.removes = append(m.removes, name)
}

func (m *applyMCPManager) calls() (puts []string, removes []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.putList...), append([]string(nil), m.removes...)
}

// plainStatusManager is a core.MCPManager without the hot-apply methods.
type plainStatusManager struct {
	status map[string]core.MCPServerStatus
}

func (m *plainStatusManager) Ensure(context.Context, []string) ([]string, map[string]string) {
	return nil, nil
}

func (m *plainStatusManager) Status(servers []string) []core.MCPServerStatus {
	out := make([]core.MCPServerStatus, 0, len(servers))
	for _, name := range servers {
		out = append(out, m.status[name])
	}
	return out
}

// TestApplyConfigMCPDiff covers the three diff outcomes: added and changed
// servers are put, a removed server is removed, an unchanged one is skipped.
func TestApplyConfigMCPDiff(t *testing.T) {
	h := newHarness(t, mock.Script{}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m"}})
	admin := newApplyMCPManager()
	h.reg.SetMCP(admin)

	def := func(command string) core.MCPConfig { return core.MCPConfig{Command: command} }
	h.reg.ApplyConfig(core.Config{
		Agents: map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m"}},
		MCP: map[string]core.MCPConfig{
			"keep":   def("keep"),
			"change": def("old"),
			"remove": def("remove"),
		},
	})
	if puts, removes := admin.calls(); len(puts) != 3 || len(removes) != 0 {
		t.Fatalf("seed apply puts=%v removes=%v", puts, removes)
	}

	admin.mu.Lock()
	admin.putList = nil
	admin.removes = nil
	admin.mu.Unlock()

	h.reg.ApplyConfig(core.Config{
		Agents: map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m"}},
		MCP: map[string]core.MCPConfig{
			"keep":   def("keep"),
			"change": def("new"),
			"add":    def("added"),
		},
	})

	puts, removes := admin.calls()
	if len(puts) != 2 || !containsString(puts, "add") || !containsString(puts, "change") || containsString(puts, "keep") {
		t.Fatalf("puts = %v, want add and change only", puts)
	}
	if len(removes) != 1 || removes[0] != "remove" {
		t.Fatalf("removes = %v, want remove", removes)
	}
	admin.mu.Lock()
	changed := admin.puts["change"]
	admin.mu.Unlock()
	if changed.Command != "new" {
		t.Fatalf("changed definition = %+v", changed)
	}

	// A second identical apply reaches the manager for nothing.
	h.reg.ApplyConfig(core.Config{
		Agents: map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m"}},
		MCP: map[string]core.MCPConfig{
			"keep":   def("keep"),
			"change": def("new"),
			"add":    def("added"),
		},
	})
	if puts, removes := admin.calls(); len(puts) != 2 || len(removes) != 1 {
		t.Fatalf("identical apply puts=%v removes=%v, want no call", puts, removes)
	}

	// A server mcp_manage already put is not applied twice by a later web save.
	admin.mu.Lock()
	admin.putList = nil
	admin.removes = nil
	admin.mu.Unlock()
	h.reg.PutServer("managed", def("managed"))
	h.reg.ApplyConfig(core.Config{
		Agents: map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m"}},
		MCP: map[string]core.MCPConfig{
			"keep":    def("keep"),
			"change":  def("new"),
			"add":     def("added"),
			"managed": def("managed"),
		},
	})
	if puts, removes := admin.calls(); len(puts) != 1 || puts[0] != "managed" || len(removes) != 0 {
		t.Fatalf("managed apply puts=%v removes=%v, want only the mcp_manage put", puts, removes)
	}
}

// TestApplyConfigDropsOverlay covers the config-wins rule: a web save clears
// the mcp_manage overlay, so the next turn uses the config list.
func TestApplyConfigDropsOverlay(t *testing.T) {
	text := "ok"
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{Text: &text}, {Stop: strptr("end_turn")}}},
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", MCP: []string{"old"}}})
	admin := newApplyMCPManager()
	h.reg.SetMCP(admin)
	h.reg.SetAgentServers("assistant", []string{"overlay"})

	h.reg.ApplyConfig(core.Config{
		Agents: map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", MCP: []string{"cfg"}}},
	})
	if _, ok := h.reg.agentServers("assistant"); ok {
		t.Fatal("overlay survived a config apply")
	}
	if result := h.run(t, "hi"); result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	admin.mu.Lock()
	ensures := append([][]string(nil), admin.ensures...)
	admin.mu.Unlock()
	if len(ensures) != 1 || len(ensures[0]) != 1 || ensures[0][0] != "cfg" {
		t.Fatalf("Ensure calls = %v, want [[cfg]]", ensures)
	}
}

// TestServerStatus covers the known/unknown split and the plain-manager path.
func TestServerStatus(t *testing.T) {
	h := newHarness(t, mock.Script{}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m"}})
	admin := newApplyMCPManager()
	admin.status["dbx"] = core.MCPServerStatus{Name: "dbx", State: "running", Tools: 25}
	h.reg.SetMCP(admin)
	h.reg.PutServer("dbx", core.MCPConfig{Command: "dbx"})

	state, tools, reason, known := h.reg.ServerStatus("dbx")
	if !known || state != "running" || tools != 25 || reason != "" {
		t.Fatalf("ServerStatus(dbx) = %q, %d, %q, %v", state, tools, reason, known)
	}
	if _, _, _, known := h.reg.ServerStatus("missing"); known {
		t.Fatal("unknown server reported as known")
	}

	// A plain manager has no admin methods, but status stays readable.
	plain := &plainStatusManager{status: map[string]core.MCPServerStatus{
		"dbx": {Name: "dbx", State: "stopped"},
	}}
	h.reg.SetMCP(plain)
	state, tools, _, known = h.reg.ServerStatus("dbx")
	if !known || state != "stopped" || tools != 0 {
		t.Fatalf("plain ServerStatus(dbx) = %q, %d, %v", state, tools, known)
	}
}

// blockingTool holds a turn open until release so ApplyConfig runs while the
// turn is in flight.
type blockingTool struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (t *blockingTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "block", Description: "Blocks.", InputSchema: json.RawMessage(`{"type":"object"}`)}
}

func (t *blockingTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }

func (t *blockingTool) Call(context.Context, core.ToolCtx, json.RawMessage) (core.ToolOutput, error) {
	t.once.Do(func() { close(t.started) })
	<-t.release
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: "ok"}, nil
}

// TestApplyConfigWhileTurnRuns is the -race guard: config reads of a running
// turn and ApplyConfig writes must not race.
func TestApplyConfigWhileTurnRuns(t *testing.T) {
	block := &blockingTool{started: make(chan struct{}), release: make(chan struct{})}
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		toolResponse("c1", "block", `{}`),
		textResponse("done"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"block"}}})
	if err := h.tools.Register(block); err != nil {
		t.Fatal(err)
	}

	turnID, err := h.reg.Start(context.Background(), h.info.ID, "hi")
	if err != nil {
		t.Fatal(err)
	}
	<-block.started

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			model := "m"
			if i%2 == 0 {
				model = "m2"
			}
			h.reg.ApplyConfig(core.Config{
				Agents: map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: model, Tools: []string{"block"}}},
			})
		}
	}()

	close(block.release)
	if _, err := h.reg.Wait(context.Background(), turnID); err != nil {
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()
}
