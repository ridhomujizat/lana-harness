package lana

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

var (
	fakeClaudeOnce sync.Once
	fakeClaudeDir  string
	fakeClaudeBin  string
	fakeClaudeErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if fakeClaudeDir != "" {
		_ = os.RemoveAll(fakeClaudeDir)
	}
	os.Exit(code)
}

// buildFakeClaude builds the runtime/claudecode fake `claude` once per test
// binary.
func buildFakeClaude(t *testing.T) string {
	t.Helper()
	fakeClaudeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "lana-fakeclaude")
		if err != nil {
			fakeClaudeErr = err
			return
		}
		fakeClaudeDir = dir
		bin := filepath.Join(dir, "claude")
		out, err := exec.Command("go", "build", "-o", bin, "./runtime/claudecode/testdata/fakeclaude").CombinedOutput()
		if err != nil {
			fakeClaudeErr = fmt.Errorf("build fakeclaude: %v\n%s", err, out)
			return
		}
		fakeClaudeBin = bin
	})
	if fakeClaudeErr != nil {
		t.Fatal(fakeClaudeErr)
	}
	return fakeClaudeBin
}

// claudeAgentConfig is a config with one Claude Agent on the fake binary.
func claudeAgentConfig(bin, workspace, extra string) string {
	return fmt.Sprintf(`
[agents.claude]
toolset = "custom"
runtime = "claude-code"
binary = %q
workspace = %q
permission = "trust"
model = "haiku"
%s

[[routes]]
channel = "library"
agent = "claude"
`, bin, workspace, extra)
}

// fakeClaudeStarts returns the argv of every process start in the fake's log.
func fakeClaudeStarts(t *testing.T, logPath string) [][]string {
	t.Helper()
	f, err := os.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var starts [][]string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 32<<20)
	for sc.Scan() {
		var line struct {
			Kind string   `json:"kind"`
			Argv []string `json:"argv"`
		}
		if json.Unmarshal(sc.Bytes(), &line) == nil && line.Kind == "start" {
			starts = append(starts, line.Argv)
		}
	}
	return starts
}

