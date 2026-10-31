package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
	"github.com/ridhomujizat/lana-harness/tools"
)

func TestLooksLikeToolCallText(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "deepseek calls begin literal",
			text: "I need to run a command.\n<｜tool▁calls▁begin｜>bash<｜tool▁call▁end｜>",
			want: true,
		},
		{
			name: "deepseek call begin escaped",
			text: "let me look " + deepseekCallBegin + `{"name":"bash"}`,
			want: true,
		},
		{
			name: "dsml marker",
			text: "<｜DSML｜tool_calls>\n<｜DSML｜invoke name=\"bash\">",
			want: true,
		},
		{
			name: "xml tool call tag",
			text: "Sure:\n<tool_call>\n{\"name\": \"bash\", \"arguments\": {}}\n</tool_call>",
			want: true,
		},
		{
			name: "xml function call tag",
			text: "<function_call>{\"name\":\"bash\"}</function_call>",
			want: true,
		},
		{
			name: "fenced tool_call block",
			text: "Running now:\n```tool_call\n{\"name\":\"bash\",\"arguments\":{\"command\":\"ls\"}}\n```",
			want: true,
		},
		{
			name: "fenced tool block",
			text: "```tool\n{\"name\":\"bash\"}\n```",
			want: true,
		},
		{
			name: "bare json object",
			text: `{"name":"bash","arguments":{"command":"ls"}}`,
			want: true,
		},
		{
			name: "bare json object with parameters",
			text: "  {\"name\": \"bash\", \"parameters\": {\"command\": \"pwd\"}}  ",
			want: true,
		},
		{
			name: "json array of calls",
			text: `[{"name":"bash","arguments":{}},{"name":"read_file","arguments":{"path":"a"}}]`,
			want: true,
		},
		{
			name: "prose mentioning a tool",
			text: "I will use the bash tool to list the files.",
			want: false,
		},
		{
			name: "json code block that is not a call",
			text: "Here is the config:\n```json\n{\"foo\": \"bar\"}\n```",
			want: false,
		},
		{
			name: "bare json without arguments",
			text: `{"name":"bash"}`,
			want: false,
		},
		{
			name: "bare json without name",
			text: `{"arguments":{"command":"ls"}}`,
			want: false,
		},
		{
			name: "normal markdown",
			text: "# Result\n\n- one\n- two\n\nAll done.",
			want: false,
		},
		{
			name: "empty",
			text: "   \n ",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeToolCallText(tc.text); got != tc.want {
				t.Fatalf("looksLikeToolCallText(%q)=%v want %v", tc.text, got, tc.want)
			}
		})
	}
}

// A reply that writes a tool call as plain text and makes no tool call runs one
// more step whose user message is the runtime note (REQ-MEM-020, REQ-MEM-021).
func TestToolCallTextNudgeRunsOneStep(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		textResponse("<｜tool▁calls▁begin｜>bash<｜tool▁call▁end｜>"),
		textResponse("done now"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"bash"}}})
	result := h.run(t, "go")
	if result.StopReason != "end_turn" || result.Text != "done now" {
		t.Fatalf("result=%+v", result)
	}
	if steps := countSteps(t, h); steps != 2 {
		t.Fatalf("steps=%d want 2", steps)
	}
	msg := h.lastUserMessage(t)
	if !msg.Runtime {
		t.Fatalf("nudge user message Runtime=false: %+v", msg)
	}
	if got := userText(msg); got != toolCallTextNote {
		t.Fatalf("nudge user message=%q want %q", got, toolCallTextNote)
	}
	if got := countEvents(t, h, core.KindToolCall); got != 0 {
		t.Fatalf("tool calls=%d want 0", got)
	}
}

// The nudge runs at most twice per turn (REQ-MEM-020).
func TestToolCallTextNudgeAtMostTwice(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		textResponse("<tool_call>"),
		textResponse("<tool_call>"),
		textResponse("<tool_call>"),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"bash"}}})
	result := h.run(t, "go")
	if result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	if steps := countSteps(t, h); steps != 3 {
		t.Fatalf("steps=%d want 3", steps)
	}
	nudges := 0
	for _, event := range h.events(t) {
		if event.Kind != core.KindUserMessage {
			continue
		}
		var payload core.UserMessage
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Runtime {
			nudges++
		}
	}
	if nudges != maxToolCallTextNudges {
		t.Fatalf("nudges=%d want %d", nudges, maxToolCallTextNudges)
	}
}

