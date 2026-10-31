package agent

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
)

// loopGuard detects repeated tool calls, consecutive tool failures, and the
// escalation cases of REQ-LPG-002..007 inside one turn. The zero value is
// ready to use; reset clears every chain and counter.
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

	// failBySig counts the error results of each non-read-only call signature
	// in the turn, consecutive or not (REQ-LPG-002).
	failBySig map[string]int
	// sameBySig tracks the last result digest and the current same-result run
	// of each read-only signature (REQ-LPG-003).
	sameBySig map[string]sameResult
	// anyArgs counts the successful read-only results of each tool and digest in
	// the turn, whatever the arguments. The key is the tool name and the digest,
	// with the cleaned path of a read_file between them; see readOnlyAnyArgsKey
	// (REQ-LPG-014).
	anyArgs map[string]int
	// chainDigests are the result digests of the current repeat chain, aligned
	// with repeat (REQ-LPG-004).
	chainDigests []string
	// history is the last loopGuardHistoryMax tracked calls of the turn
	// (REQ-LPG-005).
	history []historyEntry
	// strikes counts consecutive steps whose calls all had unknown names
	// (REQ-LPG-007).
	strikes int
	// cycles records the cycle reminders already sent, keyed by period and lap
	// count (REQ-LPG-005).
	cycles map[cycleKey]bool
	// halt is the first halt cause of the current step, or nil (REQ-LPG-009).
	halt *loopHalt
	// lastBlock is the cause of the most recent blocked call, for the turn's
	// "loop guard block" log line (REQ-LPG-013).
	lastBlock loopHalt
}

// sameResult is the state of one read-only signature: the digest of its
// previous result and the length of the current same-result run.
type sameResult struct {
	digest string
	count  int
}

// historyEntry is one tracked call of the turn: its signature key and its
// result digest.
type historyEntry struct {
	key    string
	digest string
}

// cycleKey identifies a cycle reminder by its period and lap count.
type cycleKey struct {
	period int
	laps   int
}

// loopHalt is the first halt cause of a step (REQ-LPG-008, REQ-LPG-009).
type loopHalt struct {
	kind  string
	tool  string
	count int
}

// loopGuardHit is one fired reminder: its chain kind and the chain length that
// crossed the threshold.
type loopGuardHit struct {
	kind  string
	count int
}

// The loop guard detector kinds. They name the reminder and halt log lines
// (REQ-LPG-013).
const (
	loopGuardKindRepeat          = "repeat"
	loopGuardKindFailure         = "failure"
	loopGuardKindSameArgs        = "same_args_failure"
	loopGuardKindReadOnly        = "read_only"
	loopGuardKindReadOnlyAnyArgs = "read_only_any_args"
	loopGuardKindIdenticalStreak = "identical_streak"
	loopGuardKindCycle           = "cycle"
	loopGuardKindFailureStreak   = "failure_streak"
	loopGuardKindStrikes         = "strikes"
)

// The loop guard thresholds.
const (
	// loopGuardFailWarn and loopGuardFailBlock are the reminder and block
	// counts of the same-arguments failure detector (REQ-LPG-002).
	loopGuardFailWarn  = 2
	loopGuardFailBlock = 4
	// loopGuardSameWarn and loopGuardSameBlock are the reminder and block
	// counts of the read-only no-progress detector (REQ-LPG-003).
	loopGuardSameWarn  = 2
	loopGuardSameBlock = 4
	// loopGuardAnyArgsWarn and loopGuardAnyArgsHalt are the reminder and halt
	// counts of the read-only same-result-across-arguments detector
	// (REQ-LPG-014).
	loopGuardAnyArgsWarn = 3
	loopGuardAnyArgsHalt = 8
	// loopGuardStreakLen is the exact-repeat chain length whose results must
	// all match before the next call is blocked (REQ-LPG-004).
	loopGuardStreakLen = 8
	// loopGuardHistoryMax is the number of tracked calls kept for the cycle
	// detector (REQ-LPG-005).
	loopGuardHistoryMax = 64
	// loopGuardCycleWarn and loopGuardCycleHalt are the lap counts that remind
	// and halt (REQ-LPG-005).
	loopGuardCycleWarn = 3
	loopGuardCycleHalt = 5
	// loopGuardMinPeriod and loopGuardMaxPeriod bound the cycle periods
	// (REQ-LPG-005).
	loopGuardMinPeriod = 2
	loopGuardMaxPeriod = 4
	// loopGuardFailureHalt halts the turn after that many consecutive errors
	// (REQ-LPG-006).
	loopGuardFailureHalt = 12
	// loopGuardStrikeHalt halts the turn after that many all-unknown steps
	// (REQ-LPG-007).
	loopGuardStrikeHalt = 3
)

