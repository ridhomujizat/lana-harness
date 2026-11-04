package workflow

import (
	"strings"
	"testing"
)

// researchGraph is a valid daily-research style graph: a schedule trigger, a
// loop with two body nodes, an agent with an output schema, an if with true and
// false branches, a message and a state_set.
func researchGraph() Graph {
	return Graph{
		Nodes: []Node{
			{ID: "every-day", Type: "trigger.schedule", Name: "Every day", Position: Position{X: 240, Y: 40},
				Params: map[string]any{"cron": "0 7 * * *", "enabled": true}},
			{ID: "each", Type: "loop", Name: "For each source", Position: Position{X: 240, Y: 160},
				Params: map[string]any{"items": "{{ state.sources }}"}},
			{ID: "scrape", Type: "browser", Name: "Scrape page", Parent: "each", Position: Position{X: 240, Y: 280},
				Params: map[string]any{"url": "https://{{ item }}"}},
			{ID: "rank", Type: "agent", Name: "Rank", Parent: "each", Position: Position{X: 240, Y: 400},
				Params: map[string]any{"prompt": "Rank {{ item }}", "output_schema": map[string]any{"score": "integer"}}},
			{ID: "check", Type: "if", Name: "Good enough?", Position: Position{X: 240, Y: 520},
				Params: map[string]any{"left": "{{ nodes.rank.output.score }}", "op": ">=", "right": 7}},
			{ID: "send", Type: "message", Name: "Send digest", Position: Position{X: 120, Y: 640},
				Params: map[string]any{"text": "Score: {{ nodes.rank.output.score }}"}},
			{ID: "mark", Type: "state_set", Name: "Mark run", Position: Position{X: 360, Y: 640},
				Params: map[string]any{"key": "last_run_at", "value": "{{ run.number }}"}},
		},
		Edges: []Edge{
			{ID: "e1", From: "every-day", FromPort: "out", To: "each"},
			{ID: "e2", From: "each", FromPort: "body", To: "scrape"},
			{ID: "e3", From: "scrape", FromPort: "out", To: "rank"},
			{ID: "e4", From: "each", FromPort: "done", To: "check"},
			{ID: "e5", From: "check", FromPort: "true", To: "send"},
			{ID: "e6", From: "check", FromPort: "false", To: "mark"},
		},
	}
}

// cycleGraph is the AC-WF-002 graph: a -> b -> a.
func cycleGraph() Graph {
	return Graph{
		Nodes: []Node{
			{ID: "a", Type: "message", Name: "A", Params: map[string]any{"text": "x"}},
			{ID: "b", Type: "message", Name: "B", Params: map[string]any{"text": "y"}},
		},
		Edges: []Edge{
			{ID: "e1", From: "a", To: "b"},
			{ID: "e2", From: "b", To: "a"},
		},
	}
}

// TestValidateValidGraph is the clean research graph: no issues at all.
func TestValidateValidGraph(t *testing.T) {
	issues := Validate(researchGraph(), func(string) bool { return true })
	if len(issues) != 0 {
		t.Fatalf("Validate(valid) = %+v, want no issues", issues)
	}
}

