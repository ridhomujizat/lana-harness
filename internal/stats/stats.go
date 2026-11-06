// Package stats aggregates session events and daemon log records into the
// monitoring dashboard payload of API-DSH-001. It is pure: it reads no store
// and no clock beyond the range it is given.
package stats

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/logring"
)

// Limits of API-DSH-001: at most 100 error rows, tool messages at most 300
// characters, and the tools table's last_error at most 200.
const (
	errorRowLimit  = 100
	toolMessageMax = 300
	lastErrorMax   = 200
)

// ErrInvalidRange is returned by ParseRange for a name outside the three
// dashboard ranges (API-DSH-001).
var ErrInvalidRange = errors.New("range must be one of 24h, 7d, 30d")

// ScannedKinds are the event kinds the dashboard aggregates (design.md §2).
var ScannedKinds = []string{
	core.KindStepStart, core.KindStepEnd, core.KindTurnStart, core.KindTurnEnd,
	core.KindToolCall, core.KindToolResult, core.KindAssistantAttempt,
	core.KindCompactionSummary, core.KindMemoryCaptured,
}

// Range is the resolved window of a dashboard request. From and To are Unix
// milliseconds; BucketMS is the width of one series bucket.
type Range struct {
	From     int64
	To       int64
	BucketMS int64
}

// ValidRange reports whether name is one of the dashboard range names
// (API-DSH-001).
func ValidRange(name string) bool {
	switch name {
	case "24h", "7d", "30d":
		return true
	}
	return false
}

// ParseRange resolves a range name against now. Valid names are "24h" (one
// hour buckets), "7d", and "30d" (one day buckets; API-DSH-001).
func ParseRange(name string, now time.Time) (Range, error) {
	var span, bucket time.Duration
	switch name {
	case "24h":
		span, bucket = 24*time.Hour, time.Hour
	case "7d":
		span, bucket = 7*24*time.Hour, 24*time.Hour
	case "30d":
		span, bucket = 30*24*time.Hour, 24*time.Hour
	default:
		return Range{}, fmt.Errorf("%w: %q", ErrInvalidRange, name)
	}
	to := now.UnixMilli()
	return Range{From: to - span.Milliseconds(), To: to, BucketMS: bucket.Milliseconds()}, nil
}

// Dashboard is the API-DSH-001 payload. Lists are always non-nil so the JSON
// encodes empty arrays, never null.
type Dashboard struct {
	Range           string        `json:"range"`
	Agent           string        `json:"agent"`
	Agents          []string      `json:"agents"`
	From            int64         `json:"from"`
	To              int64         `json:"to"`
	BucketMS        int64         `json:"bucket_ms"`
	DaemonStartedAt int64         `json:"daemon_started_at"`
	Now             Now           `json:"now"`
	Summary         Summary       `json:"summary"`
	Series          []SeriesPoint `json:"series"`
	ByModel         []ModelRow    `json:"by_model"`
	ByAgent         []AgentRow    `json:"by_agent"`
	BySource        []SourceRow   `json:"by_source"`
	Tools           []ToolRow     `json:"tools"`
	TurnReasons     []ReasonRow   `json:"turn_reasons"`
	TurnDuration    Duration      `json:"turn_duration"`
	Errors          []ErrorRow    `json:"errors"`
}

// Now is the "Right now" block (REQ-DSH-002). The backend fills it.
type Now struct {
	SessionsRunning      int64 `json:"sessions_running"`
	SessionsWaitingInput int64 `json:"sessions_waiting_input"`
}

// Summary is the four headline numbers (REQ-DSH-003).
type Summary struct {
	InputTokens       int64 `json:"input_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	Turns             int64 `json:"turns"`
	TurnsFailed       int64 `json:"turns_failed"`
	TurnsCancelled    int64 `json:"turns_cancelled"`
	ToolCalls         int64 `json:"tool_calls"`
	ToolErrors        int64 `json:"tool_errors"`
	ToolRejected      int64 `json:"tool_rejected"`
	ProviderRetries   int64 `json:"provider_retries"`
}

// SeriesPoint is one bucket of the token chart (REQ-DSH-004).
type SeriesPoint struct {
	Start             int64 `json:"start"`
	InputTokens       int64 `json:"input_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
}