// reset clears every chain and counter.
func (g *loopGuard) reset() {
	g.lastKey = ""
	g.repeat = 0
	g.failures = 0
	g.hits = nil
	g.failBySig = nil
	g.sameBySig = nil
	g.anyArgs = nil
	g.chainDigests = nil
	g.history = nil
	g.strikes = 0
	g.cycles = nil
	g.halt = nil
	g.lastBlock = loopHalt{}
}

// loopGuardUntracked lists the tools that are transparent to every detector:
// they neither count nor reset anything (REQ-TRL-012).
var loopGuardUntracked = map[string]bool{
	"ask_user":   true,
	"job_output": true,
	"job_list":   true,
}

// loopGuardReadOnly lists the tools whose repeated identical result is the
// read-only no-progress case (Definitions, REQ-LPG-003).
var loopGuardReadOnly = map[string]bool{
	"read_file":   true,
	"glob":        true,
	"grep":        true,
	"web_fetch":   true,
	"memory_read": true,
	"skill_load":  true,
}

// before decides one call before the step runs. A call that matches a block
// rule gets its synthetic error result and marks the step for halt; it is not
// executed. before never advances a counter (REQ-LPG-002..005, -008). Untracked
// tools are never blocked. The first match wins.
func (g *loopGuard) before(call core.ToolCall) (core.ToolOutput, bool) {
	if loopGuardUntracked[call.Name] {
		return core.ToolOutput{}, false
	}
	key := call.Name + "\x00" + canonicalArgs(call.Input)
	switch {
	case g.failBySig[key] >= loopGuardFailBlock:
		return g.block(call, loopGuardKindSameArgs, g.failBySig[key], loopGuardBlockedFailureText), true
	case loopGuardReadOnly[call.Name] && g.sameBySig[key].count >= loopGuardSameBlock:
		return g.block(call, loopGuardKindReadOnly, g.sameBySig[key].count, loopGuardBlockedReadOnlyText), true
	case key == g.lastKey && g.repeat == loopGuardStreakLen && allEqualDigest(g.chainDigests):
		return g.block(call, loopGuardKindIdenticalStreak, len(g.chainDigests), loopGuardBlockedIdenticalText), true
	}
	return core.ToolOutput{}, false
}

// block builds the synthetic error result of a blocked call and records the
// step's first halt cause (REQ-LPG-008).
func (g *loopGuard) block(call core.ToolCall, kind string, count int, format string) core.ToolOutput {
	out := core.ToolOutput{Status: core.ToolStatusError, Output: fmt.Sprintf(format, call.Name, count)}
	out.Digest = outputDigest(out)
	g.lastBlock = loopHalt{kind: kind, tool: call.Name, count: count}
	g.setHalt(kind, call.Name, count)
	return out
}

// setHalt records the first halt cause of the step (REQ-LPG-009).
func (g *loopGuard) setHalt(kind, tool string, count int) {
	if g.halt != nil {
		return
	}
	g.halt = &loopHalt{kind: kind, tool: tool, count: count}
}

// strike records a step whose calls all had unknown names (REQ-LPG-007). A step
// with at least one valid name resets the strikes.
func (g *loopGuard) strike(allUnknown bool) {
	if !allUnknown {
		g.strikes = 0
		return
	}
	g.strikes++
	if g.strikes >= loopGuardStrikeHalt {
		g.setHalt(loopGuardKindStrikes, "", g.strikes)
	}
}

// observe processes one step's calls and outputs in call order and returns the
// reminder text for the next request, or "". Several reminders fired in one
// step are deduplicated by text and joined by a blank line.
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
		reminders = g.observeRepeat(call, canonical, reminders)
		if i >= len(outputs) {
			continue
		}
		reminders = g.observeResult(call, key, outputs[i], reminders)
	}
	return strings.Join(reminders, "\n\n")
}

// observeRepeat fires the REQ-TRL-010 repeat reminders, unchanged by
// REQ-LPG-001.
func (g *loopGuard) observeRepeat(call core.ToolCall, canonical string, reminders []string) []string {
	switch g.repeat {
	case 3:
		g.addHit(loopGuardKindRepeat, g.repeat)
		return appendReminder(reminders, loopGuardGentleReminder)
	case 5, 8:
		g.addHit(loopGuardKindRepeat, g.repeat)
		return appendReminder(reminders, loopGuardDetailedReminder(call.Name, g.repeat, canonical))
	}
	return reminders
}

