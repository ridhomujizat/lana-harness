package workflow

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestBuildAssistantGuide checks that the first chat note carries the
// workflow, the graph JSON, the rules and the save-test-publish working order
// (spec 51 REQ-WFC-002, REQ-WFC-004).
func TestBuildAssistantGuide(t *testing.T) {
	w := Workflow{ID: "riset-harian", Name: "Riset harian", Description: "Scrape 5 tech sources", Version: 3}
	g := researchGraph()
	cand := &Candidate{ID: 7, BaseVersion: 3, Summary: "add telegram"}
	note := BuildAssistantGuide(w, g, AssistEnv{}, nil, cand)

	graphJSON, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	wants := []string{
		"Riset harian",
		"riset-harian",
		"Scrape 5 tech sources",
		"Current graph, version 3",
		string(graphJSON),
		"{{ nodes.<id>.output.<path> }}",
		"prefer script_parse over agent",
		`leave the script_parse param "script" empty`,
		`always give a script_parse "output_schema"`,
		`"audit_every"`,
		"workflow_builder",
		"publish only after a test passed",
		"ask_user",
		"Open candidate #7 (based on version 3, not published): add telegram",
	}
	for _, want := range wants {
		if !strings.Contains(note, want) {
			t.Fatalf("guide is missing %q\n---\n%s", want, note)
		}
	}
	for _, gone := range []string{"Reply with ONE JSON object", "Do not call tools"} {
		if strings.Contains(note, gone) {
			t.Fatalf("guide still has the old contract %q", gone)
		}
	}
}

// TestBuildAssistantDelta sends only what changed.
func TestBuildAssistantDelta(t *testing.T) {
	w := Workflow{ID: "w", Name: "W", Version: 5}
	if got := BuildAssistantDelta(w, nil, nil); got != "" {
		t.Fatalf("delta with nothing new = %q, want empty", got)
	}
	g := Graph{Nodes: []Node{{ID: "start", Type: "trigger.manual"}}}
	got := BuildAssistantDelta(w, &g, nil)
	if !strings.Contains(got, "current version is now 5") || !strings.Contains(got, `"trigger.manual"`) {
		t.Fatalf("delta = %q", got)
	}
}

// TestDiffGraphs covers added, removed, changed and position-only moves.
func TestDiffGraphs(t *testing.T) {
	a := Graph{Nodes: []Node{
		{ID: "keep", Type: "message", Name: "Keep", Position: Position{X: 0, Y: 0}, Params: map[string]any{"text": "x"}},
		{ID: "move", Type: "message", Name: "Move", Position: Position{X: 0, Y: 0}, Params: map[string]any{"text": "y"}},
		{ID: "gone", Type: "message", Name: "Gone", Params: map[string]any{"text": "z"}},
	}}
	b := Graph{Nodes: []Node{
		{ID: "keep", Type: "message", Name: "Keep", Position: Position{X: 40, Y: 80}, Params: map[string]any{"text": "x"}},
		{ID: "move", Type: "message", Name: "Move", Position: Position{X: 0, Y: 0}, Params: map[string]any{"text": "changed"}},
		{ID: "fresh", Type: "message", Name: "Fresh", Params: map[string]any{"text": "n"}},
	}}
	got := DiffGraphs(a, b)
	want := GraphChange{
		Added:   []string{"Fresh"},
		Removed: []string{"Gone"},
		Changed: []string{"Move"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DiffGraphs = %+v, want %+v", got, want)
	}
	if got.FromVersion != 0 || got.ToVersion != 0 || got.Summary != "" {
		t.Fatalf("version fields should be zero: %+v", got)
	}
}

// TestDiffGraphsEdgeTarget checks that an edge change counts as a change of the
// edge's target node, except when that node is added or removed.
func TestDiffGraphsEdgeTarget(t *testing.T) {
	base := Graph{Nodes: []Node{
		{ID: "a", Type: "message", Name: "A", Params: map[string]any{"text": "x"}},
		{ID: "b", Type: "message", Name: "B", Params: map[string]any{"text": "y"}},
	}}
	withEdge := Graph{Nodes: base.Nodes, Edges: []Edge{{ID: "e1", From: "a", To: "b"}}}

	got := DiffGraphs(base, withEdge)
	if len(got.Added) != 0 || len(got.Removed) != 0 {
		t.Fatalf("nodes should not change: %+v", got)
	}
	if !reflect.DeepEqual(got.Changed, []string{"B"}) {
		t.Fatalf("added edge: Changed = %v, want [B]", got.Changed)
	}

	if got := DiffGraphs(withEdge, base); !reflect.DeepEqual(got.Changed, []string{"B"}) {
		t.Fatalf("removed edge: Changed = %v, want [B]", got.Changed)
	}

	// A new node that is the target of a new edge is added, not changed.
	single := Graph{Nodes: []Node{base.Nodes[0]}}
	fresh := Graph{
		Nodes: []Node{base.Nodes[0], {ID: "c", Type: "message", Name: "C", Params: map[string]any{"text": "z"}}},
		Edges: []Edge{{ID: "e1", From: "a", To: "c"}},
	}
	got = DiffGraphs(single, fresh)
	if !reflect.DeepEqual(got.Added, []string{"C"}) || len(got.Changed) != 0 {
		t.Fatalf("edge into a new node: got %+v, want only C added", got)
	}
}

// TestDiffGraphsPositionOnly confirms a pure move is not a change.
func TestDiffGraphsPositionOnly(t *testing.T) {
	a := Graph{Nodes: []Node{{ID: "x", Type: "message", Name: "X", Position: Position{X: 0, Y: 0}, Params: map[string]any{"text": "hi"}}}}
	b := Graph{Nodes: []Node{{ID: "x", Type: "message", Name: "X", Position: Position{X: 500, Y: 900}, Params: map[string]any{"text": "hi"}}}}
	got := DiffGraphs(a, b)
	if len(got.Added) != 0 || len(got.Removed) != 0 || len(got.Changed) != 0 {
		t.Fatalf("position-only move should not change the graph: %+v", got)
	}
}

func TestBuildAssistantGuideLastRun(t *testing.T) {
	last := &RunDetail{
		Run: Run{ID: "r1", Number: 4, Trigger: "manual", Version: 3, Status: RunFailed, Error: "node fetch failed"},
		Nodes: []NodeRun{
			{NodeID: "start", Status: "succeeded", Output: json.RawMessage(`{"ok":true}`)},
			{NodeID: "fetch", Status: NodeFailed, Error: "unknown tool mcp__x__y"},
		},
	}
	note := BuildAssistantGuide(Workflow{ID: "w", Name: "W"}, Graph{}, AssistEnv{}, last, nil)
	for _, want := range []string{"Last run: #4 (manual, version 3) failed", "node fetch failed: unknown tool mcp__x__y", `node start succeeded: output {"ok":true}`} {
		if !strings.Contains(note, want) {
			t.Fatalf("guide misses %q:\n%s", want, note)
		}
	}
	if delta := BuildAssistantDelta(Workflow{ID: "w"}, nil, last); !strings.Contains(delta, "Last run: #4") {
		t.Fatalf("delta misses the run: %q", delta)
	}
}
