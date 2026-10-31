package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

func loopGuardTestCall(name, input string) core.ToolCall {
	return core.ToolCall{Name: name, Input: json.RawMessage(input)}
}

func loopGuardTestOutput(status core.ToolStatus) core.ToolOutput {
	return core.ToolOutput{Status: status}
}

// REQ-TRL-010: canonical arguments sort every object key, so calls that differ
// only in property order are the same call. Invalid JSON falls back to the raw
// string.
func TestCanonicalArgsSortsKeys(t *testing.T) {
	first := canonicalArgs(json.RawMessage(`{"b":1,"a":{"d":2,"c":[{"z":0,"y":1}]}}`))
	second := canonicalArgs(json.RawMessage(`{"a":{"c":[{"y":1,"z":0}],"d":2},"b":1}`))
	if first != second {
		t.Fatalf("canonical args differ:\n%s\n%s", first, second)
	}
	want := `{"a":{"c":[{"y":1,"z":0}],"d":2},"b":1}`
	if first != want {
		t.Fatalf("canonical args = %s, want %s", first, want)
	}
	if got := canonicalArgs(json.RawMessage(`not json`)); got != `not json` {
		t.Fatalf("invalid JSON = %q, want raw string", got)
	}
}

// REQ-TRL-010: exact repeats fire the gentle text at 3 and the detailed text
// (with the tool, the count, and the arguments) at 5 and 8.
func TestLoopGuardRepeatThresholds(t *testing.T) {
	var guard loopGuard
	call := loopGuardTestCall("read_file", `{"path":"a.txt"}`)
	success := []core.ToolOutput{loopGuardTestOutput(core.ToolStatusSuccess)}

	var got [8]string
	for i := range got {
		got[i] = guard.observe([]core.ToolCall{call}, success)
	}
	for i, text := range got {
		switch i {
		case 2:
			if text != loopGuardGentleReminder {
				t.Fatalf("call %d = %q, want the gentle reminder", i+1, text)
			}
		case 4:
			if !strings.Contains(text, "- consecutive_calls: 5") || !strings.Contains(text, "- tool: read_file") {
				t.Fatalf("call %d = %q, want the detailed reminder for 5", i+1, text)
			}
			if !strings.Contains(text, "- arguments: {\"path\":\"a.txt\"}") {
				t.Fatalf("call %d = %q, want the canonical arguments", i+1, text)
			}
		case 7:
			if !strings.Contains(text, "- consecutive_calls: 8") {
				t.Fatalf("call %d = %q, want the detailed reminder for 8", i+1, text)
			}
		default:
			if text != "" {
				t.Fatalf("call %d = %q, want no reminder", i+1, text)
			}
		}
	}
}

// REQ-TRL-010: a different tracked call starts a new chain of one.
func TestLoopGuardDifferentCallResetsRepeat(t *testing.T) {
	var guard loopGuard
	first := loopGuardTestCall("read_file", `{"path":"a.txt"}`)
	other := loopGuardTestCall("read_file", `{"path":"b.txt"}`)
	success := []core.ToolOutput{loopGuardTestOutput(core.ToolStatusSuccess)}

	guard.observe([]core.ToolCall{first}, success)
	guard.observe([]core.ToolCall{first}, success)
	guard.observe([]core.ToolCall{other}, success)
	guard.observe([]core.ToolCall{first}, success)
	guard.observe([]core.ToolCall{first}, success)
	got := guard.observe([]core.ToolCall{first}, success)
	if got != loopGuardGentleReminder {
		t.Fatalf("third call of the new chain = %q, want the gentle reminder", got)
	}
}

// REQ-TRL-011: consecutive error results fire at 4 and 8, and a success resets
// the streak.
func TestLoopGuardFailureThresholds(t *testing.T) {
	var guard loopGuard
	failure := []core.ToolOutput{loopGuardTestOutput(core.ToolStatusError)}
	success := []core.ToolOutput{loopGuardTestOutput(core.ToolStatusSuccess)}
	// Unique arguments keep the repeat chain at one call per step.
	unique := func(i int) core.ToolCall {
		return loopGuardTestCall("bash", fmt.Sprintf(`{"command":"cmd-%d"}`, i))
	}

	var steps [8]string
	for i := range steps {
		steps[i] = guard.observe([]core.ToolCall{unique(i)}, failure)
	}
	for i, text := range steps {
		switch i {
		case 3:
			if !strings.Contains(text, "The last 4 tool calls failed") {
				t.Fatalf("failure %d = %q, want the streak-4 reminder", i+1, text)
			}
		case 7:
			if !strings.Contains(text, "The last 8 tool calls failed") {
				t.Fatalf("failure %d = %q, want the streak-8 reminder", i+1, text)
			}
		default:
			if text != "" {
				t.Fatalf("failure %d = %q, want no reminder", i+1, text)
			}
		}
	}

	// Three errors, one success, then four errors: the success starts the
	// streak over, so the fourth error after it fires again.
	guard.reset()
	for i := 0; i < 3; i++ {
		if got := guard.observe([]core.ToolCall{unique(i)}, failure); got != "" {
			t.Fatalf("error %d = %q, want no reminder before the threshold", i+1, got)
		}
	}
	if got := guard.observe([]core.ToolCall{unique(3)}, success); got != "" {
		t.Fatalf("success = %q, want no reminder", got)
	}
	for i := 0; i < 3; i++ {
		if got := guard.observe([]core.ToolCall{unique(10 + i)}, failure); got != "" {
			t.Fatalf("error after success %d = %q, want no reminder", i+1, got)
		}
	}
	got := guard.observe([]core.ToolCall{unique(13)}, failure)
	if !strings.Contains(got, "The last 4 tool calls failed") {
		t.Fatalf("fourth error after success = %q, want the streak-4 reminder", got)
	}
}