// observeResult advances the digest-based detectors for one tracked call
// (REQ-LPG-002..006).
func (g *loopGuard) observeResult(call core.ToolCall, key string, out core.ToolOutput, reminders []string) []string {
	// REQ-LPG-004: keep the digests of the current repeat chain.
	if g.repeat == 1 {
		g.chainDigests = g.chainDigests[:0]
	}
	g.chainDigests = append(g.chainDigests, out.Digest)

	// A progress write resets every signature (REQ-LPG-002, REQ-LPG-003) and
	// the same-result-across-arguments counts (REQ-LPG-014).
	if isProgressWrite(call, out) {
		g.failBySig = nil
		g.sameBySig = nil
		g.anyArgs = nil
	}

	// REQ-LPG-003: the read-only no-progress run.
	if loopGuardReadOnly[call.Name] {
		reminders = g.observeReadOnly(call, key, out, reminders)
	}

	// REQ-LPG-014: the read-only same result across different arguments.
	reminders = g.observeAnyArgs(call, out, reminders)

	// REQ-LPG-002: the same-arguments failure count. The new detectors only
	// track results that carry a digest, that is, results produced by the tool
	// pipeline.
	switch {
	case out.Status == core.ToolStatusError:
		if out.Digest != "" {
			g.bumpFail(key)
			if g.failBySig[key] == loopGuardFailWarn {
				g.addHit(loopGuardKindSameArgs, g.failBySig[key])
				reminders = appendReminder(reminders, loopGuardSameArgsReminder(call.Name, g.failBySig[key]))
			}
		}
	case out.Status == core.ToolStatusSuccess:
		// A success resets the count of its own signature (REQ-LPG-002).
		delete(g.failBySig, key)
	}

	// REQ-TRL-011 and REQ-LPG-006: the streak of consecutive errors.
	switch out.Status {
	case core.ToolStatusError:
		g.failures++
		if g.failures == 4 || g.failures == 8 {
			g.addHit(loopGuardKindFailure, g.failures)
			reminders = appendReminder(reminders, loopGuardFailureReminder(g.failures))
		}
		if g.failures >= loopGuardFailureHalt {
			g.setHalt(loopGuardKindFailureStreak, call.Name, g.failures)
		}
	case core.ToolStatusSuccess:
		// A success ends the streak; other statuses leave it untouched
		// (REQ-TRL-011).
		g.failures = 0
	}

	g.recordHistory(key, out.Digest)
	return g.observeCycle(call.Name, reminders)
}

// observeReadOnly advances the read-only no-progress detector (REQ-LPG-003).
func (g *loopGuard) observeReadOnly(call core.ToolCall, key string, out core.ToolOutput, reminders []string) []string {
	if out.Digest == "" {
		// A result without a digest was not produced by the tool pipeline, so
		// there is nothing to compare.
		delete(g.sameBySig, key)
		return reminders
	}
	prev := g.sameBySig[key]
	switch {
	case out.Status == core.ToolStatusError:
		// An error result resets the run to the new result (REQ-LPG-003).
		prev = sameResult{digest: out.Digest, count: 1}
	case prev.digest == out.Digest:
		prev.count++
	default:
		prev = sameResult{digest: out.Digest, count: 1}
	}
	if g.sameBySig == nil {
		g.sameBySig = map[string]sameResult{}
	}
	g.sameBySig[key] = prev
	if prev.count == loopGuardSameWarn {
		g.addHit(loopGuardKindReadOnly, prev.count)
		reminders = appendReminder(reminders, loopGuardReadOnlyReminder(call.Name, prev.count))
	}
	return reminders
}

// observeAnyArgs advances the read-only same-result-across-arguments detector:
// a read-only tool whose successful result digest repeats whatever the
// arguments. It reminds at loopGuardAnyArgsWarn (once per key) and records the
// halt at loopGuardAnyArgsHalt. Error results, empty search results, and empty
// results (output "") do not count (REQ-LPG-014).
func (g *loopGuard) observeAnyArgs(call core.ToolCall, out core.ToolOutput, reminders []string) []string {
	if !loopGuardReadOnly[call.Name] || out.Status != core.ToolStatusSuccess ||
		out.Digest == "" || out.Output == "" || loopGuardEmptySearchResult(call, out) {
		return reminders
	}
	if g.anyArgs == nil {
		g.anyArgs = map[string]int{}
	}
	key := readOnlyAnyArgsKey(call, out.Digest)
	g.anyArgs[key]++
	count := g.anyArgs[key]
	switch {
	case count == loopGuardAnyArgsWarn:
		g.addHit(loopGuardKindReadOnlyAnyArgs, count)
		reminders = appendReminder(reminders, loopGuardAnyArgsReminder(call.Name, count))
	case count >= loopGuardAnyArgsHalt:
		g.setHalt(loopGuardKindReadOnlyAnyArgs, call.Name, count)
	}
	return reminders
}

