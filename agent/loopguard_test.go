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

// guardOutput builds a tool output with its pre-spill digest, as the pipeline
// does, so the digest-based detectors have something to compare.
func guardOutput(status core.ToolStatus, text string) core.ToolOutput {
	out := core.ToolOutput{Status: status, Output: text}
	out.Digest = outputDigest(out)
	return out
}

// TestLoopGuardEscalationACs covers one acceptance case of REQ-LPG-002..007
// per row (AC-LPG-001..011).
func TestLoopGuardEscalationACs(t *testing.T) {
	cases := []struct {
		id    string
		name  string
		check func(t *testing.T, g *loopGuard)
	}{
		{
			id:   "AC-LPG-001",
			name: "same-args failure reminder at two",
			check: func(t *testing.T, g *loopGuard) {
				call := loopGuardTestCall("bash", `{"command":"make"}`)
				failure := guardOutput(core.ToolStatusError, "exit code 1")
				if got := g.observe([]core.ToolCall{call}, []core.ToolOutput{failure}); got != "" {
					t.Fatalf("first failure = %q, want no reminder", got)
				}
				got := g.observe([]core.ToolCall{call}, []core.ToolOutput{failure})
				if want := loopGuardSameArgsReminder("bash", 2); got != want {
					t.Fatalf("second failure = %q, want %q", got, want)
				}
			},
		},
		{
			id:   "AC-LPG-002",
			name: "same-args block at five with other calls between",
			check: func(t *testing.T, g *loopGuard) {
				call := loopGuardTestCall("bash", `{"command":"make"}`)
				other := loopGuardTestCall("bash", `{"command":"echo hi"}`)
				failure := guardOutput(core.ToolStatusError, "exit code 1")
				success := guardOutput(core.ToolStatusSuccess, "hi")
				for i := 0; i < 4; i++ {
					g.observe([]core.ToolCall{call}, []core.ToolOutput{failure})
					g.observe([]core.ToolCall{other}, []core.ToolOutput{success})
				}
				out, blocked := g.before(call)
				if !blocked {
					t.Fatal("the fifth call of a failing signature was not blocked")
				}
				if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "Blocked bash") ||
					!strings.Contains(out.Output, "failed 4 times") {
					t.Fatalf("blocked output = %q", out.Output)
				}
				if out.Digest == "" || out.Digest != outputDigest(out) {
					t.Fatalf("blocked output digest = %q", out.Digest)
				}
				if g.halt == nil || g.halt.kind != loopGuardKindSameArgs {
					t.Fatalf("halt = %+v, want the same-args halt", g.halt)
				}
			},
		},
		{
			id:   "AC-LPG-003",
			name: "progress write resets every signature count",
			check: func(t *testing.T, g *loopGuard) {
				call := loopGuardTestCall("bash", `{"command":"make"}`)
				failure := guardOutput(core.ToolStatusError, "exit code 1")
				edit := loopGuardTestCall("edit_file", `{"path":"a.txt","old_string":"a","new_string":"b"}`)
				editOK := guardOutput(core.ToolStatusSuccess, "Edited a.txt: replaced 1 occurrence(s).")
				for i := 0; i < 3; i++ {
					g.observe([]core.ToolCall{call}, []core.ToolOutput{failure})
				}
				g.observe([]core.ToolCall{edit}, []core.ToolOutput{editOK})
				g.observe([]core.ToolCall{call}, []core.ToolOutput{failure})
				g.observe([]core.ToolCall{call}, []core.ToolOutput{failure})
				if _, blocked := g.before(call); blocked {
					t.Fatal("blocked after a progress write reset the failure count")
				}
			},
		},
		{
			id:   "AC-LPG-004",
			name: "read-only no-progress block at five",
			check: func(t *testing.T, g *loopGuard) {
				call := loopGuardTestCall("read_file", `{"path":"a.txt"}`)
				same := guardOutput(core.ToolStatusSuccess, "unchanged")
				for i := 0; i < 4; i++ {
					g.observe([]core.ToolCall{call}, []core.ToolOutput{same})
				}
				out, blocked := g.before(call)
				if !blocked {
					t.Fatal("the fifth identical read was not blocked")
				}
				if !strings.Contains(out.Output, "Blocked read_file") || !strings.Contains(out.Output, "4 times") {
					t.Fatalf("blocked output = %q", out.Output)
				}
				if g.halt == nil || g.halt.kind != loopGuardKindReadOnly {
					t.Fatalf("halt = %+v, want the read-only halt", g.halt)
				}
			},
		},
		{
			id:   "AC-LPG-005",
			name: "progress write restarts the read-only run",
			check: func(t *testing.T, g *loopGuard) {
				call := loopGuardTestCall("read_file", `{"path":"a.txt"}`)
				same := guardOutput(core.ToolStatusSuccess, "unchanged")
				edit := loopGuardTestCall("edit_file", `{"path":"a.txt","old_string":"a","new_string":"b"}`)
				editOK := guardOutput(core.ToolStatusSuccess, "Edited a.txt: replaced 1 occurrence(s).")
				for i := 0; i < 4; i++ {
					g.observe([]core.ToolCall{call}, []core.ToolOutput{same})
				}
				g.observe([]core.ToolCall{edit}, []core.ToolOutput{editOK})
				if _, blocked := g.before(call); blocked {
					t.Fatal("blocked after a progress write restarted the read-only run")
				}
				g.observe([]core.ToolCall{call}, []core.ToolOutput{same})
				if _, blocked := g.before(call); blocked {
					t.Fatal("blocked at a run of two")
				}
			},
		},
		{
			id:   "AC-LPG-006",
			name: "identical repeat chain blocks the ninth call",
			check: func(t *testing.T, g *loopGuard) {
				call := loopGuardTestCall("bash", `{"command":"true"}`)
				same := guardOutput(core.ToolStatusSuccess, "")
				for i := 0; i < loopGuardStreakLen; i++ {
					g.observe([]core.ToolCall{call}, []core.ToolOutput{same})
				}
				out, blocked := g.before(call)
				if !blocked {
					t.Fatal("the ninth identical call was not blocked")
				}
				if !strings.Contains(out.Output, "Blocked bash") || !strings.Contains(out.Output, "8 times in a row") {
					t.Fatalf("blocked output = %q", out.Output)
				}
				if g.halt == nil || g.halt.kind != loopGuardKindIdenticalStreak {
					t.Fatalf("halt = %+v, want the identical-streak halt", g.halt)
				}
			},
		},
		{
			id:   "AC-LPG-007",
			name: "a chain whose results differ is not blocked",
			check: func(t *testing.T, g *loopGuard) {
				call := loopGuardTestCall("bash", `{"command":"date"}`)
				for i := 0; i < loopGuardStreakLen; i++ {
					out := guardOutput(core.ToolStatusSuccess, fmt.Sprintf("out-%d", i))
					g.observe([]core.ToolCall{call}, []core.ToolOutput{out})
				}
				if _, blocked := g.before(call); blocked {
					t.Fatal("blocked a repeat chain whose results differ")
				}
				g.observe([]core.ToolCall{call}, []core.ToolOutput{guardOutput(core.ToolStatusSuccess, "out-9")})
				if _, blocked := g.before(call); blocked {
					t.Fatal("blocked the ninth call of a chain whose results differ")
				}
			},
		},
		{
			id:   "AC-LPG-008",
			name: "cycle reminds at three laps and halts at five",
			check: func(t *testing.T, g *loopGuard) {
				a := loopGuardTestCall("bash", `{"command":"step-a"}`)
				b := loopGuardTestCall("bash", `{"command":"step-b"}`)
				outA := guardOutput(core.ToolStatusSuccess, "A")
				outB := guardOutput(core.ToolStatusSuccess, "B")
				last := ""
				for i := 0; i < 3; i++ {
					g.observe([]core.ToolCall{a}, []core.ToolOutput{outA})
					last = g.observe([]core.ToolCall{b}, []core.ToolOutput{outB})
				}
				if !strings.Contains(last, "same cycle of 2 tool calls") {
					t.Fatalf("A-B-A-B-A-B = %q, want the cycle reminder", last)
				}
				g.reset()
				for i := 0; i < 5; i++ {
					g.observe([]core.ToolCall{a}, []core.ToolOutput{outA})
					g.observe([]core.ToolCall{b}, []core.ToolOutput{outB})
				}
				if g.halt == nil || g.halt.kind != loopGuardKindCycle {
					t.Fatalf("A-B x5 halt = %+v, want the cycle halt", g.halt)
				}
			},
		},
		{
			id:   "AC-LPG-009",
			name: "a cycle whose results change never fires",
			check: func(t *testing.T, g *loopGuard) {
				a := loopGuardTestCall("bash", `{"command":"step-a"}`)
				b := loopGuardTestCall("bash", `{"command":"step-b"}`)
				outA := guardOutput(core.ToolStatusSuccess, "A")
				var got strings.Builder
				for i := 0; i < 5; i++ {
					got.WriteString(g.observe([]core.ToolCall{a}, []core.ToolOutput{outA}))
					got.WriteString(g.observe([]core.ToolCall{b}, []core.ToolOutput{guardOutput(core.ToolStatusSuccess, fmt.Sprintf("B-%d", i))}))
				}
				if strings.Contains(got.String(), "same cycle") {
					t.Fatalf("reminders = %q, want no cycle reminder", got.String())
				}
				if g.halt != nil {
					t.Fatalf("halt = %+v, want no halt", g.halt)
				}
			},
		},
		{
			id:   "AC-LPG-010",
			name: "twelve consecutive errors halt",
			check: func(t *testing.T, g *loopGuard) {
				for i := 0; i < loopGuardFailureHalt; i++ {
					call := loopGuardTestCall("bash", fmt.Sprintf(`{"command":"fail-%d"}`, i))
					g.observe([]core.ToolCall{call}, []core.ToolOutput{guardOutput(core.ToolStatusError, "boom")})
				}
				if g.halt == nil || g.halt.kind != loopGuardKindFailureStreak {
					t.Fatalf("halt = %+v, want the failure-streak halt", g.halt)
				}
			},
		},
		{
			id:   "AC-LPG-011",
			name: "three unknown-name steps halt and a valid step resets",
			check: func(t *testing.T, g *loopGuard) {
				for i := 0; i < 3; i++ {
					g.strike(true)
				}
				if g.halt == nil || g.halt.kind != loopGuardKindStrikes {
					t.Fatalf("halt = %+v, want the strikes halt", g.halt)
				}
				g.reset()
				g.strike(true)
				g.strike(true)
				g.strike(false)
				g.strike(true)
				g.strike(true)
				if g.halt != nil {
					t.Fatalf("halt = %+v, want no halt after a valid step", g.halt)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.id+" "+tc.name, func(t *testing.T) {
			var g loopGuard
			tc.check(t, &g)
		})
	}
}

// REQ-LPG-002: the new detectors only count results that carry a digest, that
// is, results produced by the tool pipeline; a read-only failure with a digest
// is counted like any other. This keeps synthetic, digest-less outputs (the
// older chain tests) transparent to the escalation detectors.
func TestLoopGuardSameArgsNeedsDigest(t *testing.T) {
	call := loopGuardTestCall("read_file", `{"path":"a.txt"}`)

	var withoutDigest loopGuard
	noDigest := loopGuardTestOutput(core.ToolStatusError)
	for i := 0; i < 4; i++ {
		withoutDigest.observe([]core.ToolCall{call}, []core.ToolOutput{noDigest})
	}
	if _, blocked := withoutDigest.before(call); blocked {
		t.Fatal("a digest-less error result was counted by the same-arguments detector")
	}

	var withDigest loopGuard
	digest := guardOutput(core.ToolStatusError, "file not found")
	for i := 0; i < 4; i++ {
		withDigest.observe([]core.ToolCall{call}, []core.ToolOutput{digest})
	}
	if _, blocked := withDigest.before(call); !blocked {
		t.Fatal("a read-only failure with a digest was not counted by the same-arguments detector")
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
