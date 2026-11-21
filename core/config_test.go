package core

import (
	"reflect"
	"testing"
)

// TestContextFileNamesDefaults covers REQ-PCX-001: an absent setting (nil)
// yields the default list.
func TestContextFileNamesDefaults(t *testing.T) {
	var a AgentConfig
	got := a.ContextFileNames()
	if !reflect.DeepEqual(got, DefaultContextFiles) {
		t.Fatalf("ContextFileNames() = %v, want %v", got, DefaultContextFiles)
	}
	// The result is a copy: mutating it must not change the default list.
	got[0] = "changed.md"
	if DefaultContextFiles[0] != "AGENTS.md" {
		t.Fatalf("DefaultContextFiles was mutated: %v", DefaultContextFiles)
	}
}

// TestContextFileNamesEmptyList covers REQ-PCX-001: an empty configured list
// turns the feature off and returns an empty, non-nil slice.
func TestContextFileNamesEmptyList(t *testing.T) {
	empty := []string{}
	a := AgentConfig{ContextFiles: &empty}
	got := a.ContextFileNames()
	if got == nil {
		t.Fatal("ContextFileNames() = nil, want an empty non-nil slice")
	}
	if len(got) != 0 {
		t.Fatalf("ContextFileNames() = %v, want an empty slice", got)
	}
}

// TestContextFileNamesConfigured covers REQ-PCX-001: a configured list is
// returned as a copy.
func TestContextFileNamesConfigured(t *testing.T) {
	list := []string{"AGENTS.md"}
	a := AgentConfig{ContextFiles: &list}
	got := a.ContextFileNames()
	if !reflect.DeepEqual(got, list) {
		t.Fatalf("ContextFileNames() = %v, want %v", got, list)
	}
	got[0] = "changed.md"
	if list[0] != "AGENTS.md" {
		t.Fatalf("the configured list was mutated: %v", list)
	}
}