// readOnlyAnyArgsKey returns the counting key of a successful read-only result
// for REQ-LPG-014: the tool name and the result digest, with the cleaned path
// between them for read_file, so different files with the same text are not
// counted together. A read_file call whose input is not a JSON object, or whose
// "path" is missing or not a JSON string, falls back to the short
// tool-and-digest key.
func readOnlyAnyArgsKey(call core.ToolCall, digest string) string {
	short := call.Name + "\x00" + digest
	if call.Name != "read_file" {
		return short
	}
	var fields map[string]any
	if err := json.Unmarshal(call.Input, &fields); err != nil {
		return short
	}
	p, ok := fields["path"].(string)
	if !ok {
		return short
	}
	return call.Name + "\x00" + path.Clean(p) + "\x00" + digest
}

// loopGuardEmptySearchResult reports whether a result is an empty search: a
// "grep" result of exactly "no matches" or a "glob" result of exactly
// "no results". It is never counted by REQ-LPG-014.
func loopGuardEmptySearchResult(call core.ToolCall, out core.ToolOutput) bool {
	switch call.Name {
	case "grep":
		return out.Output == "no matches"
	case "glob":
		return out.Output == "no results"
	default:
		return false
	}
}

// observeCycle fires the cycle reminder and records the cycle halt at the end
// of the history (REQ-LPG-005).
func (g *loopGuard) observeCycle(tool string, reminders []string) []string {
	for period := loopGuardMinPeriod; period <= loopGuardMaxPeriod; period++ {
		laps := cycleLaps(g.history, period)
		if laps < loopGuardCycleWarn {
			continue
		}
		if laps >= loopGuardCycleHalt {
			g.setHalt(loopGuardKindCycle, tool, laps)
		}
		if laps != loopGuardCycleWarn || g.cycleSeen(period, laps) {
			continue
		}
		g.addHit(loopGuardKindCycle, laps)
		reminders = appendReminder(reminders, loopGuardCycleReminder(laps, period, tool))
	}
	return reminders
}

// cycleSeen records and reports whether a cycle reminder fired already for one
// period and lap count (REQ-LPG-005).
func (g *loopGuard) cycleSeen(period, laps int) bool {
	if g.cycles == nil {
		g.cycles = map[cycleKey]bool{}
	}
	key := cycleKey{period: period, laps: laps}
	if g.cycles[key] {
		return true
	}
	g.cycles[key] = true
	return false
}

// recordHistory appends one tracked call to the cycle history, keeping the
// newest loopGuardHistoryMax entries (REQ-LPG-005).
func (g *loopGuard) recordHistory(key, digest string) {
	g.history = append(g.history, historyEntry{key: key, digest: digest})
	if len(g.history) > loopGuardHistoryMax {
		g.history = g.history[len(g.history)-loopGuardHistoryMax:]
	}
}

// bumpFail adds one error result to a signature's count (REQ-LPG-002).
func (g *loopGuard) bumpFail(key string) {
	if g.failBySig == nil {
		g.failBySig = map[string]int{}
	}
	g.failBySig[key]++
}

// addHit records one fired reminder for the turn log.
func (g *loopGuard) addHit(kind string, count int) {
	g.hits = append(g.hits, loopGuardHit{kind: kind, count: count})
}

// cycleLaps returns the number of full cycles of the given period at the end of
// history, or 0 when the last cycle is a run of one repeated call, which is the
// repeat chain and not a cycle (REQ-LPG-005).
func cycleLaps(history []historyEntry, period int) int {
	if len(history) < period*2 {
		return 0
	}
	last := history[len(history)-period:]
	identical := true
	for i := 1; i < len(last); i++ {
		if last[i] != last[0] {
			identical = false
			break
		}
	}
	if identical {
		return 0
	}
	matches := 0
	for i := len(history) - 1; i >= period; i-- {
		if history[i] != history[i-period] {
			break
		}
		matches++
	}
	if matches == 0 {
		return 0
	}
	return (matches + period) / period
}

// allEqualDigest reports whether digests holds at least one non-empty digest
// and all of them are equal (REQ-LPG-004).
func allEqualDigest(digests []string) bool {
	if len(digests) == 0 || digests[0] == "" {
		return false
	}
	for _, digest := range digests[1:] {
		if digest != digests[0] {
			return false
		}
	}
	return true
}

