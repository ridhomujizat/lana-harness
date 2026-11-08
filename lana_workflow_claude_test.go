package lana

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

// guardianClaudeConfig is a config whose agent bima runs on
// claude-code/haiku with the memory tools, on the fake binary, with a
// DeepSeek-style provider at dsURL. extra is appended.
func guardianClaudeConfig(bin, ws, dsURL, extra string) string {
	return `
[providers.ds]
kind = "openai_compat"
base_url = "` + dsURL + `"

[router]
cooldown = "0s"
` + extra + `

[agents.bima]
model = "claude-code/haiku"
binary = "` + bin + `"
workspace = "` + ws + `"
permission = "trust"
tools = ["memory_read", "memory_write"]
`
}

// claudeStartCount is the number of fake claude process starts; 0 when the
// fake never ran.
func claudeStartCount(t *testing.T, logPath string) int {
	t.Helper()
	if _, err := os.Stat(logPath); err != nil {
		return 0
	}
	return len(fakeClaudeStarts(t, logPath))
}

// newGuardianWorkflow creates a one-node workflow guarded by bima.
func newGuardianWorkflow(t *testing.T, rt *Runtime) workflow.Detail {
	t.Helper()
	detail, err := rt.Workflows().Create(context.Background(), workflow.CreateInput{Name: "Inbox", Guardian: "bima"})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	return detail
}

// Spec 39 revision 5: the Workflow assistant of a workflow guarded by a
// Claude Code agent runs on the agent's background chain ([router]
// background_model): the reply comes from the background provider and
// Claude Code never starts.
func TestWorkflowAssistantClaudeGuardianUsesBackgroundModel(t *testing.T) {
	bin := buildFakeClaude(t)
	logPath := filepath.Join(t.TempDir(), "fake.jsonl")
	t.Setenv("FAKECLAUDE_LOG", logPath)
	ds := newSSEServer(t,
		sseResponse{body: textSSE(`{"reply":"I can plan that.","graph":null}`)},
		sseResponse{body: textSSE("[]")},
		sseResponse{body: textSSE("[]")},
		sseResponse{body: textSSE("[]")},
	)
	cfg := guardianClaudeConfig(bin, t.TempDir(), ds.server.URL, `background_model = "ds/deepseek-flash"`)
	st := openRouterTestStore(t)
	rt, err := New(context.Background(), Options{Home: t.TempDir(), Store: st, Layers: []config.Layer{{Name: "test", TOML: []byte(cfg)}}})
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = rt.Close()
		}
	}()
	wf := newGuardianWorkflow(t, rt)
	result, err := rt.Workflows().Assist(context.Background(), wf.ID, wf.Version, "add a step")
	if err != nil {
		t.Fatalf("assist: %v", err)
	}
	if result.Message.Role != "guardian" || result.Message.Text != "I can plan that." {
		t.Fatalf("assistant message = %+v", result.Message)
	}
	if n := claudeStartCount(t, logPath); n != 0 {
		t.Fatalf("claude code started %d times for a guardian structured call", n)
	}
	if ds.requests.Load() < 1 {
		t.Fatal("the background provider was not called")
	}
	closed = true
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	turns := routerCalls(t, st, "turn")
	if len(turns) == 0 {
		t.Fatal("no turn call records")
	}
	first := turns[0]
	if first.Provider != "ds" || first.Agent != "bima" || first.Requested != "ds/deepseek-flash" || first.Outcome != "ok" {
		t.Fatalf("guardian call record = %+v", first)
	}
	for _, c := range turns {
		if c.Provider == "claude-code" {
			t.Fatalf("a guardian call went to claude code: %+v", c)
		}
	}
}

// Spec 39 revision 5: without a background model the assistant answers the
// clear error, not "the guardian is not available", and nothing runs.
func TestWorkflowAssistantClaudeGuardianNeedsBackgroundModel(t *testing.T) {
	bin := buildFakeClaude(t)
	logPath := filepath.Join(t.TempDir(), "fake.jsonl")
	t.Setenv("FAKECLAUDE_LOG", logPath)
	ds := newSSEServer(t)
	rt, _ := routedRuntime(t, guardianClaudeConfig(bin, t.TempDir(), ds.server.URL, ""))
	wf := newGuardianWorkflow(t, rt)
	_, err := rt.Workflows().Assist(context.Background(), wf.ID, wf.Version, "add a step")
	var needs *workflow.GuardianBackgroundError
	if !errors.As(err, &needs) || errors.Is(err, workflow.ErrGuardianUnavailable) {
		t.Fatalf("assist err = %v", err)
	}
	const want = "The guardian bima runs on Claude Code. Set a background model in Router (or add an API model to its combo) so it can plan workflow changes."
	if err.Error() != want {
		t.Fatalf("message = %q", err.Error())
	}
	if n := claudeStartCount(t, logPath); n != 0 || ds.requests.Load() != 0 {
		t.Fatalf("a call ran: claude starts %d, provider requests %d", n, ds.requests.Load())
	}
	// A failure decision (diagnosis) is the same structured call.
	if _, err := rt.WorkflowDeps().RunAgent(context.Background(), workflow.AgentRequest{
		WorkflowID: wf.ID, NodeID: "n1", Agent: "bima", Prompt: "decide", Background: true,
	}); !errors.As(err, &needs) {
		t.Fatalf("diagnosis err = %v", err)
	}
}

