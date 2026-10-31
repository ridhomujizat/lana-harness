package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// Status values of one task result.
const (
	statusPass  = "pass"
	statusFail  = "fail"
	statusError = "error"
)

// Loop-guard reminder prefixes (REQ-TRL-023, REQ-TRL-010, REQ-TRL-011). A
// runtime user message that starts with one of them is a loop-guard reminder.
const (
	reminderRepeatPrefix   = "You are repeating the exact same tool call"
	reminderRepeatedPrefix = "Repeated tool call detected"
	reminderFailurePrefix  = "The last "
)

// bashFileOpWords are the first words that count as a file operation in bash
// (REQ-TRL-023). echo counts only with a redirect, and any command with a
// heredoc counts.
var bashFileOpWords = map[string]bool{
	"cat":    true,
	"head":   true,
	"tail":   true,
	"sed":    true,
	"awk":    true,
	"grep":   true,
	"find":   true,
	"printf": true,
}

// taskResult is the report row of one task (REQ-TRL-023).
type taskResult struct {
	Name         string         `json:"name"`
	Status       string         `json:"status"`
	CheckOutput  string         `json:"check_output"`
	Error        string         `json:"error,omitempty"`
	DurationMS   int64          `json:"duration_ms"`
	Steps        int            `json:"steps"`
	ToolCalls    map[string]int `json:"tool_calls"`
	ToolErrors   int            `json:"tool_errors"`
	BashFileOps  int            `json:"bash_file_ops"`
	Reminders    int            `json:"reminders"`
	Compactions  int            `json:"compactions"`
	InputTokens  int64          `json:"input_tokens"`
	CachedTokens int64          `json:"cached_tokens"`
	OutputTokens int64          `json:"output_tokens"`
	CacheHitPct  float64        `json:"cache_hit_pct"`
	TurnEnd      string         `json:"turn_end,omitempty"`
	CostUSD      *float64       `json:"cost_usd,omitempty"`
}

// modelReport is one model's part of a report (REQ-ECX-042).
type modelReport struct {
	Model string       `json:"model"`
	Tasks []taskResult `json:"tasks"`
	Total taskResult   `json:"total"`
}

// report is the JSON document written by -out (design section 5). A single
// model keeps the original top-level fields so older reports still load; a
// multi-model run writes one modelReport per model.
type report struct {
	Model   string        `json:"model,omitempty"`
	Started time.Time     `json:"started"`
	Tasks   []taskResult  `json:"tasks,omitempty"`
	Total   *taskResult   `json:"total,omitempty"`
	Models  []modelReport `json:"models,omitempty"`
}

// modelReports returns the report as one model entry per run, accepting both
// the current multi-model shape and the older single-model shape.
func (r *report) modelReports() []modelReport {
	if r == nil {
		return nil
	}
	if len(r.Models) > 0 {
		return r.Models
	}
	if r.Model == "" && len(r.Tasks) == 0 && r.Total == nil {
		return nil
	}
	run := modelReport{Model: r.Model, Tasks: r.Tasks}
	if r.Total != nil {
		run.Total = *r.Total
	}
	return []modelReport{run}
}

// previousForModel returns the previous report restricted to model, or nil
// when the earlier report has no matching model. The compare is by model and
// task (REQ-ECX-042).
func previousForModel(previous *report, model string) *report {
	if previous == nil {
		return nil
	}
	for _, run := range previous.modelReports() {
		if run.Model != model {
			continue
		}
		total := run.Total
		return &report{Model: run.Model, Tasks: run.Tasks, Total: &total}
	}
	return nil
}

// taskMetrics is the event-derived part of a task result.
type taskMetrics struct {
	Steps        int
	ToolCalls    map[string]int
	ToolErrors   int
	BashFileOps  int
	Reminders    int
	Compactions  int
	InputTokens  int64
	CachedTokens int64
	OutputTokens int64
	CacheHitPct  float64
	TurnEnd      string
}

// priceTable holds the optional -price-* values. Cost is computed only when
// all three flags are set (REQ-TRL-025).
type priceTable struct {
	in     float64
	cached float64
	out    float64
	set    bool
}

// cost returns the USD cost of a token count, or nil when prices are unset.
func (p priceTable) cost(input, cached, output int64) *float64 {
	if !p.set {
		return nil
	}
	uncached := input - cached
	if uncached < 0 {
		uncached = 0
	}
	cost := (float64(uncached)*p.in + float64(cached)*p.cached + float64(output)*p.out) / 1e6
	return &cost
}

