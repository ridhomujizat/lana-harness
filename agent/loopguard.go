package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
)

// loopGuard detects repeated tool calls and consecutive tool failures inside
// one turn (REQ-TRL-010..014). The zero value is ready to use; reset clears
// both chains.
type loopGuard struct {
	// lastKey is the identity of the previous tracked call: its name and
	// canonical arguments, separated by a NUL byte.
	lastKey string
	// repeat is the length of the current exact-repeat chain.
	repeat int
	// failures is the length of the current error-result streak.
	failures int
	// hits records the reminders this step fired, for the turn's log line.
	hits []loopGuardHit
}

// loopGuardHit is one fired reminder: its chain kind ("repeat" or "failure")
// and the chain length that crossed the threshold.
type loopGuardHit struct {
	kind  string
	count int
}

// reset clears both chains and the pending log hits.
func (g *loopGuard) reset() {
	g.lastKey = ""
	g.repeat = 0
	g.failures = 0
	g.hits = nil
}

// loopGuardUntracked lists the tools that are transparent to both chains:
// they neither count nor reset either of them (REQ-TRL-012).
var loopGuardUntracked = map[string]bool{
	"ask_user":   true,
	"job_output": true,
	"job_list":   true,
}

// observe processes one step's calls and outputs in call order and returns the
// reminder text for the next request, or "". Several reminders fired in one
// step are joined by a blank line.
func (g *loopGuard) observe(calls []core.ToolCall, outputs []core.ToolOutput) string {
	g.hits = nil
	var reminders []string
	for i, call := range calls {
		if loopGuardUntracked[call.Name] {
			continue
		}
		canonical := canonicalArgs(call.Input)
		key := call.Name + "\x00" + canonical
		if key == g.lastKey {
			g.repeat++
		} else {
			g.lastKey = key
			g.repeat = 1
		}
		switch g.repeat {
		case 3:
			g.addHit("repeat", g.repeat)
			reminders = append(reminders, loopGuardGentleReminder)
		case 5, 8:
			g.addHit("repeat", g.repeat)
			reminders = append(reminders, loopGuardDetailedReminder(call.Name, g.repeat, canonical))
		}
		if i >= len(outputs) {
			continue
		}
		switch outputs[i].Status {
		case core.ToolStatusError:
			g.failures++
			if g.failures == 4 || g.failures == 8 {
				g.addHit("failure", g.failures)
				reminders = append(reminders, loopGuardFailureReminder(g.failures))
			}
		case core.ToolStatusSuccess:
			// A success ends the streak; other statuses leave it untouched
			// (REQ-TRL-011).
			g.failures = 0
		}
	}
	return strings.Join(reminders, "\n\n")
}

// addHit records one fired reminder for the turn log.
func (g *loopGuard) addHit(kind string, count int) {
	g.hits = append(g.hits, loopGuardHit{kind: kind, count: count})
}

// canonicalArgs returns the canonical string form of a call's arguments: JSON
// unmarshalled to a generic value and marshalled again, which sorts every map
// key. Invalid JSON falls back to the raw string (REQ-TRL-010).
func canonicalArgs(raw json.RawMessage) string {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return string(raw)
	}
	return string(canonical)
}

// loopGuardArgumentsPreviewChars caps the arguments quoted in the detailed
// reminder. It bounds only the model-visible text; the chain key always uses
// the full canonical string.
const loopGuardArgumentsPreviewChars = 500

// loopGuardGentleReminder is the first-threshold reminder (REQ-TRL-010). It is
// the deepseek-harness repeat-tool-reminder gentle text.
const loopGuardGentleReminder = "You are repeating the exact same tool call with identical arguments. " +
	"Carefully analyze the previous result before calling again: if the task is " +
	"not complete, try a different approach or different arguments instead of " +
	"repeating the call."

// loopGuardDetailedReminder names the tool, the run length, and the canonical
// arguments of a later-threshold repeat.
func loopGuardDetailedReminder(toolName string, count int, canonical string) string {
	return "Repeated tool call detected:\n" +
		"- tool: " + toolName + "\n" +
		fmt.Sprintf("- consecutive_calls: %d\n", count) +
		"- arguments: " + previewLoopGuardArgs(canonical) + "\n" +
		"The repeated calls are not making progress. Do not call this tool with " +
		"these exact arguments again. Inspect the latest result and choose a " +
		"different action, different arguments, or finish the task if enough " +
		"evidence has been gathered."
}

// loopGuardFailureReminder tells the model to stop retrying a failing
// approach (REQ-TRL-011).
func loopGuardFailureReminder(count int) string {
	return fmt.Sprintf("The last %d tool calls failed; read the errors; "+
		"do not retry a variation of the same approach; try a different "+
		"approach, or ask the user with ask_user when blocked.", count)
}

// previewLoopGuardArgs head-truncates the canonical arguments for the
// detailed reminder and marks how much was omitted.
func previewLoopGuardArgs(canonical string) string {
	if utf8.RuneCountInString(canonical) <= loopGuardArgumentsPreviewChars {
		return canonical
	}
	runes := []rune(canonical)
	omitted := len(runes) - loopGuardArgumentsPreviewChars
	return string(runes[:loopGuardArgumentsPreviewChars]) + fmt.Sprintf("… (+%d more chars)", omitted)
}