// ModelRow is one row of the by-model table.
type ModelRow struct {
	Provider          string `json:"provider"`
	Model             string `json:"model"`
	Requests          int64  `json:"requests"`
	InputTokens       int64  `json:"input_tokens"`
	CachedInputTokens int64  `json:"cached_input_tokens"`
	OutputTokens      int64  `json:"output_tokens"`
}

// AgentRow is one row of the by-agent table.
type AgentRow struct {
	Agent             string `json:"agent"`
	Turns             int64  `json:"turns"`
	InputTokens       int64  `json:"input_tokens"`
	CachedInputTokens int64  `json:"cached_input_tokens"`
	OutputTokens      int64  `json:"output_tokens"`
}

// SourceRow is one row of the by-source table.
type SourceRow struct {
	Source            string `json:"source"`
	Count             int64  `json:"count"`
	InputTokens       int64  `json:"input_tokens"`
	CachedInputTokens int64  `json:"cached_input_tokens"`
	OutputTokens      int64  `json:"output_tokens"`
}

// ToolRow is one row of the tools table (REQ-DSH-006).
type ToolRow struct {
	Name        string `json:"name"`
	Calls       int64  `json:"calls"`
	Errors      int64  `json:"errors"`
	Rejected    int64  `json:"rejected"`
	AvgMS       int64  `json:"avg_ms"`
	P95MS       int64  `json:"p95_ms"`
	LastError   string `json:"last_error"`
	LastErrorAt int64  `json:"last_error_at"`
}

// ReasonRow is one row of the turn-outcomes table (REQ-DSH-007).
type ReasonRow struct {
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
}

// Duration is an average and a nearest-rank p95, in milliseconds.
type Duration struct {
	AvgMS int64 `json:"avg_ms"`
	P95MS int64 `json:"p95_ms"`
}

// ErrorRow is one row of the error log (REQ-DSH-008).
type ErrorRow struct {
	At        int64  `json:"at"`
	Source    string `json:"source"`
	Level     string `json:"level"`
	Agent     string `json:"agent"`
	SessionID string `json:"session_id"`
	TurnID    string `json:"turn_id"`
	Tool      string `json:"tool"`
	Message   string `json:"message"`
}

