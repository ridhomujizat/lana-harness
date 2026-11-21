package tools

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// searchSpecs is a fixed deferred tool set: a unique bare name
// (create_ticket), an ambiguous one (ping on two servers), and a couple of
// distinct servers.
func searchSpecs() []core.ToolSpec {
	return []core.ToolSpec{
		{Name: "mcp__kerjan__create_ticket", Description: "Create a support ticket.", InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`)},
		{Name: "mcp__kerjan__list_tickets", Description: "List open tickets.", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "mcp__kerjan__ping", Description: "Ping the kerjan server.", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "mcp__dbx__dbx_list_tables", Description: "List database tables.", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "mcp__dbx__ping", Description: "Ping the dbx server.", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}
}

func callToolSearch(t *testing.T, tc core.ToolCtx, input string) core.ToolOutput {
	t.Helper()
	return callToolCtx(t, NewToolSearch(), tc, input)
}

func TestToolSearchSelectFullName(t *testing.T) {
	out := callToolSearch(t, core.ToolCtx{DeferredTools: searchSpecs()}, `{"query":"select:mcp__kerjan__create_ticket"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("status = %q", out.Status)
	}
	if !reflect.DeepEqual(out.Loaded, []string{"mcp__kerjan__create_ticket"}) {
		t.Fatalf("Loaded = %v", out.Loaded)
	}
	if !strings.Contains(out.Output, "## mcp__kerjan__create_ticket\nCreate a support ticket.") {
		t.Fatalf("output lacks the tool block:\n%s", out.Output)
	}
	if !strings.HasSuffix(out.Output, "\n\nLoaded: mcp__kerjan__create_ticket. Call them directly in your next step.") {
		t.Fatalf("output lacks the loaded line:\n%s", out.Output)
	}
	if strings.Contains(out.Output, "mcp__kerjan__ping") {
		t.Fatalf("select must return only the named tool:\n%s", out.Output)
	}
}

func TestToolSearchSelectBareUniqueName(t *testing.T) {
	out := callToolSearch(t, core.ToolCtx{DeferredTools: searchSpecs()}, `{"query":"select:create_ticket"}`)
	if !reflect.DeepEqual(out.Loaded, []string{"mcp__kerjan__create_ticket"}) {
		t.Fatalf("Loaded = %v\n%s", out.Loaded, out.Output)
	}
}

func TestToolSearchSelectUnknownName(t *testing.T) {
	out := callToolSearch(t, core.ToolCtx{DeferredTools: searchSpecs()}, `{"query":"select:mcp__kerjan__create_ticket,no_such_tool"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("status = %q", out.Status)
	}
	if !reflect.DeepEqual(out.Loaded, []string{"mcp__kerjan__create_ticket"}) {
		t.Fatalf("Loaded = %v", out.Loaded)
	}
	if !strings.Contains(out.Output, "Not found: no_such_tool") {
		t.Fatalf("unknown name is not reported as not found:\n%s", out.Output)
	}
}

func TestToolSearchSelectAmbiguousBareName(t *testing.T) {
	out := callToolSearch(t, core.ToolCtx{DeferredTools: searchSpecs()}, `{"query":"select:ping"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("status = %q", out.Status)
	}
	if len(out.Loaded) != 0 {
		t.Fatalf("ambiguous bare name loaded something: %v", out.Loaded)
	}
	if !strings.Contains(out.Output, "No deferred tool matched") || !strings.Contains(out.Output, "ping") {
		t.Fatalf("ambiguous bare name output = %q", out.Output)
	}
}

// Score per word: 3 tool part, 2 server, 1 description; ties keep name order;
// score 0 is excluded.
func TestToolSearchKeywordScores(t *testing.T) {
	specs := []core.ToolSpec{
		{Name: "mcp__aaa__ticket", Description: "A unique description with zzz.", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "mcp__zzz__ticket", Description: "Plain.", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "mcp__tickets__other", Description: "Plain.", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "mcp__mmm__thing", Description: "Handles ticket data.", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "mcp__mmm__nothing", Description: "Unrelated.", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}
	out := callToolSearch(t, core.ToolCtx{DeferredTools: specs}, `{"query":"ticket","limit":10}`)
	want := []string{"mcp__aaa__ticket", "mcp__zzz__ticket", "mcp__tickets__other", "mcp__mmm__thing"}
	if !reflect.DeepEqual(out.Loaded, want) {
		t.Fatalf("Loaded = %v, want %v\n%s", out.Loaded, want, out.Output)
	}
}

func TestToolSearchKeywordRankingTicketFirst(t *testing.T) {
	out := callToolSearch(t, core.ToolCtx{DeferredTools: searchSpecs()}, `{"query":"create ticket"}`)
	if len(out.Loaded) == 0 || out.Loaded[0] != "mcp__kerjan__create_ticket" {
		t.Fatalf("Loaded = %v, want create_ticket first", out.Loaded)
	}
}

func TestToolSearchLimitDefaultAndCap(t *testing.T) {
	var specs []core.ToolSpec
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"} {
		specs = append(specs, core.ToolSpec{Name: "mcp__srv__" + name, Description: "gamma", InputSchema: json.RawMessage(`{"type":"object"}`)})
	}
	tc := core.ToolCtx{DeferredTools: specs}

	if out := callToolSearch(t, tc, `{"query":"gamma"}`); len(out.Loaded) != 5 {
		t.Fatalf("default limit Loaded = %d, want 5", len(out.Loaded))
	}
	if out := callToolSearch(t, tc, `{"query":"gamma","limit":3}`); len(out.Loaded) != 3 {
		t.Fatalf("limit 3 Loaded = %d, want 3", len(out.Loaded))
	}
	if out := callToolSearch(t, tc, `{"query":"gamma","limit":99}`); len(out.Loaded) != 10 {
		t.Fatalf("limit 99 Loaded = %d, want 10", len(out.Loaded))
	}
}

func TestToolSearchNoMatch(t *testing.T) {
	out := callToolSearch(t, core.ToolCtx{DeferredTools: searchSpecs()}, `{"query":"zzzznomatch"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("status = %q", out.Status)
	}
	if len(out.Loaded) != 0 {
		t.Fatalf("Loaded = %v, want empty", out.Loaded)
	}
	if !strings.Contains(out.Output, "No deferred tool matched") || !strings.Contains(out.Output, "select:") {
		t.Fatalf("no-match output = %q", out.Output)
	}
}

func TestToolSearchNoDeferredTools(t *testing.T) {
	out := callToolSearch(t, core.ToolCtx{}, `{"query":"anything"}`)
	if out.Status != core.ToolStatusError || out.Output != "no deferred tools" {
		t.Fatalf("output = %q status = %q", out.Output, out.Status)
	}
}

func TestToolSearchExactOutputFormat(t *testing.T) {
	spec := core.ToolSpec{
		Name:        "mcp__kerjan__create_ticket",
		Description: "Create a ticket.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}}}`),
	}
	out := callToolSearch(t, core.ToolCtx{DeferredTools: []core.ToolSpec{spec}}, `{"query":"select:mcp__kerjan__create_ticket"}`)
	want := "## mcp__kerjan__create_ticket\n" +
		"Create a ticket.\n" +
		"Input schema: {\"type\":\"object\",\"properties\":{\"title\":{\"type\":\"string\"}}}\n" +
		"\n" +
		"Loaded: mcp__kerjan__create_ticket. Call them directly in your next step."
	if out.Output != want {
		t.Fatalf("output:\n%q\nwant:\n%q", out.Output, want)
	}
}
