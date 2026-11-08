package workflow

// Tests for edge id normalization: the web canvas keys hover, selection and
// deletion by edge id, so a graph whose edges share an empty (or repeated) id
// must never reach it. Normalization happens where a graph is saved and where
// one is read (detail and version views), but never rewrites stored history.

import (
	"context"
	"encoding/json"
	"testing"
)

// edgeIDGraph is a valid graph whose edges carry an empty id and a repeated
// one, the shape the workflow assistant used to produce.
func edgeIDGraph() Graph {
	return Graph{
		Nodes: []Node{
			{ID: "start", Type: "trigger.manual", Name: "Start", Position: Position{X: 0, Y: 0},
				Params: map[string]any{"enabled": true}},
			{ID: "a", Type: "message", Name: "A", Position: Position{X: 0, Y: 120},
				Params: map[string]any{"text": "a"}},
			{ID: "b", Type: "message", Name: "B", Position: Position{X: 0, Y: 240},
				Params: map[string]any{"text": "b"}},
			{ID: "c", Type: "message", Name: "C", Position: Position{X: 0, Y: 360},
				Params: map[string]any{"text": "c"}},
		},
		Edges: []Edge{
			{ID: "", From: "start", To: "a"},
			{ID: "dup", From: "start", To: "b"},
			{ID: "dup", From: "start", To: "c"},
		},
	}
}

// assertUniqueEdgeIDs fails when any edge id is empty or used more than once.
func assertUniqueEdgeIDs(t *testing.T, g Graph) {
	t.Helper()
	seen := map[string]bool{}
	for i, e := range g.Edges {
		if e.ID == "" {
			t.Fatalf("edge %d has an empty id: %+v", i, g.Edges)
		}
		if seen[e.ID] {
			t.Fatalf("edge %d repeats id %q: %+v", i, e.ID, g.Edges)
		}
		seen[e.ID] = true
	}
}

func TestGraphNormalizedAssignsUniqueEdgeIDs(t *testing.T) {
	in := edgeIDGraph()
	out := in.Normalized()
	assertUniqueEdgeIDs(t, out)

	// The first empty id takes "e<N>" with N past the edge count, the shape the
	// web canvas mints for a new edge; the repeated one gets the next free id.
	if out.Edges[0].ID != "e4" || out.Edges[1].ID != "dup" || out.Edges[2].ID != "e5" {
		t.Fatalf("normalized ids = %q, %q, %q", out.Edges[0].ID, out.Edges[1].ID, out.Edges[2].ID)
	}

	// Normalizing never writes into the caller's graph.
	if in.Edges[0].ID != "" || in.Edges[1].ID != "dup" || in.Edges[2].ID != "dup" {
		t.Fatalf("Normalized wrote into the source graph: %+v", in.Edges)
	}

	// It is idempotent: a normalized graph comes out unchanged.
	assertUniqueEdgeIDs(t, out.Normalized())
	if again := out.Normalized(); again.Edges[1].ID != out.Edges[1].ID || again.Edges[2].ID != out.Edges[2].ID {
		t.Fatalf("not idempotent: %+v vs %+v", again.Edges, out.Edges)
	}
}

func TestGraphNormalizedEdgeIDAvoidsExisting(t *testing.T) {
	g := Graph{Edges: []Edge{
		{ID: "e3", From: "a", To: "b"},
		{ID: "", From: "b", To: "c"},
	}}
	out := g.Normalized()
	assertUniqueEdgeIDs(t, out)
	if out.Edges[1].ID == "e3" {
		t.Fatalf("generated id collides with the existing e3: %+v", out.Edges)
	}
}

// A graph saved through the service reaches the store with unique, non-empty
// edge ids.
func TestServiceSaveNormalizesEdgeIDs(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{})
	ctx := context.Background()

	g := edgeIDGraph()
	created, err := svc.Create(ctx, CreateInput{Name: "Edges", Guardian: "g", Graph: &g})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	assertUniqueEdgeIDs(t, created.Graph)

	stored, err := st.GetVersion(ctx, "edges", 1)
	if err != nil {
		t.Fatalf("stored version: %v", err)
	}
	assertUniqueEdgeIDs(t, stored.Graph)
}

// A version stored before the fix still holds empty ids; the detail and version
// reads normalize it, and the stored row keeps its old ids.
func TestServiceReadNormalizesStoredEdgeIDs(t *testing.T) {
	st := NewMemStore()
	ctx := context.Background()
	if _, err := st.CreateWorkflow(ctx, Workflow{
		ID: "legacy", Name: "Legacy", Guardian: "g", Status: StatusDraft,
	}, edgeIDGraph(), "owner", "created"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	svc := newTestService(t, st, newFakeDeps(), &testClock{})

	detail, err := svc.Get(ctx, "legacy")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	assertUniqueEdgeIDs(t, detail.Graph)

	// The JSON boundary normalizes too.
	raw, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Graph Graph `json:"graph"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	assertUniqueEdgeIDs(t, wire.Graph)

	versions, err := svc.Versions(ctx, "legacy")
	if err != nil {
		t.Fatalf("versions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("versions=%d want 1", len(versions))
	}
	assertUniqueEdgeIDs(t, versions[0].Graph)

	// Reads did not rewrite history: the stored row keeps the old ids.
	stored, err := st.GetVersion(ctx, "legacy", 1)
	if err != nil {
		t.Fatalf("stored version: %v", err)
	}
	if stored.Graph.Edges[0].ID != "" || stored.Graph.Edges[1].ID != "dup" {
		t.Fatalf("read rewrote history: %+v", stored.Graph.Edges)
	}
}