// Aggregate folds the scanned events and the daemon log records into the
// dashboard payload. It fills every section except `now`, `range`, and
// `daemon_started_at`, which the backend owns (design.md §3).
//
// A non-empty agent filter aggregates only events of that agent and drops
// daemon log records, which carry no agent. Agents always lists every agent
// seen in the events, ignoring the filter (REQ-DSH-011, API-DSH-001 r2).
func Aggregate(r Range, agent string, events []core.ScannedEvent, logs []logring.Record) Dashboard {
	d := Dashboard{
		Agent:       agent,
		Agents:      agentNames(events),
		From:        r.From,
		To:          r.To,
		BucketMS:    r.BucketMS,
		Series:      []SeriesPoint{},
		ByModel:     []ModelRow{},
		ByAgent:     []AgentRow{},
		BySource:    []SourceRow{},
		Tools:       []ToolRow{},
		TurnReasons: []ReasonRow{},
		Errors:      []ErrorRow{},
	}
	series, first := newSeries(r)
	d.Series = series

	modelRows := map[[2]string]*ModelRow{}
	agentRows := map[string]*AgentRow{}
	sources := map[string]*SourceRow{
		"turn":           {Source: "turn"},
		"compaction":     {Source: "compaction"},
		"memory_capture": {Source: "memory_capture"},
	}
	tools := map[string]*toolRowAcc{}
	toolStarts := map[string]toolStart{}
	turnStarts := map[string]int64{}
	reasons := map[string]int64{}
	var turnDurations []int64
	var summary Summary
	var errorRows []ErrorRow

	addSeries := func(at int64, usage core.Usage) {
		if len(series) == 0 {
			return
		}
		index := (bucketStart(at, r.BucketMS) - first) / r.BucketMS
		if index < 0 || index >= int64(len(series)) {
			return
		}
		series[index].InputTokens += usage.InputTokens
		series[index].CachedInputTokens += usage.CachedInputTokens
		series[index].OutputTokens += usage.OutputTokens
	}
	addTotals := func(usage core.Usage) {
		summary.InputTokens += usage.InputTokens
		summary.CachedInputTokens += usage.CachedInputTokens
		summary.OutputTokens += usage.OutputTokens
	}
	addSourceTokens := func(row *SourceRow, usage core.Usage) {
		row.InputTokens += usage.InputTokens
		row.CachedInputTokens += usage.CachedInputTokens
		row.OutputTokens += usage.OutputTokens
	}
	agentRow := func(name string) *AgentRow {
		row, ok := agentRows[name]
		if !ok {
			row = &AgentRow{Agent: name}
			agentRows[name] = row
		}
		return row
	}
	toolAcc := func(name string) *toolRowAcc {
		acc, ok := tools[name]
		if !ok {
			acc = &toolRowAcc{name: name}
			tools[name] = acc
		}
		return acc
	}

	for _, event := range events {
		// REQ-DSH-011: an agent filter narrows every section to that agent's
		// sessions.
		if agent != "" && event.Agent != agent {
			continue
		}
		switch event.Kind {
		case core.KindStepEnd:
			var payload core.StepEnd
			if !decode(event.Payload, &payload) {
				continue
			}
			addTotals(payload.Usage)
			addSeries(event.CreatedAt, payload.Usage)
			row := sources["turn"]
			row.Count++
			addSourceTokens(row, payload.Usage)
			key := [2]string{payload.Provider, payload.Model}
			model, ok := modelRows[key]
			if !ok {
				model = &ModelRow{Provider: payload.Provider, Model: payload.Model}
				modelRows[key] = model
			}
			model.Requests++
			model.InputTokens += payload.Usage.InputTokens
			model.CachedInputTokens += payload.Usage.CachedInputTokens
			model.OutputTokens += payload.Usage.OutputTokens
			agent := agentRow(event.Agent)
			agent.InputTokens += payload.Usage.InputTokens
			agent.CachedInputTokens += payload.Usage.CachedInputTokens
			agent.OutputTokens += payload.Usage.OutputTokens
		case core.KindCompactionSummary:
			var payload core.CompactionSummary
			if !decode(event.Payload, &payload) {
				continue
			}
			addTotals(payload.Usage)
			addSeries(event.CreatedAt, payload.Usage)
			row := sources["compaction"]
			row.Count++
			addSourceTokens(row, payload.Usage)
		case core.KindMemoryCaptured:
			var payload core.MemoryCaptured
			if !decode(event.Payload, &payload) {
				continue
			}
			addTotals(payload.Usage)
			addSeries(event.CreatedAt, payload.Usage)
			row := sources["memory_capture"]
			row.Count++
			addSourceTokens(row, payload.Usage)
		case core.KindTurnStart:
			var payload core.TurnStart
			if !decode(event.Payload, &payload) {
				continue
			}
			turnStarts[eventKey(event.SessionID, turnID(event.TurnID, payload.TurnID))] = event.CreatedAt
		case core.KindTurnEnd:
			var payload core.TurnEnd
			if !decode(event.Payload, &payload) {
				continue
			}
			summary.Turns++
			agentRow(event.Agent).Turns++
			reason := payload.Reason
			if reason == "" {
				reason = "end_turn"
			}
			reasons[reason]++
			if reason == "cancelled" {
				summary.TurnsCancelled++
			}
			id := eventKey(event.SessionID, turnID(event.TurnID, payload.TurnID))
			if turnFailed(payload) {
				summary.TurnsFailed++
				errorRows = append(errorRows, ErrorRow{
					At:        event.CreatedAt,
					Source:    "turn",
					Level:     "error",
					Agent:     event.Agent,
					SessionID: event.SessionID,
					TurnID:    turnID(event.TurnID, payload.TurnID),
					Message:   turnErrorMessage(payload),
				})
			}
			if start, ok := turnStarts[id]; ok && event.CreatedAt >= start {
				turnDurations = append(turnDurations, event.CreatedAt-start)
			}
		case core.KindToolCall:
			var payload core.ToolCallPayload
			if !decode(event.Payload, &payload) {
				continue
			}
			toolAcc(payload.Name).calls++
			summary.ToolCalls++
			toolStarts[eventKey(event.SessionID, payload.CallID)] = toolStart{name: payload.Name, at: event.CreatedAt}
		case core.KindToolResult:
			var payload core.ToolResultPayload
			if !decode(event.Payload, &payload) {
				continue
			}
			start, ok := toolStarts[eventKey(event.SessionID, payload.CallID)]
			if !ok {
				continue
			}
			acc := toolAcc(start.name)
			if event.CreatedAt >= start.at {
				acc.durations = append(acc.durations, event.CreatedAt-start.at)
			}
			switch payload.Status {
			case core.ToolStatusError:
				acc.errors++
				summary.ToolErrors++
				if event.CreatedAt >= acc.lastErrorAt {
					acc.lastError = cutRunes(payload.Output, lastErrorMax)
					acc.lastErrorAt = event.CreatedAt
				}
				errorRows = append(errorRows, ErrorRow{
					At:        event.CreatedAt,
					Source:    "tool",
					Level:     "error",
					Agent:     event.Agent,
					SessionID: event.SessionID,
					TurnID:    event.TurnID,
					Tool:      start.name,
					Message:   cutRunes(toolErrorMessage(start.name, payload.Output), toolMessageMax),
				})
			case core.ToolStatusRejected:
				acc.rejected++
				summary.ToolRejected++
			}
		case core.KindAssistantAttempt:
			var payload core.AssistantAttempt
			if !decode(event.Payload, &payload) {
				continue
			}
			// A cancelled attempt marks a user cancel, not a provider retry:
			// it counts in neither the retry total nor the error log
			// (REQ-DSH-003, REQ-DSH-008).
			if payload.Error.Class == "cancelled" {
				continue
			}
			summary.ProviderRetries++
			errorRows = append(errorRows, ErrorRow{
				At:        event.CreatedAt,
				Source:    "provider",
				Level:     "error",
				Agent:     event.Agent,
				SessionID: event.SessionID,
				TurnID:    event.TurnID,
				Message:   providerErrorMessage(payload.Error),
			})
		}
	}

	// Daemon log records have no agent, so an agent filter drops them
	// (REQ-DSH-011, API-DSH-001 r2).
	if agent == "" {
		for _, record := range logs {
			message := record.Message
			if record.Attrs != "" {
				message += " " + record.Attrs
			}
			level := "warn"
			if record.Level >= slog.LevelError {
				level = "error"
			}
			errorRows = append(errorRows, ErrorRow{At: record.At, Source: "daemon", Level: level, Message: message})
		}
	}

	d.Summary = summary
	d.ByModel = modelRowsSorted(modelRows)
	d.ByAgent = agentRowsSorted(agentRows)
	d.BySource = []SourceRow{*sources["turn"], *sources["compaction"], *sources["memory_capture"]}
	d.Tools = toolRowsSorted(tools)
	d.TurnReasons = reasonRowsSorted(reasons)
	d.TurnDuration = Duration{AvgMS: avgMS(turnDurations), P95MS: p95MS(turnDurations)}
	d.Errors = errorRowsSorted(errorRows)
	return d
}

