package lana

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// API-RTR-013: a plan window Claude Code reports without a utilization stays
// nil (JSON null, "usage not reported"); a reported one is copied, not
// aliased.
func TestExternalWindowsUtilization(t *testing.T) {
	util := 0.42
	resets := time.Unix(1794088200, 0)
	in := []core.RuntimeLimitWindow{
		{Type: "five_hour", Status: "allowed", Utilization: &util, ResetsAt: resets},
		{Type: "seven_day", Status: "allowed"},
	}
	out := externalWindows(in)
	if len(out) != 2 || out[0].Utilization == nil || *out[0].Utilization != 0.42 || out[0].ResetsAt == nil || !out[0].ResetsAt.Equal(resets) {
		t.Fatalf("windows = %+v", out)
	}
	if out[0].Utilization == in[0].Utilization {
		t.Fatal("utilization aliases the runtime event")
	}
	if out[1].Utilization != nil || out[1].ResetsAt != nil {
		t.Fatalf("unreported window = %+v", out[1])
	}
	data, err := json.Marshal(out[1])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"utilization":null`) {
		t.Fatalf("unreported window JSON = %s", data)
	}
}