// computeMetrics reads the report metrics from a session's event log.
func computeMetrics(events []core.SessionEvent) (taskMetrics, error) {
	m := taskMetrics{ToolCalls: map[string]int{}}
	for _, event := range events {
		switch event.Kind {
		case core.KindStepEnd:
			var payload core.StepEnd
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return m, fmt.Errorf("step/end: %w", err)
			}
			m.Steps++
			m.InputTokens += payload.Usage.InputTokens
			m.CachedTokens += payload.Usage.CachedInputTokens
			m.OutputTokens += payload.Usage.OutputTokens
		case core.KindToolCall:
			var payload core.ToolCallPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return m, fmt.Errorf("tool/call: %w", err)
			}
			m.ToolCalls[payload.Name]++
			if payload.Name != "bash" {
				continue
			}
			var input struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal(payload.Input, &input); err == nil && isBashFileOp(input.Command) {
				m.BashFileOps++
			}
		case core.KindToolResult:
			var payload core.ToolResultPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return m, fmt.Errorf("tool/result: %w", err)
			}
			if payload.Status == core.ToolStatusError {
				m.ToolErrors++
			}
		case core.KindUserMessage:
			var payload core.UserMessage
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return m, fmt.Errorf("user/message: %w", err)
			}
			if isLoopGuardReminder(payload) {
				m.Reminders++
			}
		case core.KindCompactionSummary:
			m.Compactions++
		case core.KindTurnEnd:
			var payload core.TurnEnd
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return m, fmt.Errorf("turn/end: %w", err)
			}
			m.TurnEnd = payload.Reason
		}
	}
	m.CacheHitPct = cacheHitPct(m.CachedTokens, m.InputTokens)
	return m, nil
}

