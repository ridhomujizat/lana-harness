package agent

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

func alwaysDeferrable(string) bool { return true }

func mcpToolNames(server string, n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("mcp__%s__t%02d", server, i))
	}
	return out
}

func sortedNames(names ...string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}

// AC-DTL-002: with defer_tools absent, a count of 15 or less is sent in full
// (REQ-DTL-001).
func TestPlanDeferralThresholdAtBoundary(t *testing.T) {
	offered := sortedNames(append([]string{"read_file"}, mcpToolNames("srv", 15)...)...)
	base, deferrable := planDeferral(offered, nil, alwaysDeferrable, true)
	if !reflect.DeepEqual(base, offered) {
		t.Fatalf("base=%v want the offered list %v", base, offered)
	}
	if deferrable != nil {
		t.Fatalf("deferrable=%v want nil", deferrable)
	}
}

// AC-DTL-001: one more than the threshold defers every MCP schema and offers
// tool_search (REQ-DTL-001, REQ-DTL-004).
func TestPlanDeferralAboveThreshold(t *testing.T) {
	offered := sortedNames(append([]string{"read_file"}, mcpToolNames("srv", 16)...)...)
	base, deferrable := planDeferral(offered, nil, alwaysDeferrable, true)
	if want := []string{"read_file", "tool_search"}; !reflect.DeepEqual(base, want) {
		t.Fatalf("base=%v want %v", base, want)
	}
	if want := mcpToolNames("srv", 16); !reflect.DeepEqual(deferrable, want) {
		t.Fatalf("deferrable=%v want %v", deferrable, want)
	}
}

// REQ-DTL-001: defer_tools true defers a single tool; false never defers.
func TestPlanDeferralExplicit(t *testing.T) {
	on, off := true, false
	offered := []string{"read_file", "mcp__srv__a"}

	base, deferrable := planDeferral(offered, &on, alwaysDeferrable, true)
	if want := []string{"read_file", "tool_search"}; !reflect.DeepEqual(base, want) {
		t.Fatalf("true base=%v want %v", base, want)
	}
	if want := []string{"mcp__srv__a"}; !reflect.DeepEqual(deferrable, want) {
		t.Fatalf("true deferrable=%v want %v", deferrable, want)
	}

	base, deferrable = planDeferral(offered, &off, alwaysDeferrable, true)
	if !reflect.DeepEqual(base, offered) || deferrable != nil {
		t.Fatalf("false base=%v deferrable=%v want %v nil", base, deferrable, offered)
	}
}

// AC-DTL-003: a server with defer = false is sent in full and not counted
// (REQ-DTL-001).
func TestPlanDeferralServerOptOut(t *testing.T) {
	offered := sortedNames(append(append([]string{"read_file"}, mcpToolNames("on", 16)...), mcpToolNames("off", 3)...)...)
	serverDeferrable := func(server string) bool { return server != "off" }
	base, deferrable := planDeferral(offered, nil, serverDeferrable, true)
	if want := mcpToolNames("on", 16); !reflect.DeepEqual(deferrable, want) {
		t.Fatalf("deferrable=%v want %v", deferrable, want)
	}
	wantBase := sortedNames(append([]string{"read_file", "tool_search"}, mcpToolNames("off", 3)...)...)
	if !reflect.DeepEqual(base, wantBase) {
		t.Fatalf("base=%v want %v", base, wantBase)
	}
}

// REQ-DTL-004: tool_search joins base only when it may be offered; deferral off
// and a nil toolSearchOK leave the offered list unchanged.
func TestPlanDeferralToolSearch(t *testing.T) {
	on := true
	offered := sortedNames(append([]string{"read_file"}, mcpToolNames("srv", 4)...)...)

	base, deferrable := planDeferral(offered, &on, alwaysDeferrable, false)
	if want := []string{"read_file"}; !reflect.DeepEqual(base, want) {
		t.Fatalf("base=%v want %v", base, want)
	}
	if len(deferrable) != 4 {
		t.Fatalf("deferrable=%v want the four MCP tools", deferrable)
	}

	// AC-DTL-011: no deferrable tool means no tool_search and an unchanged list.
	base, deferrable = planDeferral([]string{"read_file", "bash"}, nil, alwaysDeferrable, true)
	if want := []string{"read_file", "bash"}; !reflect.DeepEqual(base, want) || deferrable != nil {
		t.Fatalf("base=%v deferrable=%v want %v nil", base, deferrable, want)
	}
}

func loadedEvent(names ...string) core.SessionEvent {
	raw, _ := json.Marshal(core.ToolsLoaded{V: core.PayloadVersion, Names: names})
	return core.SessionEvent{Kind: core.KindToolsLoaded, Payload: raw}
}

func compactionSummaryEvent() core.SessionEvent {
	raw, _ := json.Marshal(core.CompactionSummary{V: core.PayloadVersion, Summary: "sum"})
	return core.SessionEvent{Kind: core.KindCompactionSummary, Payload: raw}
}

// REQ-DTL-006: the loaded set keeps event order, drops duplicates, non-deferred
// names, and unreadable payloads, and restarts after the latest compaction.
func TestLoadedFromEvents(t *testing.T) {
	deferrable := []string{"mcp__srv__a", "mcp__srv__b", "mcp__srv__c"}

	t.Run("order and dedupe", func(t *testing.T) {
		events := []core.SessionEvent{
			loadedEvent("mcp__srv__b", "mcp__srv__a"),
			loadedEvent("mcp__srv__a"),
			loadedEvent("mcp__srv__c"),
		}
		got := loadedFromEvents(events, deferrable)
		if want := []string{"mcp__srv__b", "mcp__srv__a", "mcp__srv__c"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("loaded=%v want %v", got, want)
		}
	})

	t.Run("non-deferrable and unreadable dropped", func(t *testing.T) {
		events := []core.SessionEvent{
			loadedEvent("read_file", "mcp__srv__a"),
			{Kind: core.KindToolsLoaded, Payload: json.RawMessage("not json")},
		}
		got := loadedFromEvents(events, deferrable)
		if want := []string{"mcp__srv__a"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("loaded=%v want %v", got, want)
		}
	})

	t.Run("compaction resets", func(t *testing.T) {
		events := []core.SessionEvent{
			loadedEvent("mcp__srv__a"),
			compactionSummaryEvent(),
			loadedEvent("mcp__srv__b"),
		}
		got := loadedFromEvents(events, deferrable)
		if want := []string{"mcp__srv__b"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("loaded=%v want %v", got, want)
		}
	})

	t.Run("no deferrable set", func(t *testing.T) {
		if got := loadedFromEvents([]core.SessionEvent{loadedEvent("mcp__srv__a")}, nil); got != nil {
			t.Fatalf("loaded=%v want nil", got)
		}
	})
}
