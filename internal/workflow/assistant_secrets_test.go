package workflow

import (
	"context"
	"strings"
	"testing"
)

// secretDeps adds the cheap secret-name list of spec 53 to assistDeps.
type secretDeps struct {
	*assistDeps
	names []string
}

func (d *secretDeps) AssistSecrets(context.Context) []string { return d.names }

// Spec 53 REQ-SEC-010: a secret the owner adds after the first note (for
// example through secret_request) is named in the next note, once.
func TestAssistantNoteNamesNewSecrets(t *testing.T) {
	st := NewMemStore()
	deps := &secretDeps{assistDeps: newAssistDeps(), names: []string{"GITHUB_TOKEN"}}
	deps.env = AssistEnv{Secrets: []string{"GITHUB_TOKEN"}}
	svc := newTestService(t, st, deps, &testClock{})
	ctx := context.Background()
	g := Graph{Nodes: []Node{wfNode("start", "trigger.manual", "Start", map[string]any{"enabled": true})}}
	seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, g)
	sid, err := svc.EnsureAssistantSession(ctx, "w")
	if err != nil {
		t.Fatal(err)
	}

	note, commit, err := svc.AssistantNote(ctx, "w", sid)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "GITHUB_TOKEN") || !strings.Contains(note, "secret_request") {
		t.Fatalf("first note misses the secrets or the secret_request rule:\n%s", note)
	}
	commit()
	if note, _, _ := svc.AssistantNote(ctx, "w", sid); note != "" {
		t.Fatalf("note with no new secret = %q", note)
	}

	deps.names = []string{"GITHUB_TOKEN", "STRIPE_KEY"}
	note, commit, _ = svc.AssistantNote(ctx, "w", sid)
	if note != "Secrets set by the owner now (names only): GITHUB_TOKEN, STRIPE_KEY." {
		t.Fatalf("delta note = %q", note)
	}
	commit()
	if note, _, _ := svc.AssistantNote(ctx, "w", sid); note != "" {
		t.Fatalf("the new secret was repeated: %q", note)
	}
}