// toolRowAcc accumulates one tool's results while the events are folded.
type toolRowAcc struct {
	name        string
	calls       int64
	errors      int64
	rejected    int64
	durations   []int64
	lastError   string
	lastErrorAt int64
}

// toolStart is the point a tool/call was seen, used to time its result and to
// name it.
type toolStart struct {
	name string
	at   int64
}

// newSeries builds the zero-filled bucket list and returns the first bucket
// start. Buckets align to BucketMS in UTC and run up to the bucket holding To
// (API-DSH-001).
func newSeries(r Range) ([]SeriesPoint, int64) {
	if r.BucketMS <= 0 {
		return []SeriesPoint{}, 0
	}
	first := bucketStart(r.From, r.BucketMS)
	if first > r.To {
		return []SeriesPoint{}, first
	}
	series := make([]SeriesPoint, 0, (r.To-first)/r.BucketMS+1)
	for start := first; start <= r.To; start += r.BucketMS {
		series = append(series, SeriesPoint{Start: start})
	}
	return series, first
}

// bucketStart floors a Unix-millisecond time to the bucket size.
func bucketStart(at, bucketMS int64) int64 {
	return at / bucketMS * bucketMS
}

func eventKey(sessionID, id string) string {
	return sessionID + "\x00" + id
}