// TestValidateIssueCodes covers one graph per issue code.
func TestValidateIssueCodes(t *testing.T) {
	agentOK := func(string) bool { return true }
	agentNo := func(string) bool { return false }

	cases := []struct {
		name   string
		graph  Graph
		agents func(string) bool
		code   string
		node   string
	}{
		{
			name:   "unknown_type",
			graph:  Graph{Nodes: []Node{{ID: "a", Type: "nope"}}},
			agents: agentOK, code: "unknown_type", node: "a",
		},
		{
			name:   "bad_id",
			graph:  Graph{Nodes: []Node{{ID: "Bad Id", Type: "message", Params: map[string]any{"text": "x"}}}},
			agents: agentOK, code: "bad_id", node: "Bad Id",
		},
		{
			name: "duplicate_id",
			graph: Graph{Nodes: []Node{
				{ID: "a", Type: "message", Params: map[string]any{"text": "x"}},
				{ID: "a", Type: "message", Params: map[string]any{"text": "y"}},
			}},
			agents: agentOK, code: "duplicate_id", node: "a",
		},
		{
			name:   "missing_param",
			graph:  Graph{Nodes: []Node{{ID: "a", Type: "agent"}}},
			agents: agentOK, code: "missing_param", node: "a",
		},
		{
			name:   "missing_param_cases",
			graph:  Graph{Nodes: []Node{{ID: "a", Type: "switch", Params: map[string]any{"value": 1, "cases": []any{}}}}},
			agents: agentOK, code: "missing_param", node: "a",
		},
		{
			name:   "missing_param_minutes",
			graph:  Graph{Nodes: []Node{{ID: "a", Type: "trigger.interval", Params: map[string]any{"minutes": 0}}}},
			agents: agentOK, code: "missing_param", node: "a",
		},
		{
			name:   "missing_param_seconds",
			graph:  Graph{Nodes: []Node{{ID: "a", Type: "wait", Params: map[string]any{"seconds": 604801}}}},
			agents: agentOK, code: "missing_param", node: "a",
		},
		{
			name:   "bad_template",
			graph:  Graph{Nodes: []Node{{ID: "a", Type: "message", Params: map[string]any{"text": "{{ input"}}}},
			agents: agentOK, code: "bad_template", node: "a",
		},
		{
			name:   "bad_template_ref",
			graph:  Graph{Nodes: []Node{{ID: "a", Type: "message", Params: map[string]any{"text": "{{ nodes.ghost.output.x }}"}}}},
			agents: agentOK, code: "bad_template", node: "a",
		},
		{
			name:   "bad_cron",
			graph:  Graph{Nodes: []Node{{ID: "a", Type: "trigger.schedule", Params: map[string]any{"cron": "nope"}}}},
			agents: agentOK, code: "bad_cron", node: "a",
		},
		{
			name:   "bad_parent_missing",
			graph:  Graph{Nodes: []Node{{ID: "a", Type: "message", Parent: "ghost", Params: map[string]any{"text": "x"}}}},
			agents: agentOK, code: "bad_parent", node: "a",
		},
		{
			name: "bad_parent_not_loop",
			graph: Graph{Nodes: []Node{
				{ID: "t", Type: "trigger.manual"},
				{ID: "a", Type: "message", Parent: "t", Params: map[string]any{"text": "x"}},
			}},
			agents: agentOK, code: "bad_parent", node: "a",
		},
		{
			name:   "bad_parent_trigger",
			graph:  Graph{Nodes: []Node{{ID: "t", Type: "trigger.manual", Parent: "each"}}},
			agents: agentOK, code: "bad_parent", node: "t",
		},
		{
			name: "bad_parent_nested_loop",
			graph: Graph{Nodes: []Node{
				{ID: "outer", Type: "loop", Params: map[string]any{"items": "{{ input.x }}"}},
				{ID: "inner", Type: "loop", Parent: "outer", Params: map[string]any{"items": "{{ input.y }}"}},
			}},
			agents: agentOK, code: "bad_parent", node: "inner",
		},
		{
			name: "missing_node",
			graph: Graph{
				Nodes: []Node{{ID: "a", Type: "message", Params: map[string]any{"text": "x"}}},
				Edges: []Edge{{ID: "e1", From: "a", To: "ghost"}},
			},
			agents: agentOK, code: "missing_node", node: "e1",
		},
		{
			name: "bad_port",
			graph: Graph{
				Nodes: []Node{
					{ID: "a", Type: "message", Params: map[string]any{"text": "x"}},
					{ID: "b", Type: "message", Params: map[string]any{"text": "y"}},
				},
				Edges: []Edge{{ID: "e1", From: "a", FromPort: "true", To: "b"}},
			},
			agents: agentOK, code: "bad_port", node: "e1",
		},
		{
			name: "bad_port_into_trigger",
			graph: Graph{
				Nodes: []Node{
					{ID: "a", Type: "message", Params: map[string]any{"text": "x"}},
					{ID: "t", Type: "trigger.manual"},
				},
				Edges: []Edge{{ID: "e1", From: "a", To: "t"}},
			},
			agents: agentOK, code: "bad_port", node: "e1",
		},
		{
			name: "bad_edge",
			graph: Graph{
				Nodes: []Node{
					{ID: "l", Type: "loop", Params: map[string]any{"items": "{{ input.x }}"}},
					{ID: "b", Type: "message", Parent: "l", Params: map[string]any{"text": "x"}},
					{ID: "t", Type: "message", Params: map[string]any{"text": "y"}},
				},
				Edges: []Edge{
					{ID: "e1", From: "l", FromPort: "body", To: "b"},
					{ID: "e2", From: "b", To: "t"},
				},
			},
			agents: agentOK, code: "bad_edge", node: "e2",
		},
		{
			name:   "cycle",
			graph:  cycleGraph(),
			agents: agentOK, code: "cycle", node: "a",
		},
		{
			name:   "unreachable",
			graph:  Graph{Nodes: []Node{{ID: "a", Type: "message", Params: map[string]any{"text": "x"}}}},
			agents: agentOK, code: "unreachable", node: "a",
		},
		{
			name: "unreachable_body",
			graph: Graph{Nodes: []Node{
				{ID: "l", Type: "loop", Params: map[string]any{"items": "{{ input.x }}"}},
				{ID: "b", Type: "message", Parent: "l", Params: map[string]any{"text": "x"}},
			}},
			agents: agentOK, code: "unreachable", node: "b",
		},
		{
			name: "unknown_agent",
			graph: Graph{
				Nodes: []Node{
					{ID: "t", Type: "trigger.manual"},
					{ID: "a", Type: "bot", Params: map[string]any{"agent": "ghost", "prompt": "x"}},
				},
				Edges: []Edge{{ID: "e1", From: "t", To: "a"}},
			},
			agents: agentNo, code: "unknown_agent", node: "a",
		},
	}

	for _, tc := range cases {
		issues := Validate(tc.graph, tc.agents)
		if !hasCodeFor(issues, tc.node, tc.code) {
			t.Fatalf("%s: want issue %q on node %q, got %+v", tc.name, tc.code, tc.node, issues)
		}
	}
}

