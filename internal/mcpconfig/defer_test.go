package mcpconfig

import (
	"strings"
	"testing"
)

// API-DTL-002: defer survives a Render/FromMap round trip, so a rewrite of the
// [mcp.<name>] table never drops it, and a nil value stays out of the table.
func TestRenderFromMapDeferRoundTrip(t *testing.T) {
	def := Definition{Type: "command", Command: "npx", Defer: boolPtr(false)}
	table := Render("docs", def)
	if !strings.Contains(table, "defer = false\n") {
		t.Fatalf("Render dropped defer:\n%s", table)
	}
	doc, err := Parse([]byte(table))
	if err != nil {
		t.Fatal(err)
	}
	got := FromMap(doc.MCP["docs"])
	if got.Defer == nil || *got.Defer {
		t.Fatalf("FromMap defer = %v", got.Defer)
	}
	if plain := Render("docs", Definition{Type: "command", Command: "npx"}); strings.Contains(plain, "defer") {
		t.Fatalf("Render wrote defer for a nil value:\n%s", plain)
	}
}

func boolPtr(v bool) *bool { return &v }
