package workflow

import (
	"slices"
	"testing"
)

func TestCatalogSpec(t *testing.T) {
	want := []struct {
		typ      string
		group    string
		required []string
		ports    []string
		trigger  bool
	}{
		{"trigger.manual", groupTrigger, nil, []string{"out"}, true},
		{"trigger.schedule", groupTrigger, []string{"cron"}, []string{"out"}, true},
		{"trigger.interval", groupTrigger, []string{"minutes"}, []string{"out"}, true},
		{"trigger.webhook", groupTrigger, nil, []string{"out"}, true},
		{"trigger.event", groupTrigger, []string{"kind"}, []string{"out"}, true},
		{"trigger.chat", groupTrigger, []string{"command"}, []string{"out"}, true},
		{"agent", groupAI, []string{"prompt"}, []string{"out"}, false},
		{"bot", groupAI, []string{"agent", "prompt"}, []string{"out"}, false},
		{"worker", groupAI, []string{"task"}, []string{"out"}, false},
		{"script_parse", groupAI, []string{"input", "goal", "output_schema"}, []string{"out"}, false},
		{"browser", groupTool, []string{"url"}, []string{"out"}, false},
		{"bash", groupTool, []string{"command"}, []string{"out"}, false},
		{"webfetch", groupTool, []string{"url"}, []string{"out"}, false},
		{"tool", groupTool, []string{"name"}, []string{"out"}, false},
		{"message", groupTool, []string{"text"}, []string{"out"}, false},
		{"knowledge", groupTool, []string{"title", "text"}, []string{"out"}, false},
		{"if", groupFlow, []string{"left", "op"}, []string{"true", "false"}, false},
		{"switch", groupFlow, []string{"value", "cases"}, nil, false},
		{"loop", groupFlow, []string{"items"}, []string{"body", "done", "out"}, false},
		{"wait", groupFlow, []string{"seconds"}, []string{"out"}, false},
		{"approval", groupFlow, []string{"message"}, []string{"approved", "denied"}, false},
		{"merge", groupFlow, nil, []string{"out"}, false},
		{"state_get", groupState, []string{"key"}, []string{"out"}, false},
		{"state_set", groupState, []string{"key", "value"}, []string{"out"}, false},
	}

	for _, tc := range want {
		spec, ok := Spec(tc.typ)
		if !ok {
			t.Fatalf("Spec(%q) not found", tc.typ)
		}
		if spec.Type != tc.typ || spec.Group != tc.group || spec.Trigger != tc.trigger {
			t.Fatalf("Spec(%q)=%+v", tc.typ, spec)
		}
		if !slices.Equal(spec.Required, tc.required) {
			t.Fatalf("Spec(%q).Required=%v want %v", tc.typ, spec.Required, tc.required)
		}
		if !slices.Equal(spec.Ports, tc.ports) {
			t.Fatalf("Spec(%q).Ports=%v want %v", tc.typ, spec.Ports, tc.ports)
		}
		if spec.Label == "" {
			t.Fatalf("Spec(%q).Label is empty", tc.typ)
		}
	}

	if _, ok := Spec("nope"); ok {
		t.Fatal("Spec(nope) reported found")
	}
}

func TestCatalogNodeTypes(t *testing.T) {
	types := NodeTypes()
	if len(types) != 24 {
		t.Fatalf("NodeTypes=%v want 24 types", types)
	}
	if !slices.IsSorted(types) {
		t.Fatalf("NodeTypes is not sorted: %v", types)
	}
	for _, want := range []string{
		"trigger.manual", "trigger.schedule", "trigger.interval", "trigger.webhook",
		"trigger.event", "trigger.chat", "agent", "bot", "worker", "script_parse",
		"browser", "bash", "webfetch", "tool", "message", "knowledge", "if", "switch",
		"loop", "wait", "approval", "merge", "state_get", "state_set",
	} {
		if !slices.Contains(types, want) {
			t.Fatalf("NodeTypes is missing %q", want)
		}
	}
}

func TestCatalogPorts(t *testing.T) {
	cases := []struct {
		node Node
		want []string
	}{
		{Node{Type: "switch", Params: map[string]any{"cases": []any{"a", "b"}}}, []string{"a", "b", "default"}},
		{Node{Type: "switch", Params: map[string]any{"cases": []string{"x"}}}, []string{"x", "default"}},
		{Node{Type: "switch"}, []string{"default"}},
		{Node{Type: "if"}, []string{"true", "false"}},
		{Node{Type: "loop"}, []string{"body", "done", "out"}},
		{Node{Type: "approval"}, []string{"approved", "denied"}},
		{Node{Type: "merge"}, []string{"out"}},
		{Node{Type: "browser"}, []string{"out"}},
		{Node{Type: "unknown"}, []string{"out"}},
	}
	for _, tc := range cases {
		if got := NodePorts(tc.node); !slices.Equal(got, tc.want) {
			t.Fatalf("NodePorts(%+v)=%v want %v", tc.node, got, tc.want)
		}
	}
}

func TestCatalogLockTools(t *testing.T) {
	cases := []struct {
		node Node
		want []string
	}{
		{Node{Type: "browser"}, []string{"browser"}},
		{Node{Type: "bash"}, []string{"bash"}},
		{Node{Type: "webfetch"}, []string{"webfetch"}},
		{Node{Type: "tool", Params: map[string]any{"name": "mcp.search"}}, []string{"mcp.search"}},
		{Node{Type: "tool"}, nil},
		{Node{Type: "message"}, []string{"telegram.send"}},
		{Node{Type: "knowledge"}, []string{"knowledge.save"}},
		{Node{Type: "state_set"}, []string{"state.set"}},
		{Node{Type: "worker", Params: map[string]any{"tools": []any{"webfetch", "knowledge_search"}}}, []string{"webfetch", "knowledge_search"}},
		{Node{Type: "worker", Params: map[string]any{"tools": []string{"bash"}}}, []string{"bash"}},
		{Node{Type: "worker"}, nil},
		{Node{Type: "state_get"}, nil},
		{Node{Type: "agent"}, nil},
		{Node{Type: "script_parse"}, nil},
		{Node{Type: "trigger.schedule"}, nil},
		{Node{Type: "browser", Disabled: true}, nil},
	}
	for _, tc := range cases {
		if got := LockToolsOf(tc.node); !slices.Equal(got, tc.want) {
			t.Fatalf("LockToolsOf(%+v)=%v want %v", tc.node, got, tc.want)
		}
	}
}

// TestCatalogScriptParse pins the spec #44 catalog row: required params, the out
// port, and the empty tool lock (REQ-SP-001, REQ-SP-014).
func TestCatalogScriptParse(t *testing.T) {
	spec, ok := Spec("script_parse")
	if !ok {
		t.Fatal("Spec(script_parse) not found")
	}
	if spec.Type != "script_parse" || spec.Group != groupAI || spec.Trigger {
		t.Fatalf("Spec(script_parse)=%+v", spec)
	}
	if spec.Label != "Script Parse" {
		t.Fatalf("Label=%q, want %q", spec.Label, "Script Parse")
	}
	if want := []string{"input", "goal", "output_schema"}; !slices.Equal(spec.Required, want) {
		t.Fatalf("Required=%v, want %v", spec.Required, want)
	}
	if want := []string{"out"}; !slices.Equal(spec.Ports, want) {
		t.Fatalf("Ports=%v, want %v", spec.Ports, want)
	}
	if got := LockToolsOf(Node{Type: "script_parse"}); got != nil {
		t.Fatalf("LockToolsOf(script_parse)=%v, want nil", got)
	}
}
