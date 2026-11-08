package workflow

import "encoding/json"

// The web UI iterates these lists without null checks, so a nil Go slice (or
// a nil params map) must never reach JSON as null. The MarshalJSON methods
// below cover every boundary that writes JSON: API responses and the store
// (graphs, locks and chat change cards are stored as JSON). Rows stored before
// this fix with "edges":null still unmarshal into a nil slice, which is fine
// in Go and marshals back out as [].

// Normalized returns a copy of g whose node and edge lists and node params
// are never nil.
func (g Graph) Normalized() Graph {
	out := Graph{Nodes: g.Nodes, Edges: g.Edges}
	if out.Nodes == nil {
		out.Nodes = []Node{}
	}
	if out.Edges == nil {
		out.Edges = []Edge{}
	}
	copied := false // copy the nodes before the first write; g stays untouched
	for i := range out.Nodes {
		if out.Nodes[i].Params != nil {
			continue
		}
		if !copied {
			out.Nodes = append([]Node(nil), out.Nodes...)
			copied = true
		}
		out.Nodes[i].Params = map[string]any{}
	}
	return out
}

// MarshalJSON writes nodes, edges and every node's params as [] / {} when
// they are nil.
func (g Graph) MarshalJSON() ([]byte, error) {
	type plain Graph
	return json.Marshal(plain(g.Normalized()))
}

// MarshalJSON writes added, removed and changed as [] when they are nil.
func (c GraphChange) MarshalJSON() ([]byte, error) {
	type plain GraphChange
	out := plain(c)
	out.Added = nonNilStrings(out.Added)
	out.Removed = nonNilStrings(out.Removed)
	out.Changed = nonNilStrings(out.Changed)
	return json.Marshal(out)
}

// MarshalJSON writes tools as [] when it is nil.
func (l Lock) MarshalJSON() ([]byte, error) {
	type plain Lock
	out := plain(l)
	if out.Tools == nil {
		out.Tools = []LockTool{}
	}
	return json.Marshal(out)
}

// MarshalJSON writes used_by as [] when it is nil.
func (t LockTool) MarshalJSON() ([]byte, error) {
	type plain LockTool
	out := plain(t)
	out.UsedBy = nonNilStrings(out.UsedBy)
	return json.Marshal(out)
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