// A guardian whose combo has an API model runs its structured calls on that
// model without [router] background_model.
func TestWorkflowGuardianComboUsesAPIModel(t *testing.T) {
	bin := buildFakeClaude(t)
	logPath := filepath.Join(t.TempDir(), "fake.jsonl")
	t.Setenv("FAKECLAUDE_LOG", logPath)
	ds := newSSEServer(t,
		sseResponse{body: textSSE(`{"reply":"combo plan","graph":null}`)},
		sseResponse{body: textSSE("[]")},
		sseResponse{body: textSSE("[]")},
	)
	cfg := guardianClaudeConfig(bin, t.TempDir(), ds.server.URL, "") + `
[combos.mix]
models = ["claude-code/haiku", "ds/deepseek-flash"]
`
	cfg = strings.Replace(cfg, `model = "claude-code/haiku"`+"\nbinary", `model = "mix"`+"\nbinary", 1)
	rt, _ := routedRuntime(t, cfg)
	result, err := rt.WorkflowDeps().RunAgent(context.Background(), workflow.AgentRequest{
		WorkflowID: "wf", NodeID: "assistant", Agent: "bima", Prompt: "plan", Background: true,
	})
	if err != nil || !strings.Contains(result.Text, "combo plan") {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if n := claudeStartCount(t, logPath); n != 0 {
		t.Fatalf("claude code started %d times", n)
	}
}

// REQ-CCT-008, API-CCT-027: a workflow agent node on a Claude Code agent runs
// in Claude Code when the built-in account's stored allow_scheduled is set,
// and is refused without it.
func TestWorkflowAgentNodeOnClaudeCodeAccount(t *testing.T) {
	bin := buildFakeClaude(t)
	fixtures, err := filepath.Abs(filepath.Join("runtime", "claudecode", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "fake.jsonl")
	t.Setenv("FAKECLAUDE_LOG", logPath)
	t.Setenv("FAKECLAUDE_FIXTURE", filepath.Join(fixtures, "hi.jsonl"))
	ds := newSSEServer(t)

	// Without allow_scheduled the node is refused before anything runs.
	refused, _ := routedRuntime(t, guardianClaudeConfig(bin, t.TempDir(), ds.server.URL, ""))
	const want = "claude-code is not allowed for scheduled runs of agent 'bima'"
	if _, err := refused.WorkflowDeps().RunAgent(context.Background(), workflow.AgentRequest{WorkflowID: "wf", NodeID: "n1", Agent: "bima", Prompt: "hi"}); err == nil || err.Error() != want {
		t.Fatalf("refused err = %v", err)
	}

	cfg := guardianClaudeConfig(bin, t.TempDir(), ds.server.URL, "") + `
[providers.claude-code]
kind = "claude-code"
allow_scheduled = true
`
	rt, _ := routedRuntime(t, cfg)
	result, err := rt.WorkflowDeps().RunAgent(context.Background(), workflow.AgentRequest{WorkflowID: "wf", NodeID: "n1", Agent: "bima", Prompt: "hi"})
	if err != nil {
		t.Fatalf("agent node: %v", err)
	}
	if result.Text != "Hello there, friend!" {
		t.Fatalf("result = %+v", result)
	}
	starts := fakeClaudeStarts(t, logPath)
	if len(starts) != 1 || argAfter(starts[0], "--model") != "haiku" {
		t.Fatalf("starts = %v", starts)
	}
	if ds.requests.Load() != 0 {
		t.Fatalf("the provider was called %d times", ds.requests.Load())
	}
}

// API-CCT-027: a disabled built-in account fails its Claude Code targets
// before output, and the chain falls back to the next target.
func TestBuiltinClaudeAccountDisabledFallsBack(t *testing.T) {
	bin := buildFakeClaude(t)
	logPath := filepath.Join(t.TempDir(), "fake.jsonl")
	t.Setenv("FAKECLAUDE_LOG", logPath)
	ds := newSSEServer(t,
		sseResponse{body: textSSE("fallback answer")},
		sseResponse{body: textSSE("[]")},
		sseResponse{body: textSSE("[]")},
	)
	cfg := guardianClaudeConfig(bin, t.TempDir(), ds.server.URL, "") + `
[providers.claude-code]
kind = "claude-code"
disabled = true

[combos.mix]
models = ["claude-code/haiku", "ds/deepseek-flash"]
`
	cfg = strings.Replace(cfg, `model = "claude-code/haiku"`+"\nbinary", `model = "mix"`+"\nbinary", 1)
	rt, _ := routedRuntime(t, cfg)
	if !rt.Router().Disabled("claude-code") {
		t.Fatal("the built-in account is not disabled")
	}
	result, err := rt.Run(context.Background(), Request{Agent: "bima", Prompt: "hi"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Text != "fallback answer" || result.Model != "ds/deepseek-flash" {
		t.Fatalf("result = %+v", result)
	}
	if n := claudeStartCount(t, logPath); n != 0 {
		t.Fatalf("claude code started %d times on a disabled account", n)
	}
}
