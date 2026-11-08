package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// TestRealUsageProbe runs the probe against the installed claude for the
// built-in account. It is skipped unless LANA_REAL_CLAUDE_USAGE=1: it uses
// the owner's real login (no model call; /usage is a local command).
func TestRealUsageProbe(t *testing.T) {
	if os.Getenv("LANA_REAL_CLAUDE_USAGE") != "1" {
		t.Skip("set LANA_REAL_CLAUDE_USAGE=1 to probe the installed claude")
	}
	r := New(core.RuntimeDeps{}, Options{})
	defer func() { _ = r.Shutdown(context.Background()) }()
	bin, err := r.accountBinary(core.RuntimeAccount{Name: core.ClaudeCodeProvider})
	if err != nil {
		t.Fatal(err)
	}
	env, _, err := buildEnv(r.opts.Environ(), core.RuntimeConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	out, _, err := r.runUsage(context.Background(), bin, env, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range bytes.Split(out, []byte("\n")) {
		var ev struct {
			Type    string          `json:"type"`
			IsError bool            `json:"is_error"`
			Turns   int             `json:"num_turns"`
			Usage   json.RawMessage `json:"usage"`
		}
		if json.Unmarshal(line, &ev) == nil && ev.Type == "result" {
			var u struct {
				In  int64 `json:"input_tokens"`
				Out int64 `json:"output_tokens"`
			}
			_ = json.Unmarshal(ev.Usage, &u)
			t.Logf("result: is_error=%v num_turns=%d input_tokens=%d output_tokens=%d", ev.IsError, ev.Turns, u.In, u.Out)
		}
		if bytes.Contains(line, []byte(`"hook_`)) {
			t.Errorf("a hook ran: %.120s", line)
		}
	}
	text, err := usageResult(out)
	if err != nil {
		t.Fatal(err)
	}
	report, err := parseUsageText(text, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("subscription=%v", report.Subscription)
	for _, w := range report.Windows {
		t.Logf("window type=%s label=%q status=%s utilization=%.2f resets_at=%s", w.Type, w.Label, w.Status, *w.Utilization, w.ResetsAt.Format("2006-01-02T15:04:05Z07:00"))
	}
}