// TestValidateCycleACWF002 checks AC-WF-002: an a -> b -> a graph fails with a
// cycle issue that names the nodes.
func TestValidateCycleACWF002(t *testing.T) {
	issues := Validate(cycleGraph(), nil)
	var cycle *Issue
	for i := range issues {
		if issues[i].Code == "cycle" {
			cycle = &issues[i]
		}
	}
	if cycle == nil {
		t.Fatalf("Validate = %+v, want a cycle issue", issues)
	}
	if !strings.Contains(cycle.Message, "a") || !strings.Contains(cycle.Message, "b") {
		t.Fatalf("cycle message %q should name a and b", cycle.Message)
	}
}

// TestValidateForActivationNoTrigger checks the activation-only rule.
func TestValidateForActivationNoTrigger(t *testing.T) {
	plain := Graph{Nodes: []Node{{ID: "a", Type: "message", Params: map[string]any{"text": "x"}}}}
	issues := ValidateForActivation(plain, nil)
	if !hasCodeFor(issues, "", "no_trigger") {
		t.Fatalf("ValidateForActivation = %+v, want no_trigger with empty node id", issues)
	}

	disabled := Graph{Nodes: []Node{{ID: "t", Type: "trigger.manual", Params: map[string]any{"enabled": false}}}}
	if !hasCode(ValidateForActivation(disabled, nil), "no_trigger") {
		t.Fatal("a disabled trigger should still be no_trigger")
	}

	if hasCode(ValidateForActivation(researchGraph(), nil), "no_trigger") {
		t.Fatal("an enabled schedule trigger should not be no_trigger")
	}
	if got := ValidateForActivation(researchGraph(), nil); len(got) != 0 {
		t.Fatalf("ValidateForActivation(valid) = %+v, want no issues", got)
	}
}

// TestValidateWorkflowID covers the slug id pattern.
func TestValidateWorkflowID(t *testing.T) {
	for _, id := range []string{"riset-harian", "w", "a1-b2", "riset-"} {
		if !ValidWorkflowID(id) {
			t.Fatalf("ValidWorkflowID(%q) = false, want true", id)
		}
	}
	for _, id := range []string{"", "Riset", "riset_harian", "-riset", "riset_har"} {
		if ValidWorkflowID(id) {
			t.Fatalf("ValidWorkflowID(%q) = true, want false", id)
		}
	}
}

// TestSlug covers the name-to-id rules of REQ-WF-001.
func TestSlug(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Riset harian", "riset-harian"},
		{"", "workflow"},
		{"  Hello -- World  ", "hello-world"},
		{"Fetch_news (daily)", "fetch-news-daily"},
		{"...", "workflow"},
		{strings.Repeat("a", 60), strings.Repeat("a", 48)},
	}
	for _, tc := range cases {
		if got := Slug(tc.in); got != tc.want {
			t.Fatalf("Slug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// hasCode reports whether any issue has the given code.
func hasCode(issues []Issue, code string) bool {
	for _, is := range issues {
		if is.Code == code {
			return true
		}
	}
	return false
}

// hasCodeFor reports whether an issue with the code exists on the node.
func hasCodeFor(issues []Issue, node, code string) bool {
	for _, is := range issues {
		if is.Code == code && is.NodeID == node {
			return true
		}
	}
	return false
}