// REQ-TRL-012: ask_user, job_output, and job_list neither count nor reset
// either chain.
func TestLoopGuardUntrackedToolsTransparent(t *testing.T) {
	var guard loopGuard
	tracked := loopGuardTestCall("read_file", `{"path":"a.txt"}`)
	failure := []core.ToolOutput{loopGuardTestOutput(core.ToolStatusError)}
	untracked := func(name string, status core.ToolStatus) string {
		return guard.observe([]core.ToolCall{loopGuardTestCall(name, `{}`)}, []core.ToolOutput{loopGuardTestOutput(status)})
	}

	if got := guard.observe([]core.ToolCall{tracked}, failure); got != "" {
		t.Fatalf("first error = %q", got)
	}
	if got := guard.observe([]core.ToolCall{tracked}, failure); got != "" {
		t.Fatalf("second error = %q", got)
	}
	if got := untracked("ask_user", core.ToolStatusError); got != "" {
		t.Fatalf("ask_user = %q, want no reminder", got)
	}
	if got := untracked("job_output", core.ToolStatusSuccess); got != "" {
		t.Fatalf("job_output = %q, want no reminder", got)
	}
	if got := untracked("job_list", core.ToolStatusError); got != "" {
		t.Fatalf("job_list = %q, want no reminder", got)
	}
	// The third tracked call still extends the repeat chain and the failure
	// streak: the untracked calls in between were transparent.
	if got := guard.observe([]core.ToolCall{tracked}, failure); got != loopGuardGentleReminder {
		t.Fatalf("third tracked call = %q, want the gentle reminder", got)
	}
	got := guard.observe([]core.ToolCall{tracked}, failure)
	if !strings.Contains(got, "The last 4 tool calls failed") {
		t.Fatalf("fourth tracked call = %q, want the streak-4 reminder", got)
	}
}

// REQ-TRL-010/011: several reminders fired in one step are one string joined
// by a blank line; a batch crossing a threshold sends one reminder for it.
func TestLoopGuardJoinsStepReminders(t *testing.T) {
	var guard loopGuard
	call := loopGuardTestCall("read_file", `{"path":"a.txt"}`)
	failure := []core.ToolOutput{loopGuardTestOutput(core.ToolStatusError)}

	calls := make([]core.ToolCall, 8)
	outputs := make([]core.ToolOutput, 8)
	for i := range calls {
		calls[i] = call
		outputs[i] = failure[0]
	}
	got := guard.observe(calls, outputs)
	if count := strings.Count(got, "\n\n"); count != 4 {
		t.Fatalf("batch reminders joined with %d separators:\n%s", count, got)
	}
	for _, want := range []string{
		loopGuardGentleReminder,
		"The last 4 tool calls failed",
		"- consecutive_calls: 5",
		"- consecutive_calls: 8",
		"The last 8 tool calls failed",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("batch reminders missing %q:\n%s", want, got)
		}
	}
	if count := strings.Count(got, "The last 4 tool calls failed"); count != 1 {
		t.Fatalf("streak-4 reminder fired %d times in one batch", count)
	}
	if len(guard.hits) != 5 {
		t.Fatalf("hits = %+v, want 5", guard.hits)
	}
}

// REQ-TRL-010: the detailed reminder caps the quoted arguments at 500
// characters and marks the omitted remainder.
func TestLoopGuardCapsArguments(t *testing.T) {
	var guard loopGuard
	long := strings.Repeat("a", 600)
	call := loopGuardTestCall("bash", `{"command":"`+long+`"}`)
	success := []core.ToolOutput{loopGuardTestOutput(core.ToolStatusSuccess)}

	var got string
	for i := 0; i < 5; i++ {
		got = guard.observe([]core.ToolCall{call}, success)
	}
	if !strings.Contains(got, "… (+") || !strings.Contains(got, "more chars)") {
		t.Fatalf("detailed reminder does not mark the omitted arguments:\n%s", got)
	}
	if strings.Contains(got, long) {
		t.Fatal("detailed reminder quoted the uncapped arguments")
	}
}
