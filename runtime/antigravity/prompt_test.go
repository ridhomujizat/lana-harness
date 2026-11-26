package antigravity

import (
	"errors"
	"slices"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// REQ-RPR-002: the argv of a prompt run: --model unless "default", --sandbox
// always, --effort unless "" or "off", never --dangerously-skip-permissions.
func TestBuildPromptArgs(t *testing.T) {
	got, err := buildPromptArgs(core.RuntimeConfig{Model: "gemini-4-neon", Effort: "high"})
	want := []string{"--input-format", "stream-json", "--output-format", "stream-json", "--disable-slash-commands", "--model", "gemini-4-neon", "--sandbox", "--effort", "high"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("args = %q %v", got, err)
	}
	got, _ = buildPromptArgs(core.RuntimeConfig{Model: "default", Effort: "off"})
	if !slices.Equal(got, []string{"--input-format", "stream-json", "--output-format", "stream-json", "--disable-slash-commands", "--sandbox"}) {
		t.Fatalf("default args = %q", got)
	}
	if _, err := buildPromptArgs(core.RuntimeConfig{Model: "--yolo"}); err == nil {
		t.Fatal("a flag-like model was accepted")
	}
}

// REQ-RPR-004: step_update text deltas stream, thinking and the user's step
// do not; the result gives the usage (input + cache read); a run without
// deltas answers the whole response once.
func TestPromptParser(t *testing.T) {
	var events []core.RuntimeEvent
	p := &promptParser{emit: func(ev core.RuntimeEvent) { events = append(events, ev) }}
	for _, line := range []string{
		`{"type":"init","model":"m"}`,
		`{"type":"step_update","source":"SOURCE_USER","text_delta":"echo"}`,
		`{"type":"step_update","thinking_delta":"hmm"}`,
		`{"type":"step_update","step":{"source":"SOURCE_MODEL","textDelta":"Hi"}}`,
		`{"type":"step_update","text_delta":" there"}`,
		`{"type":"result","status":"SUCCESS","response":"Hi there","usage":{"input_tokens":5,"output_tokens":2,"cache_read_tokens":3}}`,
	} {
		p.handle([]byte(line))
	}
	if p.res == nil || !p.res.ok() {
		t.Fatalf("result = %+v", p.res)
	}
	p.finish()
	if len(events) != 3 || events[0].Text != "Hi" || events[1].Text != " there" || events[2].Kind != core.RuntimeUsage {
		t.Fatalf("events = %+v", events)
	}
	if u := events[2].Usage; u.InputTokens != 8 || u.OutputTokens != 2 || u.CachedInputTokens != 3 {
		t.Fatalf("usage = %+v", u)
	}

	events = nil
	p = &promptParser{emit: func(ev core.RuntimeEvent) { events = append(events, ev) }}
	p.handle([]byte(`{"type":"result","status":"SUCCESS","response":"Whole answer"}`))
	p.finish()
	if len(events) != 2 || events[0].Kind != core.RuntimeAssistantMessage || events[0].Text != "Whole answer" {
		t.Fatalf("whole events = %+v", events)
	}
}

// REQ-RPR-005: agy's failures map to router classes, and a failure before any
// text is an early error.
func TestPromptFailures(t *testing.T) {
	for msg, want := range map[string]string{
		"Error: authentication required. Run 'agy' to log in.":           classAuth,
		"RESOURCE_EXHAUSTED: quota exceeded for Gemini Models":           classRateLimit,
		"invalid model selection (--model x --effort high): not offered": classUnknownModel,
		"something broke": classRuntime,
	} {
		if got, _ := classifyPromptFailure("agy-2", msg); got != want {
			t.Fatalf("%q = %s, want %s", msg, got, want)
		}
	}
	p := &promptParser{emit: func(core.RuntimeEvent) {}}
	p.handle([]byte(`AGY_ERROR: {"message":"quota exceeded"}`))
	p.handle([]byte(`{"type":"result","status":"ERROR","finished_reason":"error"}`))
	if p.res.ok() || p.errorText() != "quota exceeded" {
		t.Fatalf("error text = %q", p.errorText())
	}
	var early *EarlyError
	if err := p.failure(classRateLimit, "x"); !errors.As(err, &early) || early.Class != classRateLimit {
		t.Fatalf("failure = %v", err)
	}
	p.sawText = true
	if err := p.failure(classRateLimit, "x"); errors.As(err, &early) {
		t.Fatalf("a failure after text is early: %v", err)
	}
}
