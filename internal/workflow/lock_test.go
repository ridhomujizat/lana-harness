package workflow

import (
	"reflect"
	"testing"
)

// lockGraph is the fixture of the catalog lock test: a browser, a worker with
// two tools, a message, a knowledge and a state_set node.
func lockGraph() Graph {
	return Graph{Nodes: []Node{
		{ID: "open", Type: "browser", Name: "Scrape"},
		{ID: "work", Type: "worker", Name: "Worker", Params: map[string]any{"tools": []any{"webfetch", "knowledge_search"}}},
		{ID: "say", Type: "message", Name: "Send digest"},
		{ID: "save", Type: "knowledge", Name: "Save note"},
		{ID: "mark", Type: "state_set", Name: "Mark", Params: map[string]any{"key": "k", "value": 1}},
	}}
}

func TestLockComputeLock(t *testing.T) {
	want := []LockTool{
		{Tool: "browser", UsedBy: []string{"Scrape"}, Risk: RiskRead},
		{Tool: "knowledge.save", UsedBy: []string{"Save note"}, Risk: RiskWrite},
		{Tool: "knowledge_search", UsedBy: []string{"Worker"}, Risk: RiskWrite},
		{Tool: "state.set", UsedBy: []string{"Mark"}, Risk: RiskWrite},
		{Tool: "telegram.send", UsedBy: []string{"Send digest"}, Risk: RiskWrite},
		{Tool: "webfetch", UsedBy: []string{"Worker"}, Risk: RiskRead},
	}
	got := ComputeLock(lockGraph(), nil)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ComputeLock=%+v want %+v", got, want)
	}

	// The risk function names unknown tools; an empty answer defaults to write.
	risk := func(tool string) string {
		if tool == "knowledge_search" {
			return RiskRead
		}
		return ""
	}
	got = ComputeLock(Graph{Nodes: []Node{
		{ID: "a", Type: "tool", Name: "Search", Params: map[string]any{"name": "knowledge_search"}},
		{ID: "b", Type: "tool", Name: "Fetch", Params: map[string]any{"name": "mcp.search"}},
	}}, risk)
	want = []LockTool{
		{Tool: "knowledge_search", UsedBy: []string{"Search"}, Risk: RiskRead},
		{Tool: "mcp.search", UsedBy: []string{"Fetch"}, Risk: RiskWrite},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ComputeLock risk=%+v want %+v", got, want)
	}

	// Empty and nil graphs yield a non-nil empty slice.
	if got := ComputeLock(Graph{}, nil); got == nil || len(got) != 0 {
		t.Fatalf("ComputeLock(Graph{})=%+v want empty non-nil", got)
	}
}

func TestLockUsedByDedupAndGraphOrder(t *testing.T) {
	g := Graph{Nodes: []Node{
		{ID: "w", Type: "worker", Name: "Worker", Params: map[string]any{"tools": []any{"bash", "bash"}}},
		{ID: "b", Type: "bash", Name: "Worker"},
	}}
	got := ComputeLock(g, nil)
	want := []LockTool{{Tool: "bash", UsedBy: []string{"Worker"}, Risk: RiskAdmin}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ComputeLock=%+v want %+v", got, want)
	}

	// Disabled nodes contribute no tool.
	disabled := Graph{Nodes: []Node{{ID: "b", Type: "bash", Name: "Off", Disabled: true}}}
	if got := ComputeLock(disabled, nil); len(got) != 0 {
		t.Fatalf("disabled node contributed %+v", got)
	}
}

func TestLockCovers(t *testing.T) {
	g := lockGraph()
	lock := &Lock{Version: 1, Tools: []LockTool{
		{Tool: "browser", Risk: RiskRead},
		{Tool: "knowledge.save", Risk: RiskWrite},
		{Tool: "knowledge_search", Risk: RiskWrite},
		{Tool: "state.set", Risk: RiskWrite},
		{Tool: "telegram.send", Risk: RiskWrite},
		{Tool: "webfetch", Risk: RiskRead},
	}}
	if !LockCovers(lock, g) {
		t.Fatal("LockCovers=false want true for a lock with every tool")
	}

	partial := &Lock{Version: 1, Tools: []LockTool{
		{Tool: "browser"}, {Tool: "knowledge.save"}, {Tool: "knowledge_search"},
		{Tool: "state.set"}, {Tool: "telegram.send"},
	}}
	if LockCovers(partial, g) {
		t.Fatal("LockCovers=true want false when webfetch is missing")
	}

	if LockCovers(nil, g) {
		t.Fatal("LockCovers=true want false for a nil lock")
	}
	if !LockCovers(lock, Graph{}) {
		t.Fatal("LockCovers=false want true for an empty graph")
	}
}

func TestLockSet(t *testing.T) {
	lock := &Lock{Tools: []LockTool{{Tool: "browser"}, {Tool: "bash"}}}
	set := LockSet(lock)
	if !set["browser"] || !set["bash"] || len(set) != 2 {
		t.Fatalf("LockSet=%v", set)
	}
	if empty := LockSet(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("LockSet(nil)=%v want empty non-nil", empty)
	}
}
