package lana

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

// fakeClaudeLog returns the fake's log lines of one kind.
func fakeClaudeLog(t *testing.T, logPath, kind string) []map[string]any {
	t.Helper()
	f, err := os.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 32<<20)
	for sc.Scan() {
		var line map[string]any
		if json.Unmarshal(sc.Bytes(), &line) == nil && line["kind"] == kind {
			out = append(out, line)
		}
	}
	return out
}

// Spec 39 end to end with the fake `claude` (T-CCT-01):
//   - AC-CCT-003, AC-CCT-001: a legacy runtime agent runs as claude-code/haiku
//     and the fake gets --model haiku;
//   - AC-CCT-006: --append-system-prompt holds the persona and memory;
//   - AC-CCT-007: Claude calls mcp__lana__memory_write over the loopback MCP
//     host; the memory has the fact and the log shows the lana tool call;
//   - AC-CCT-009: memory capture after the Claude turn calls the background
//     model with source memory_capture;
//   - AC-CCT-005: a combo whose Claude Code target is not logged in falls back
//     to DeepSeek in the same turn, with the attempt and both call records;
//   - AC-CCT-010: a workflow run of the combo agent without allow_scheduled is
//     answered by DeepSeek without starting Claude Code;
//   - the initialize response's models reach Router.Models("claude-code").
func TestClaudeCodeTargetIntegration(t *testing.T) {
	bin := buildFakeClaude(t)
	fixtures, err := filepath.Abs(filepath.Join("runtime", "claudecode", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "fake.jsonl")
	t.Setenv("FAKECLAUDE_LOG", logPath)
	t.Setenv("FAKECLAUDE_FIXTURE", filepath.Join(fixtures, "mcp_memory.jsonl"))
	ds := newSSEServer(t,
		sseResponse{body: textSSE("[]")},              // memory capture after the Claude turn
		sseResponse{body: textSSE("deepseek answer")}, // combo fallback
		sseResponse{body: textSSE("scheduled answer")},
	)
	home := t.TempDir()
	ws := t.TempDir()
	persona := filepath.Join(home, "agents", "bima", "AGENT.md")
	if err := os.MkdirAll(filepath.Dir(persona), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(persona, []byte("You are Bima"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "agents", "bima", "MEMORY.md"), []byte("The owner lives in Bandung.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := `
[providers.ds]
kind = "openai_compat"
base_url = "` + ds.server.URL + `"

[combos.c]
models = ["claude-code/opus", "ds/deepseek-flash"]

[router]
cooldown = "0s"
background_model = "ds/deepseek-flash"

[agents.bima]
runtime = "claude-code"
model = "haiku"
binary = "` + bin + `"
workspace = "` + ws + `"
permission = "trust"
tools = ["memory_write"]

[agents.combo]
model = "c"
binary = "` + bin + `"
workspace = "` + ws + `"
permission = "trust"

[[routes]]
channel = "library"
agent = "bima"
`
	st := openRouterTestStore(t)
	rt, err := New(context.Background(), Options{Home: home, Store: st, Layers: []config.Layer{{Name: "test", TOML: []byte(cfg)}}})
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = rt.Close()
		}
	}()

	// Part 1: legacy agent, persona, lana MCP tool, memory capture.
	result, err := rt.Run(context.Background(), Request{Agent: "bima", Prompt: "remember that I like green tea"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.StopReason != StopEndTurn || result.Text != "Saved." {
		t.Fatalf("result = %+v", result)
	}
	starts := fakeClaudeStarts(t, logPath)
	if len(starts) != 1 {
		t.Fatalf("starts = %v", starts)
	}
	argv := starts[0]
	if argAfter(argv, "--model") != "haiku" {
		t.Fatalf("model argv = %v", argv)
	}
	prompt := argAfter(argv, "--append-system-prompt")
	if !strings.Contains(prompt, "You are Bima") || !strings.Contains(prompt, "Bandung") || !strings.Contains(prompt, "mcp__lana__") {
		t.Fatalf("--append-system-prompt = %q", prompt)
	}
	if mcpCfg := argAfter(argv, "--mcp-config"); !strings.Contains(mcpCfg, `"lana"`) || !strings.Contains(mcpCfg, "Bearer ") {
		t.Fatalf("--mcp-config = %q", mcpCfg)
	}
	for _, a := range argv {
		if a == "--strict-mcp-config" {
			t.Fatal("--strict-mcp-config passed")
		}
	}
	lists := fakeClaudeLog(t, logPath, "mcp_list")
	if len(lists) != 1 || lists[0]["error"] != nil || !strings.Contains(strings.ReplaceAll(jsonString(lists[0]["tools"]), " ", ""), `"memory_write"`) ||
		strings.Contains(jsonString(lists[0]["tools"]), "read_file") {
		t.Fatalf("mcp_list = %+v", lists)
	}
	results := fakeClaudeLog(t, logPath, "mcp_result")
	if len(results) != 1 || results[0]["is_error"] != false {
		t.Fatalf("mcp_result = %+v", results)
	}
	memory, err := os.ReadFile(filepath.Join(home, "agents", "bima", "MEMORY.md"))
	if err != nil || !strings.Contains(string(memory), "green tea") {
		t.Fatalf("memory = %q, %v", memory, err)
	}
	calls := sessionPayloads[core.ToolCallPayload](t, rt, result.SessionID, core.KindToolCall)
	if len(calls) != 1 || calls[0].Name != "memory_write" || calls[0].External || calls[0].CallID != "toolu_mcp_1" {
		t.Fatalf("tool/call = %+v", calls)
	}
	toolResults := sessionPayloads[core.ToolResultPayload](t, rt, result.SessionID, core.KindToolResult)
	if len(toolResults) != 1 || toolResults[0].Status != core.ToolStatusSuccess {
		t.Fatalf("tool/result = %+v", toolResults)
	}
	waitFor(t, "memory capture", func() bool { return ds.requests.Load() >= 1 })

	// The models of the initialize response reach the router.
	models, err := rt.Router().Models(context.Background(), "claude-code", false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range models {
		found = found || (m.Address == "claude-code/claude-opus-5-5" && m.Name == "Claude Code · Opus 5.5")
	}
	if !found {
		t.Fatalf("models = %+v", models)
	}

	// Part 2: combo fallback on not logged in.
	t.Setenv("FAKECLAUDE_FIXTURE", filepath.Join(fixtures, "notloggedin.jsonl"))
	result, err = rt.Run(context.Background(), Request{Agent: "combo", Prompt: "hi"})
	if err != nil {
		t.Fatalf("combo run: %v", err)
	}
	if result.Text != "deepseek answer" || result.Model != "ds/deepseek-flash" {
		t.Fatalf("combo result = %+v", result)
	}
	attempts := sessionPayloads[core.AssistantAttempt](t, rt, result.SessionID, core.KindAssistantAttempt)
	if len(attempts) != 1 || attempts[0].Provider != "claude-code" || attempts[0].Model != "opus" || attempts[0].Error.Class != "auth" {
		t.Fatalf("attempts = %+v", attempts)
	}
	startsBefore := len(fakeClaudeStarts(t, logPath))

	// Part 3: a workflow run without allow_scheduled skips Claude Code.
	wf, err := (workflowDeps{rt: rt}).RunAgent(context.Background(), workflow.AgentRequest{WorkflowID: "wf", Agent: "combo", Prompt: "scheduled"})
	if err != nil {
		t.Fatalf("workflow: %v", err)
	}
	if !strings.Contains(wf.Text, "scheduled answer") || len(fakeClaudeStarts(t, logPath)) != startsBefore {
		t.Fatalf("workflow result = %+v", wf)
	}

	closed = true
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	captures := routerCalls(t, st, "memory_capture")
	if len(captures) != 1 || captures[0].Provider != "ds" || captures[0].Agent != "bima" || captures[0].Requested != "ds/deepseek-flash" {
		t.Fatalf("memory_capture calls = %+v", captures)
	}
	turns := routerCalls(t, st, "turn")
	var claudeFailed, claudeOK, dsOK bool
	for _, c := range turns {
		switch {
		case c.Provider == "claude-code" && c.Outcome == "auth" && c.Requested == "c":
			claudeFailed = true
		case c.Provider == "claude-code" && c.Outcome == "ok" && c.Requested == "claude-code/haiku":
			claudeOK = true
		case c.Provider == "ds" && c.Outcome == "ok":
			dsOK = true
		}
	}
	if !claudeFailed || !claudeOK || !dsOK {
		t.Fatalf("turn calls = %+v", turns)
	}
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
