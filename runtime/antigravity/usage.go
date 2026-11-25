package antigravity

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

const (
	// usageProbeTimeout bounds one `agy -p /usage` probe (API-AGY-012).
	usageProbeTimeout = 45 * time.Second
	// unknownReset is the reset time agy reports for a window that has not
	// started: lana stores no reset time for it.
	unknownReset = "1970-01-01T00:00:00Z"
)

// usageArgs is the argv of the usage probe after the binary (API-AGY-012).
var usageArgs = []string{"-p", "/usage", "--output-format", "json", "--print-timeout", "30s"}

// UsageProbe runs `agy -p /usage --output-format json` for the account and
// returns its plan windows (REQ-AGY-009, API-AGY-012). /usage is a local
// command: it makes no model call, and lana treats any input or output token
// in the answer as a model call and fails the probe. The probe runs with the
// account's HOME, stdin closed, in a new empty folder that is removed
// afterwards, and never reads a credential.
func (r *Runtime) UsageProbe(ctx context.Context, account core.RuntimeAccount) (core.RuntimeUsageReport, error) {
	bin, err := r.resolveBinary(account.Binary)
	if err != nil {
		return core.RuntimeUsageReport{}, err
	}
	env := buildEnv(r.opts.Environ(), core.RuntimeConfig{Home: account.Home})
	dir, err := os.MkdirTemp("", "lana-usage-")
	if err != nil {
		return core.RuntimeUsageReport{}, fmt.Errorf("usage probe folder: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	out, _, err := r.runProbe(ctx, bin, usageArgs, env, dir, usageProbeTimeout)
	if err != nil {
		return core.RuntimeUsageReport{}, err
	}
	return parseUsage(out)
}

var _ core.RuntimeUsageProber = (*Runtime)(nil)

// usageJSON is the part of the /usage answer lana reads (API-AGY-012).
type usageJSON struct {
	Status string `json:"status"`
	Usage  struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
	Command struct {
		Data struct {
			Groups []struct {
				Name    string `json:"name"`
				Buckets []struct {
					Window            string  `json:"window"`
					RemainingFraction float64 `json:"remaining_fraction"`
					ResetTime         string  `json:"reset_time"`
				} `json:"buckets"`
			} `json:"groups"`
		} `json:"data"`
	} `json:"command"`
}

// parseUsage reads the /usage JSON and builds the account's plan windows: one
// per bucket, "<group>/<window>". A reset time of unknownReset (or none) is
// stored as no reset time. It fails on a missing or unreadable answer, a
// status other than SUCCESS, and an answer that reports input or output tokens
// (a model call).
func parseUsage(out string) (core.RuntimeUsageReport, error) {
	trimmed := strings.TrimSpace(out)
	start := strings.IndexByte(trimmed, '{')
	if start < 0 {
		return core.RuntimeUsageReport{}, userError("Antigravity /usage gave no JSON")
	}
	var u usageJSON
	if err := json.Unmarshal([]byte(trimmed[start:]), &u); err != nil {
		return core.RuntimeUsageReport{}, userError("Antigravity /usage answered in an unknown form: %s", truncate(detailLine(trimmed), 120))
	}
	if u.Status != "SUCCESS" {
		status := u.Status
		if status == "" {
			status = "none"
		}
		return core.RuntimeUsageReport{}, userError("Antigravity /usage failed: status %s", status)
	}
	if u.Usage.InputTokens > 0 || u.Usage.OutputTokens > 0 {
		return core.RuntimeUsageReport{}, userError(
			"Antigravity /usage made a model call (%d input, %d output tokens); the refresh is treated as failed",
			u.Usage.InputTokens, u.Usage.OutputTokens)
	}
	report := core.RuntimeUsageReport{Subscription: true}
	for _, g := range u.Command.Data.Groups {
		for _, b := range g.Buckets {
			window := core.RuntimeLimitWindow{
				Type:   g.Name + "/" + b.Window,
				Label:  usageLabel(g.Name, b.Window),
				Status: "allowed",
			}
			util := 1 - b.RemainingFraction
			if util < 0 {
				util = 0
			}
			window.Utilization = &util
			if util >= 1 {
				window.Status = "rejected"
			}
			if at, ok := parseReset(b.ResetTime); ok {
				window.ResetsAt = at
			}
			report.Windows = append(report.Windows, window)
		}
	}
	return report, nil
}

// usageLabel is the UI label of a window: "<group> · 5 hours" or
// "<group> · weekly".
func usageLabel(group, window string) string {
	switch window {
	case "weekly":
		return group + " · weekly"
	default:
		return group + " · 5 hours"
	}
}

// parseReset reads a bucket's reset_time: unknownReset and unparseable values
// mean the window has not started, so no reset time is stored.
func parseReset(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" || value == unknownReset {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false
	}
	return at.UTC(), true
}