// isLoopGuardReminder reports whether a user message is a runtime loop-guard
// note (REQ-TRL-013, REQ-TRL-023).
func isLoopGuardReminder(message core.UserMessage) bool {
	if !message.Runtime {
		return false
	}
	text := strings.TrimSpace(userMessageText(message))
	for _, prefix := range []string{reminderRepeatPrefix, reminderRepeatedPrefix, reminderFailurePrefix} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// userMessageText joins the text blocks of a user message.
func userMessageText(message core.UserMessage) string {
	var text strings.Builder
	for _, block := range message.Content {
		if block.Kind == core.BlockText {
			text.WriteString(block.Text)
		}
	}
	return text.String()
}

// isBashFileOp applies the REQ-TRL-023 heuristic to a bash command.
func isBashFileOp(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	if strings.Contains(command, "<<") {
		return true
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	if bashFileOpWords[fields[0]] {
		return true
	}
	return fields[0] == "echo" && strings.Contains(command, ">")
}

// cacheHitPct is the cached share of the input tokens.
func cacheHitPct(cached, input int64) float64 {
	if input <= 0 {
		return 0
	}
	return float64(cached) * 100 / float64(input)
}

// totalToolCalls returns the number of tool calls in a task.
func totalToolCalls(calls map[string]int) int {
	total := 0
	for _, count := range calls {
		total += count
	}
	return total
}

// buildTotal sums all task rows and fills the total row of the report.
func buildTotal(tasks []taskResult, prices priceTable) taskResult {
	total := taskResult{Name: "total", ToolCalls: map[string]int{}}
	passed, failed, errored := 0, 0, 0
	for _, task := range tasks {
		switch task.Status {
		case statusPass:
			passed++
		case statusFail:
			failed++
		case statusError:
			errored++
		}
		total.DurationMS += task.DurationMS
		total.Steps += task.Steps
		total.ToolErrors += task.ToolErrors
		total.BashFileOps += task.BashFileOps
		total.Reminders += task.Reminders
		total.Compactions += task.Compactions
		total.InputTokens += task.InputTokens
		total.CachedTokens += task.CachedTokens
		total.OutputTokens += task.OutputTokens
		for name, count := range task.ToolCalls {
			total.ToolCalls[name] += count
		}
	}
	total.Status = fmt.Sprintf("%d pass, %d fail, %d error", passed, failed, errored)
	total.CacheHitPct = cacheHitPct(total.CachedTokens, total.InputTokens)
	total.CostUSD = prices.cost(total.InputTokens, total.CachedTokens, total.OutputTokens)
	return total
}

// loadReport reads an earlier JSON report for -compare.
func loadReport(path string) (*report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var value report
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &value, nil
}

// printReports writes one table per model, each with its own total row. A
// multi-model run gets a model heading before each table (REQ-ECX-042).
func printReports(out io.Writer, runs []modelReport, previous *report, showCost bool) {
	for i, run := range runs {
		if len(runs) > 1 {
			if i > 0 {
				fmt.Fprintln(out)
			}
			fmt.Fprintf(out, "model: %s\n", run.Model)
		}
		printTable(out, run.Tasks, run.Total, previousForModel(previous, run.Model), showCost)
	}
}

// printTable writes the report table through text/tabwriter.
func printTable(out io.Writer, tasks []taskResult, total taskResult, previous *report, showCost bool) {
	previousTasks := map[string]taskResult{}
	if previous != nil {
		for _, task := range previous.Tasks {
			previousTasks[task.Name] = task
		}
		if previous.Total != nil && previous.Total.Name != "" {
			previousTasks[previous.Total.Name] = *previous.Total
		}
	}
	header := []string{"TASK", "STATUS", "DURATION", "STEPS", "CALLS", "TOOLS", "ERRORS", "BASH", "REMIND", "COMPACT", "INPUT", "CACHED", "HIT", "OUTPUT"}
	if showCost {
		header = append(header, "COST")
	}
	header = append(header, "END")

	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(header, "\t"))
	for _, task := range tasks {
		prev, has := previousTasks[task.Name]
		fmt.Fprintln(w, tableRow(task, prev, has, showCost))
	}
	prev, has := previousTasks[total.Name]
	fmt.Fprintln(w, tableRow(total, prev, has, showCost))
	if err := w.Flush(); err != nil {
		return
	}
}

// tableRow renders one report row.
func tableRow(task taskResult, prev taskResult, hasPrev, showCost bool) string {
	cells := []string{
		task.Name,
		task.Status,
		formatDurationDelta(task.DurationMS, prev.DurationMS, hasPrev),
		formatIntDelta(task.Steps, prev.Steps, hasPrev),
		formatIntDelta(totalToolCalls(task.ToolCalls), totalToolCalls(prev.ToolCalls), hasPrev),
		formatToolCalls(task.ToolCalls),
		formatIntDelta(task.ToolErrors, prev.ToolErrors, hasPrev),
		formatIntDelta(task.BashFileOps, prev.BashFileOps, hasPrev),
		formatIntDelta(task.Reminders, prev.Reminders, hasPrev),
		formatIntDelta(task.Compactions, prev.Compactions, hasPrev),
		formatInt64Delta(task.InputTokens, prev.InputTokens, hasPrev),
		formatInt64Delta(task.CachedTokens, prev.CachedTokens, hasPrev),
		formatPctDelta(task.CacheHitPct, prev.CacheHitPct, hasPrev),
		formatInt64Delta(task.OutputTokens, prev.OutputTokens, hasPrev),
	}
	if showCost {
		current, previous := 0.0, 0.0
		if task.CostUSD != nil {
			current = *task.CostUSD
		}
		if prev.CostUSD != nil {
			previous = *prev.CostUSD
		}
		cells = append(cells, formatCostDelta(current, previous, hasPrev))
	}
	cells = append(cells, task.TurnEnd)
	return strings.Join(cells, "\t")
}

// formatIntDelta renders an integer with its signed change.
func formatIntDelta(current, previous int, hasPrev bool) string {
	text := strconv.Itoa(current)
	if !hasPrev {
		return text
	}
	return text + " (" + signedInt(current-previous) + ")"
}

// formatInt64Delta renders an int64 with its signed change.
func formatInt64Delta(current, previous int64, hasPrev bool) string {
	text := strconv.FormatInt(current, 10)
	if !hasPrev {
		return text
	}
	return text + " (" + signedInt64(current-previous) + ")"
}

func signedInt(value int) string {
	if value >= 0 {
		return "+" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
}

func signedInt64(value int64) string {
	if value >= 0 {
		return "+" + strconv.FormatInt(value, 10)
	}
	return strconv.FormatInt(value, 10)
}

// formatDurationMS renders a duration in milliseconds.
func formatDurationMS(ms int64) string {
	if ms == 0 {
		return "0s"
	}
	return (time.Duration(ms) * time.Millisecond).Round(time.Millisecond).String()
}

// formatDurationDelta renders a duration with its signed change.
func formatDurationDelta(current, previous int64, hasPrev bool) string {
	text := formatDurationMS(current)
	if !hasPrev {
		return text
	}
	delta := time.Duration(current-previous) * time.Millisecond
	return text + " (" + signedDuration(delta) + ")"
}

func signedDuration(delta time.Duration) string {
	delta = delta.Round(time.Millisecond)
	if delta >= 0 {
		return "+" + delta.String()
	}
	return delta.String()
}

// formatPctDelta renders a percentage with its change in percentage points.
func formatPctDelta(current, previous float64, hasPrev bool) string {
	text := fmt.Sprintf("%.1f%%", current)
	if !hasPrev {
		return text
	}
	return text + fmt.Sprintf(" (%+.1fpp)", current-previous)
}

// formatCostDelta renders a USD cost with its signed change.
func formatCostDelta(current, previous float64, hasPrev bool) string {
	text := fmt.Sprintf("$%.6f", current)
	if !hasPrev {
		return text
	}
	return text + " (" + signedCost(current-previous) + ")"
}

func signedCost(delta float64) string {
	if delta >= 0 {
		return fmt.Sprintf("+$%.6f", delta)
	}
	return fmt.Sprintf("-$%.6f", -delta)
}

// formatToolCalls renders the per-name tool calls in stable order.
func formatToolCalls(calls map[string]int) string {
	if len(calls) == 0 {
		return "-"
	}
	names := make([]string, 0, len(calls))
	for name := range calls {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s:%d", name, calls[name]))
	}
	return strings.Join(parts, " ")
}

// tailText returns at most max trailing bytes of text.
func tailText(text string, max int) string {
	if len(text) <= max {
		return text
	}
	return text[len(text)-max:]
}
