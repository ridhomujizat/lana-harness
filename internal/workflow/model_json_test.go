package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A freshly created workflow has one trigger node and no edges. The UI
// iterates graph.edges, so the API must send [] and never null.
func TestCreatedWorkflowMarshalsEmptyEdges(t *testing.T) {
	svc := newTestService(t, NewMemStore(), newFakeDeps(), &testClock{})
	created, err := svc.Create(context.Background(), CreateInput{Name: "hi", Guardian: "g"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(created.Graph.Nodes) != 1 {
		t.Fatalf("nodes=%+v, want one trigger", created.Graph.Nodes)
	}
	b, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"edges":[]`) {
		t.Fatalf("detail JSON has no \"edges\":[]: %s", b)
	}
	if strings.Contains(string(b), "null") && strings.Contains(string(b), `"edges":null`) {
		t.Fatalf("detail JSON has edges null: %s", b)
	}
}

func TestGraphMarshalNeverNull(t *testing.T) {
	cases := map[string]Graph{
		"zero":       {},
		"nil params": {Nodes: []Node{{ID: "a", Type: "trigger.manual"}}},
	}
	for name, g := range cases {
		b, err := json.Marshal(g)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		s := string(b)
		if strings.Contains(s, "null") || !strings.Contains(s, `"nodes":[`) || !strings.Contains(s, `"edges":[]`) {
			t.Fatalf("%s: graph JSON=%s", name, s)
		}
	}
	// Normalized never writes into the caller's nodes.
	g := Graph{Nodes: []Node{{ID: "a"}}}
	if n := g.Normalized(); n.Nodes[0].Params == nil || g.Nodes[0].Params != nil {
		t.Fatalf("Normalized params: got %v, original %v", n.Nodes[0].Params, g.Nodes[0].Params)
	}
}

// Versions stored before the fix may hold "edges":null; they still load and
// marshal back out as [].
func TestGraphWithNullEdgesLoads(t *testing.T) {
	var v Version
	raw := `{"version":1,"graph":{"nodes":[{"id":"run-button","type":"trigger.manual","name":"Run","position":{"x":0,"y":0},"params":null}],"edges":null}}`
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(v.Graph.Nodes) != 1 || v.Graph.Edges != nil {
		t.Fatalf("graph=%+v", v.Graph)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); !strings.Contains(s, `"edges":[]`) || !strings.Contains(s, `"params":{}`) {
		t.Fatalf("version JSON=%s", s)
	}
}

func TestChangeAndLockMarshalNeverNull(t *testing.T) {
	for name, value := range map[string]any{
		"change":            GraphChange{FromVersion: 1, ToVersion: 2},
		"lock":              Lock{Version: 1},
		"lock tool":         LockTool{Tool: "bash"},
		"chat":              ChatMessage{Role: "guardian", Change: &GraphChange{}},
		"lock tool in lock": Lock{Tools: []LockTool{{Tool: "bash"}}},
	} {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(string(b), "null") {
			t.Fatalf("%s JSON has null: %s", name, b)
		}
	}
}