// turnID prefers the event's turn id and falls back to the payload's.
func turnID(eventID, payloadID string) string {
	if eventID != "" {
		return eventID
	}
	return payloadID
}

func turnFailed(end core.TurnEnd) bool {
	if end.Error != nil {
		return true
	}
	switch end.Reason {
	case "error", "max_steps", "loop", "interrupted":
		return true
	}
	return false
}

func turnErrorMessage(end core.TurnEnd) string {
	if end.Error != nil {
		return joinParts(end.Error.Class, end.Error.Message)
	}
	if end.Reason == "" {
		return "end_turn"
	}
	return end.Reason
}

func providerErrorMessage(providerError core.PayloadError) string {
	status := ""
	if providerError.Status != 0 {
		status = strconv.Itoa(providerError.Status)
	}
	return joinParts(providerError.Class, status, providerError.Message)
}

func toolErrorMessage(name, output string) string {
	if output == "" {
		return name
	}
	return name + ": " + output
}

// joinParts joins the non-empty parts with ": ".
func joinParts(parts ...string) string {
	nonEmpty := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			nonEmpty = append(nonEmpty, part)
		}
	}
	return strings.Join(nonEmpty, ": ")
}

func modelRowsSorted(rows map[[2]string]*ModelRow) []ModelRow {
	out := make([]ModelRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Requests != out[j].Requests {
			return out[i].Requests > out[j].Requests
		}
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Model < out[j].Model
	})
	return out
}

// agentNames returns the sorted unique agents seen in the events, ignoring
// empty names. It runs before the agent filter (REQ-DSH-011).
func agentNames(events []core.ScannedEvent) []string {
	names := map[string]struct{}{}
	for _, event := range events {
		if event.Agent != "" {
			names[event.Agent] = struct{}{}
		}
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func agentRowsSorted(rows map[string]*AgentRow) []AgentRow {
	out := make([]AgentRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Turns != out[j].Turns {
			return out[i].Turns > out[j].Turns
		}
		return out[i].Agent < out[j].Agent
	})
	return out
}

func toolRowsSorted(rows map[string]*toolRowAcc) []ToolRow {
	out := make([]ToolRow, 0, len(rows))
	for _, acc := range rows {
		out = append(out, ToolRow{
			Name:        acc.name,
			Calls:       acc.calls,
			Errors:      acc.errors,
			Rejected:    acc.rejected,
			AvgMS:       avgMS(acc.durations),
			P95MS:       p95MS(acc.durations),
			LastError:   acc.lastError,
			LastErrorAt: acc.lastErrorAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// reasonRowsSorted returns the reason counts, most frequent first. An empty
// reason is reported as end_turn (API-DSH-001).
func reasonRowsSorted(reasons map[string]int64) []ReasonRow {
	out := make([]ReasonRow, 0, len(reasons))
	for reason, count := range reasons {
		out = append(out, ReasonRow{Reason: reason, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}

// errorRowsSorted returns the newest errorRowLimit rows, newest first.
func errorRowsSorted(rows []ErrorRow) []ErrorRow {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].At > rows[j].At })
	if len(rows) > errorRowLimit {
		rows = rows[:errorRowLimit]
	}
	if rows == nil {
		return []ErrorRow{}
	}
	return rows
}

// avgMS is the rounded mean of the samples, or 0 with no samples.
func avgMS(samples []int64) int64 {
	if len(samples) == 0 {
		return 0
	}
	var sum int64
	for _, sample := range samples {
		sum += sample
	}
	return int64(math.Round(float64(sum) / float64(len(samples))))
}

// p95MS is the nearest-rank 95th percentile of the samples, or 0 with no
// samples (API-DSH-001).
func p95MS(samples []int64) int64 {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]int64(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := (95*len(sorted) + 99) / 100
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

// cutRunes keeps at most n leading characters of s.
func cutRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

func decode(payload json.RawMessage, dst any) bool {
	return json.Unmarshal(payload, dst) == nil
}
