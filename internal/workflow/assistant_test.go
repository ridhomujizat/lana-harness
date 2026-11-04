package workflow

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// TestBuildAssistantPrompt checks that the prompt carries the workflow, the
// graph JSON, the chat, the owner message and the reply contract.
func TestBuildAssistantPrompt(t *testing.T) {
	w := Workflow{ID: "riset-harian", Name: "Riset harian", Description: "Scrape 5 tech sources"}
	g := researchGraph()
	history := []ChatMessage{
		{Role: "owner", Text: "add a summarize step"},
		{Role: "guardian", Text: "done"},
	}
	prompt := BuildAssistantPrompt(w, g, history, "now also send to telegram")

	graphJSON, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	wants := []string{
		"Riset harian",
		"riset-harian",
		"Scrape 5 tech sources",
		string(graphJSON),
		"owner: add a summarize step",
		"guardian: done",
		"now also send to telegram",
		"Reply with ONE JSON object only, no prose around it",
		"Do not call tools",
		`"reply"`,
		`"graph"`,
		`"summary"`,
		"{{ nodes.<id>.output.<path> }}",
	}
	for _, want := range wants {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt is missing %q\n---\n%s", want, prompt)
		}
	}
}

// TestBuildAssistantPromptHistoryLimit keeps only the last ten messages.
func TestBuildAssistantPromptHistoryLimit(t *testing.T) {
	var history []ChatMessage
	for i := 0; i < 15; i++ {
		history = append(history, ChatMessage{Role: "owner", Text: fmt.Sprintf("m%d", i)})
	}
	prompt := BuildAssistantPrompt(Workflow{ID: "w", Name: "W"}, Graph{}, history, "hi")
	for _, dropped := range []string{"m0", "m1", "m2", "m3", "m4"} {
		if strings.Contains(prompt, "owner: "+dropped+"\n") {
			t.Fatalf("prompt should not keep %q", dropped)
		}
	}
	for _, kept := range []string{"m5", "m10", "m14"} {
		if !strings.Contains(prompt, "owner: "+kept+"\n") {
			t.Fatalf("prompt should keep %q\n%s", kept, prompt)
		}
	}
}

// TestParseAssistantReply covers a fenced JSON answer, prose only and a null
// graph.
func TestParseAssistantReply(t *testing.T) {
	t.Run("fenced null graph", func(t *testing.T) {
		text := "Here you go:\n```json\n{\"reply\":\"oke\",\"graph\":null,\"summary\":\"none\"}\n```\nBye."
		reply, err := ParseAssistantReply(text)
		if err != nil {
			t.Fatal(err)
		}
		if reply.Reply != "oke" || reply.Summary != "none" || reply.Graph != nil {
			t.Fatalf("ParseAssistantReply = %+v", reply)
		}
	})

	t.Run("missing graph key", func(t *testing.T) {
		reply, err := ParseAssistantReply(`{"reply":"no change","summary":""}`)
		if err != nil {
			t.Fatal(err)
		}
		if reply.Reply != "no change" || reply.Graph != nil {
			t.Fatalf("ParseAssistantReply = %+v", reply)
		}
	})

	t.Run("prose only", func(t *testing.T) {
		reply, err := ParseAssistantReply("  I would add a summarize step.  ")
		if err != nil {
			t.Fatal(err)
		}
		if reply.Reply != "I would add a summarize step." || reply.Graph != nil || reply.Summary != "" {
			t.Fatalf("ParseAssistantReply = %+v", reply)
		}
	})

	t.Run("full graph", func(t *testing.T) {
		g := researchGraph()
		raw, err := json.Marshal(g)
		if err != nil {
			t.Fatal(err)
		}
		text := `{"reply":"added one node","graph":` + string(raw) + `,"summary":"+1 node"}`
		reply, err := ParseAssistantReply(text)
		if err != nil {
			t.Fatal(err)
		}
		if reply.Reply != "added one node" || reply.Summary != "+1 node" {
			t.Fatalf("ParseAssistantReply = %+v", reply)
		}
		if reply.Graph == nil || len(reply.Graph.Nodes) != len(g.Nodes) || len(reply.Graph.Edges) != len(g.Edges) {
			t.Fatalf("ParseAssistantReply graph = %+v", reply.Graph)
		}
	})
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