// isProgressWrite reports whether a call is a successful write_file or
// edit_file, which resets every signature count (Definitions, REQ-LPG-002).
func isProgressWrite(call core.ToolCall, out core.ToolOutput) bool {
	if out.Status != core.ToolStatusSuccess {
		return false
	}
	return call.Name == "write_file" || call.Name == "edit_file"
}

// appendReminder adds text unless an identical reminder is already queued
// (REQ-LPG-002..005).
func appendReminder(reminders []string, text string) []string {
	for _, existing := range reminders {
		if existing == text {
			return reminders
		}
	}
	return append(reminders, text)
}

// allUnknownNames reports whether every result of a step came from a call with
// an unknown tool name (REQ-LPG-007).
func allUnknownNames(outputs []core.ToolOutput) bool {
	if len(outputs) == 0 {
		return false
	}
	for _, out := range outputs {
		if !out.UnknownName {
			return false
		}
	}
	return true
}

// loopGuardSameArgsReminder is the same-arguments failure reminder
// (REQ-LPG-002).
func loopGuardSameArgsReminder(toolName string, count int) string {
	return fmt.Sprintf("%s has failed %d times with identical arguments. This looks like a loop; "+
		"inspect the error and change strategy instead of retrying it unchanged.", toolName, count)
}

// loopGuardReadOnlyReminder is the read-only no-progress reminder
// (REQ-LPG-003).
func loopGuardReadOnlyReminder(toolName string, count int) string {
	return fmt.Sprintf("%s returned the same result %d times. Use the result already provided or "+
		"change the query instead of repeating it unchanged.", toolName, count)
}

// loopGuardAnyArgsReminder is the read-only same-result-across-arguments
// reminder (REQ-LPG-014).
func loopGuardAnyArgsReminder(toolName string, count int) string {
	return fmt.Sprintf("%s returned the same result %d times with different arguments. Changing the "+
		"arguments does not give new information; use the result already provided or try a "+
		"different approach.", toolName, count)
}

// loopGuardCycleReminder is the cycle reminder (REQ-LPG-005).
func loopGuardCycleReminder(laps, period int, toolName string) string {
	return fmt.Sprintf("The last %d rounds repeated the same cycle of %d tool calls (ending with %s) "+
		"with identical arguments and identical results. Do not repeat the batch; change arguments, "+
		"use a different tool, or proceed with what you have.", laps, period, toolName)
}

// The blocked-call error texts (REQ-LPG-008). The first format argument is the
// tool name and the second is the count.
const (
	loopGuardBlockedFailureText   = "Blocked %s: the same tool call failed %d times with identical arguments. Stop retrying it unchanged; change strategy or explain the blocker."
	loopGuardBlockedReadOnlyText  = "Blocked %s: this read-only call returned the same result %d times. Stop repeating it unchanged; use the result already provided or try a different query."
	loopGuardBlockedIdenticalText = "Blocked %s: the same call with identical arguments returned the same result %d times in a row. Stop repeating it unchanged; use the result already provided or change strategy."
)

// loopGuardHaltNote is the runtime note of a halt (REQ-LPG-009).
func loopGuardHaltNote(halt loopHalt) string {
	return "lana stopped this turn because the same work repeated without progress (" +
		halt.detail() + "). Do not call tools. Tell the owner in their language what you tried, " +
		"what blocked you, and what you need from them to continue."
}

// detail names the detector, the tool, and the count of a halt (REQ-LPG-009).
func (h loopHalt) detail() string {
	switch h.kind {
	case loopGuardKindSameArgs:
		return fmt.Sprintf("%s failed %d times with identical arguments", h.tool, h.count)
	case loopGuardKindReadOnly:
		return fmt.Sprintf("%s returned the same result %d times", h.tool, h.count)
	case loopGuardKindReadOnlyAnyArgs:
		return fmt.Sprintf("%s returned the same result %d times with different arguments", h.tool, h.count)
	case loopGuardKindIdenticalStreak:
		return fmt.Sprintf("%s repeated the same call and result %d times in a row", h.tool, h.count)
	case loopGuardKindCycle:
		return fmt.Sprintf("a cycle ending with %s repeated %d times", h.tool, h.count)
	case loopGuardKindFailureStreak:
		return fmt.Sprintf("%d tool calls failed in a row", h.count)
	case loopGuardKindStrikes:
		return fmt.Sprintf("%d steps in a row called unknown tools", h.count)
	default:
		return h.kind
	}
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
