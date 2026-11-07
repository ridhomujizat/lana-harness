package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/stats"
)

// The usage_stats agent tool gives an agent a compact view of its own token
// use, tool activity, and recent errors (self-observability).

// UsageStatsSource is the dashboard source of the usage_stats tool.
type UsageStatsSource interface {
	Dashboard(ctx context.Context, rangeName, agent string) (stats.Dashboard, error)
}

const (
	usageStatsDefaultRange = "24h"
	usageStatsAllAgents    = "all"
	usageStatsToolRows     = 10
	usageStatsErrorRows    = 10
	usageStatsMessageMax   = 300
	// usageStatsTimeLayout is local RFC3339 at minute precision.
	usageStatsTimeLayout = "2006-01-02T15:04Z07:00"
)

var usageStatsSpec = json.RawMessage(`{"type":"object","properties":{` +
	`"range":{"type":"string","enum":["24h","7d","30d"],"description":"Time range. Default 24h."},` +
	`"agent":{"type":"string","description":"Agent to report. Defaults to the calling agent; all means every agent."}` +
	`},"additionalProperties":false}`)

type usageStatsTool struct{ src UsageStatsSource }

// NewUsageStats creates the usage_stats tool over src.
func NewUsageStats(src UsageStatsSource) core.Tool { return &usageStatsTool{src: src} }

func (t *usageStatsTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        "usage_stats",
		Description: "Show your token use, tool activity, and recent errors for a time range. Defaults to the last 24 hours and the calling agent; use all for every agent.",
		InputSchema: usageStatsSpec,
	}
}

// Risk is read: the tool only reads the dashboard aggregation.
func (t *usageStatsTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }

func (t *usageStatsTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var in struct {
		Range string `json:"range"`
		Agent string `json:"agent"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	rangeName := strings.TrimSpace(in.Range)
	if rangeName == "" {
		rangeName = usageStatsDefaultRange
	}
	if !stats.ValidRange(rangeName) {
		return inputErrorOutput(fmt.Errorf("invalid range %q: %w", rangeName, stats.ErrInvalidRange))
	}
	agent := strings.TrimSpace(in.Agent)
	switch agent {
	case "":
		agent = tc.AgentName
	case usageStatsAllAgents:
		agent = ""
	}
	if t.src == nil {
		return errorOutput("usage stats are not available")
	}
	dashboard, err := t.src.Dashboard(ctx, rangeName, agent)
	if err != nil {
		return errorOutput(err.Error())
	}
	return successOutput(formatUsageStats(dashboard, rangeName, agent))
}

// formatUsageStats renders the compact text result: a header, the summary and
// tokens, the model rows, the top tools, and the most recent errors.
func formatUsageStats(dashboard stats.Dashboard, rangeName, agent string) string {
	agentLabel := agent
	if agentLabel == "" {
		agentLabel = usageStatsAllAgents
	}
	var b strings.Builder
	fmt.Fprintf(&b, "usage_stats range=%s agent=%s\n", rangeName, agentLabel)

	// The stats payload has no cost field, so the summary carries the counters
	// and token totals it does have.
	summary := dashboard.Summary
	fmt.Fprintf(&b, "summary: turns=%d turns_failed=%d turns_cancelled=%d tool_calls=%d tool_errors=%d tool_rejected=%d provider_retries=%d\n",
		summary.Turns, summary.TurnsFailed, summary.TurnsCancelled,
		summary.ToolCalls, summary.ToolErrors, summary.ToolRejected, summary.ProviderRetries)
	fmt.Fprintf(&b, "tokens: input_tokens=%d cached_input_tokens=%d output_tokens=%d\n",
		summary.InputTokens, summary.CachedInputTokens, summary.OutputTokens)

	if len(dashboard.ByModel) > 0 {
		b.WriteString("models:\n")
		for _, row := range dashboard.ByModel {
			fmt.Fprintf(&b, "  %s: turns=%d input_tokens=%d cached_input_tokens=%d output_tokens=%d\n",
				usageStatsModelName(row), row.Requests, row.InputTokens, row.CachedInputTokens, row.OutputTokens)
		}
	}

	if tools := usageStatsTopTools(dashboard.Tools); len(tools) > 0 {
		b.WriteString("tools:\n")
		for _, row := range tools {
			fmt.Fprintf(&b, "  %s: calls=%d errors=%d\n", row.Name, row.Calls, row.Errors)
		}
	}

	b.WriteString("errors (last 10):\n")
	errors := usageStatsRecentErrors(dashboard.Errors)
	if len(errors) == 0 {
		b.WriteString("  none\n")
	}
	for _, row := range errors {
		source := row.Source
		if source == "" {
			source = "unknown"
		}
		kind := row.Level
		if kind == "" {
			kind = "error"
		}
		fmt.Fprintf(&b, "  %s [%s/%s] %s\n",
			time.UnixMilli(row.At).Local().Format(usageStatsTimeLayout), source, kind,
			usageStatsMessage(row.Message))
	}
	return b.String()
}

// usageStatsModelName renders provider/model, or whichever part is present.
func usageStatsModelName(row stats.ModelRow) string {
	switch {
	case row.Provider == "":
		return row.Model
	case row.Model == "":
		return row.Provider
	default:
		return row.Provider + "/" + row.Model
	}
}

// usageStatsTopTools returns the ten tools with the most calls, ties in name
// order.
func usageStatsTopTools(rows []stats.ToolRow) []stats.ToolRow {
	sorted := append([]stats.ToolRow(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Calls != sorted[j].Calls {
			return sorted[i].Calls > sorted[j].Calls
		}
		return sorted[i].Name < sorted[j].Name
	})
	if len(sorted) > usageStatsToolRows {
		sorted = sorted[:usageStatsToolRows]
	}
	return sorted
}

// usageStatsRecentErrors returns the ten newest errors.
func usageStatsRecentErrors(rows []stats.ErrorRow) []stats.ErrorRow {
	sorted := append([]stats.ErrorRow(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At > sorted[j].At })
	if len(sorted) > usageStatsErrorRows {
		sorted = sorted[:usageStatsErrorRows]
	}
	return sorted
}

// usageStatsMessage flattens one message to a line and cuts it to 300 runes.
func usageStatsMessage(message string) string {
	flat := strings.Join(strings.Fields(message), " ")
	runes := []rune(flat)
	if len(runes) <= usageStatsMessageMax {
		return flat
	}
	return string(runes[:usageStatsMessageMax])
}
