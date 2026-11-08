package workflow

import (
	"encoding/json"
	"testing"
)

func TestGraphMarshalNilListsAsEmpty(t *testing.T) {
	data, err := json.Marshal(Graph{})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"nodes":[],"edges":[]}` {
		t.Fatalf("got %s", data)
	}
	data, err = json.Marshal(struct {
		Graph Graph `json:"graph"`
	}{Graph{Nodes: []Node{{ID: "a"}}}})
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Graph struct {
			Edges []Edge `json:"edges"`
		} `json:"graph"`
	}
	if err := json.Unmarshal(data, &back); err != nil || back.Graph.Edges == nil {
		t.Fatalf("edges not an empty list: %s", data)
	}
}
