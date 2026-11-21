package web

import (
	"strconv"
	"strings"
	"testing"
)

// API-DTL-001: the agent editor keeps an owner-set defer_tools through a
// record round trip, so a dashboard save never drops it.
func TestAgentRecordKeepsDeferTools(t *testing.T) {
	for _, value := range []bool{true, false} {
		record := agentRecordFromMap(map[string]any{"defer_tools": value, "model": "m"})
		if record.DeferTools == nil || *record.DeferTools != value {
			t.Fatalf("record.DeferTools = %v, want %v", record.DeferTools, value)
		}
		want := "defer_tools = " + strconv.FormatBool(value)
		if table := renderAgentTable("coder", record); !strings.Contains(table, want) {
			t.Fatalf("rendered table lacks %q:\n%s", want, table)
		}
	}
	// An absent value stays absent.
	if table := renderAgentTable("coder", agentRecordFromMap(map[string]any{"model": "m"})); strings.Contains(table, "defer_tools") {
		t.Fatalf("rendered table has defer_tools with no value:\n%s", table)
	}
}
