package antigravity

import (
	"context"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// setModelOf returns the modelId of the turn's session/set_model, or "".
func (h *harness) setModelOf() string {
	calls := h.stdinOf("session/set_model")
	if len(calls) == 0 {
		return ""
	}
	params, _ := calls[0]["params"].(map[string]any)
	id, _ := params["modelId"].(string)
	return id
}

// AC-MES-001, AC-MES-002: a base model with an effort runs as the variant
// agy lists for it; an effort the base does not list uses its default
// variant (gpt-oss-120b lists only medium).
func TestEffortPicksVariant(t *testing.T) {
	cases := []struct {
		model, effort, want string
	}{
		{"gemini-4-neon", "xhigh", "gemini-4-neon-xhigh"},   // AC-MES-001
		{"gpt-oss-120b", "xhigh", "gpt-oss-120b-medium"},    // AC-MES-002
		{"gemini-4-neon", "", "gemini-4-neon-high"},         // default: high
		{"gemini-4-neon", "minimal", "gemini-4-neon-high"},  // not listed: default
		{"gemini-4-neon-low", "xhigh", "gemini-4-neon-low"}, // full variant wins
		{"gemini-pro-orchestrator", "high", "gemini-pro-orchestrator"},
		{"some-new-model", "high", "some-new-model"},
	}
	for _, tc := range cases {
		t.Run(tc.model+"/"+tc.effort, func(t *testing.T) {
			h := newHarness(t, "acp_turn1.jsonl", Options{})
			turn := h.turn("s1")
			turn.Config.Model = tc.model
			turn.Config.Effort = tc.effort
			if _, err := h.run(context.Background(), turn); err != nil {
				t.Fatal(err)
			}
			if got := h.setModelOf(); got != tc.want {
				t.Fatalf("set_model = %q, want %q", got, tc.want)
			}
		})
	}
}

// The variant comes from the cached `agy models` list once it is read: the
// second turn of an account does not run `agy models` again.
func TestEffortVariantUsesCachedList(t *testing.T) {
	h := newHarness(t, "acp_turn1.jsonl", Options{})
	for _, sid := range []string{"s1", "s2"} {
		turn := h.turn(sid)
		turn.Config.Model = "gemini-4-neon"
		turn.Config.Effort = "low"
		if _, err := h.run(context.Background(), turn); err != nil {
			t.Fatal(err)
		}
	}
	probes := 0
	for _, s := range h.starts() {
		if len(s.Argv) == 1 && s.Argv[0] == "models" {
			probes++
		}
	}
	if probes != 1 {
		t.Fatalf("agy models ran %d times, want 1", probes)
	}
}

func TestPickVariant(t *testing.T) {
	list := []core.RuntimeModel{
		{Value: "gemini-4-neon-high"}, {Value: "gemini-4-neon-xhigh"}, {Value: "gemini-4-neon-medium"}, {Value: "gemini-4-neon-low"},
		{Value: "gemini-3.8-flash-medium"}, {Value: "gemini-3.8-flash-low"},
		{Value: "gemini-pro-orchestrator"},
	}
	cases := []struct{ base, effort, want string }{
		{"gemini-4-neon", "medium", "gemini-4-neon-medium"},
		{"gemini-4-neon", "max", "gemini-4-neon-high"},
		{"gemini-3.8-flash", "", "gemini-3.8-flash-low"}, // no high: the first
		{"gemini-pro-orchestrator", "low", "gemini-pro-orchestrator"},
		{"unknown", "low", "unknown"},
	}
	for _, tc := range cases {
		if got := pickVariant(tc.base, tc.effort, list); got != tc.want {
			t.Errorf("pickVariant(%q, %q) = %q, want %q", tc.base, tc.effort, got, tc.want)
		}
	}
	if got := pickVariant("gemini-4-neon", "low", nil); got != "gemini-4-neon" {
		t.Fatalf("no list = %q", got)
	}
}