// Prose that names a tool is not a malformed tool call and runs no nudge.
func TestToolCallTextNudgeSkipsProse(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{
		textResponse("I will use the bash tool to list the files."),
	}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"bash"}}})
	result := h.run(t, "go")
	if result.StopReason != "end_turn" {
		t.Fatalf("result=%+v", result)
	}
	if steps := countSteps(t, h); steps != 1 {
		t.Fatalf("steps=%d want 1", steps)
	}
}

// A plain answer that is not tool-call text has no runtime note.
func TestToolCallTextNudgeSkipsPlainAnswer(t *testing.T) {
	h := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("all done, nothing to run")}}, nil)
	h.run(t, "go")
	if msg := h.lastUserMessage(t); msg.Runtime {
		t.Fatalf("owner message marked runtime: %+v", msg)
	}
}

// The memory capture section is present only when the turn's tool set offers
// memory_write, and it stays byte-identical across two turns (REQ-MEM-001).
func TestPromptMemoryCaptureSection(t *testing.T) {
	with := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("a"), textResponse("b")}}, map[string]core.AgentConfig{
		"assistant": {Provider: "mock", Model: "m", Tools: []string{"memory_write"}},
	})
	if err := with.tools.Register(tools.NewMemoryWrite()); err != nil {
		t.Fatal(err)
	}
	rec := &requestRecorder{inner: with.llm.providers["mock"]}
	with.llm.providers["mock"] = rec
	with.run(t, "1")
	with.run(t, "2")

	prompts := with.systemPrompts(t)
	if len(prompts) != 1 {
		t.Fatalf("system messages=%d want 1", len(prompts))
	}
	if !strings.Contains(prompts[0], "# Memory capture") {
		t.Fatalf("prompt lacks the memory capture section:\n%s", prompts[0])
	}
	rec.mu.Lock()
	requests := append([]core.ChatRequest(nil), rec.requests...)
	rec.mu.Unlock()
	if len(requests) != 2 || requests[0].System != requests[1].System {
		t.Fatalf("System not byte-identical across turns: %+v", requests)
	}
	if requests[0].System != prompts[0] {
		t.Fatalf("system message differs from the request System")
	}

	without := newHarness(t, mock.Script{Responses: []mock.Response{textResponse("ok")}}, nil)
	without.run(t, "hi")
	if got := without.systemPrompts(t)[0]; strings.Contains(got, "# Memory capture") {
		t.Fatalf("prompt without memory_write must not carry the section:\n%s", got)
	}
}

// emptyAnswerNote and stepLimitNote are stored with Runtime true (REQ-MEM-021).
func TestRuntimeNoteFlags(t *testing.T) {
	t.Run("empty answer", func(t *testing.T) {
		h := newHarness(t, mock.Script{Responses: []mock.Response{
			toolResponse("c1", "read_file", `{"path":"a.txt"}`),
			textResponse(""),
			textResponse("here is the report"),
		}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}}})
		if err := os.WriteFile(filepath.Join(h.ws, "a.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if result := h.run(t, "hi"); result.StopReason != "end_turn" {
			t.Fatalf("result=%+v", result)
		}
		msg := h.lastUserMessage(t)
		if !msg.Runtime || userText(msg) != emptyAnswerNote {
			t.Fatalf("empty-answer note=%+v want Runtime and %q", msg, emptyAnswerNote)
		}
	})

	t.Run("step limit", func(t *testing.T) {
		h := newHarness(t, mock.Script{Responses: []mock.Response{
			toolResponse("c1", "read_file", `{"path":"a.txt"}`),
			toolResponse("c2", "read_file", `{"path":"a.txt"}`),
			textResponse("here is the report"),
		}}, map[string]core.AgentConfig{"assistant": {Provider: "mock", Model: "m", Tools: []string{"read_file"}, MaxSteps: 2}})
		if err := os.WriteFile(filepath.Join(h.ws, "a.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if result := h.run(t, "hi"); result.StopReason != "max_steps" {
			t.Fatalf("result=%+v", result)
		}
		msg := h.lastUserMessage(t)
		if !msg.Runtime || userText(msg) != stepLimitNote {
			t.Fatalf("step-limit note=%+v want Runtime and %q", msg, stepLimitNote)
		}
	})
}