func argAfter(argv []string, flag string) string {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

// AC-CCR-002, AC-CCR-004, AC-CCR-009 (data side): a Claude Code turn through
// the runtime and the fake `claude` writes the native events, stores the
// Claude session UUID, records one router call with provider claude-code
// (API-CCT-008, was claude-agent), updates the claude-code plan windows, and
// resumes the UUID after a restart.
func TestClaudeAgentTurnThroughRuntime(t *testing.T) {
	bin := buildFakeClaude(t)
	fixture, err := filepath.Abs(filepath.Join("runtime", "claudecode", "testdata", "bash.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "fake.jsonl")
	t.Setenv("FAKECLAUDE_FIXTURE", fixture)
	t.Setenv("FAKECLAUDE_LOG", logPath)
	cfg := claudeAgentConfig(bin, t.TempDir(), "")

	st := openRouterTestStore(t)
	rt, err := New(context.Background(), Options{Store: st, Layers: []config.Layer{{Name: "test", TOML: []byte(cfg)}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := rt.Run(context.Background(), Request{Prompt: "list files"})
	if err != nil {
		_ = rt.Close()
		t.Fatalf("run: %v", err)
	}
	if result.StopReason != StopEndTurn || !strings.Contains(result.Text, "notes.txt") || result.Model != "claude-code/claude-haiku-5-5" {
		_ = rt.Close()
		t.Fatalf("result = %+v", result)
	}
	sessionID := result.SessionID

	events, err := rt.History(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	claudeID := ""
	external := false
	for _, event := range events {
		kinds = append(kinds, event.Kind)
		switch event.Kind {
		case core.KindSystemMessage:
			var meta core.SystemMessage
			if err := json.Unmarshal(event.Payload, &meta); err != nil {
				t.Fatal(err)
			}
			if meta.Subtype == core.SystemSubtypeRuntime {
				claudeID = meta.ClaudeSessionID
			}
		case core.KindToolCall:
			var call core.ToolCallPayload
			if err := json.Unmarshal(event.Payload, &call); err != nil {
				t.Fatal(err)
			}
			external = call.External && call.Name == "Bash"
		}
	}
	want := []string{core.KindTurnStart, core.KindUserMessage, core.KindStepStart, core.KindAssistantMessage,
		core.KindToolCall, core.KindToolResult, core.KindStepEnd, core.KindTurnEnd}
	i := 0
	for _, kind := range kinds {
		if i < len(want) && kind == want[i] {
			i++
		}
	}
	if i != len(want) || !external {
		t.Fatalf("kinds = %v external=%v", kinds, external)
	}
	starts := fakeClaudeStarts(t, logPath)
	if len(starts) != 1 || argAfter(starts[0], "--session-id") == "" || argAfter(starts[0], "--session-id") != claudeID {
		t.Fatalf("starts = %v, stored id %q", starts, claudeID)
	}
	if argAfter(starts[0], "--model") != "haiku" {
		t.Fatalf("argv = %v", starts[0])
	}

	// AC-CCR-009 data side: the plan window from rate_limit_event.
	var windows int
	for _, snap := range rt.Router().Limits(context.Background(), false) {
		if snap.Provider == "claude-code" {
			if snap.Kind != "claude-code" || len(snap.Windows) == 0 || snap.Windows[0].Type != "five_hour" || snap.Windows[0].Utilization == nil || *snap.Windows[0].Utilization != 0.08 || snap.Windows[0].ResetsAt == nil {
				t.Fatalf("snapshot = %+v", snap)
			}
			windows = len(snap.Windows)
		}
	}
	if windows == 0 {
		t.Fatal("no claude-code limit snapshot")
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}

	calls := routerCalls(t, st, "turn")
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	call := calls[0]
	if call.Provider != "claude-code" || call.Model != "claude-haiku-5-5" || call.Requested != "claude-code/haiku" ||
		call.Outcome != "ok" || call.Agent != "claude" || call.SessionID != sessionID || call.Attempt != 1 ||
		call.InputTokens == 0 || call.CostUSD == nil || *call.CostUSD <= 0 {
		t.Fatalf("call = %+v", call)
	}

	// After a restart the next turn resumes the stored UUID.
	rt2, err := New(context.Background(), Options{Store: st, Layers: []config.Layer{{Name: "test", TOML: []byte(cfg)}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt2.Close() })
	if _, err := rt2.Run(context.Background(), Request{Prompt: "again", SessionID: sessionID}); err != nil {
		t.Fatalf("second run: %v", err)
	}
	starts = fakeClaudeStarts(t, logPath)
	if len(starts) != 2 || argAfter(starts[1], "--resume") != claudeID || argAfter(starts[1], "--session-id") != "" {
		t.Fatalf("starts = %v", starts)
	}
}

// REQ-CCT-008 (amends AC-CCR-008): a scheduled run of an agent whose chain
// has only Claude Code targets fails without allow_scheduled; a chain with
// another target runs it instead (TestClaudeCodeTargetIntegration).
func TestClaudeAgentScheduledRuns(t *testing.T) {
	bin := buildFakeClaude(t)
	cfg := claudeAgentConfig(bin, t.TempDir(), "") + `
[agents.cron]
toolset = "custom"
runtime = "claude-code"
binary = "` + bin + `"
allow_scheduled = true

[providers.ds]
kind = "openai_compat"
base_url = "http://127.0.0.1:1"

[agents.mixed]
toolset = "custom"
model = "claude-code/opus"
fallback = ["ds/deepseek-flash"]

[agents.plain]
toolset = "custom"
model = "ds/deepseek-flash"
`
	rt, _ := routedRuntime(t, cfg)
	const want = "claude-code is not allowed for scheduled runs of agent 'claude'"
	if _, err := (calendarActions{rt: rt}).AgentPrompt(context.Background(), "claude", "check mail", "Morning"); err == nil || err.Error() != want {
		t.Fatalf("calendar err = %v", err)
	}
	if _, err := (workflowDeps{rt: rt}).RunAgent(context.Background(), workflow.AgentRequest{WorkflowID: "wf", Agent: "claude", Prompt: "x"}); err == nil || err.Error() != want {
		t.Fatalf("workflow err = %v", err)
	}
	if err := rt.scheduledRunAllowed("cron", ""); err != nil {
		t.Fatalf("allow_scheduled agent refused: %v", err)
	}
	if err := rt.scheduledRunAllowed("mixed", ""); err != nil {
		t.Fatalf("agent with a fallback refused: %v", err)
	}
	if err := rt.scheduledRunAllowed("plain", "claude-code/haiku"); err == nil {
		t.Fatal("a workflow model override with only claude-code was allowed")
	}
}
