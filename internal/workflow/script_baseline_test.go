package workflow

// Tests for the drift guard baseline reader (spec #45, REQ-DG-001, REQ-DG-007).
// They seed runs and node runs directly in a MemStore, so the reader sees the
// same shapes the engine writes.

import (
	"context"
	"testing"
)

// seedBaselineRun stores a run whose parse node run carries the given output,
// status and via, so loadScriptBaseline can read it. seedRun assigns the next
// run number.
func seedBaselineRun(t *testing.T, st *MemStore, wf Workflow, out any, status NodeStatus, via string) Run {
	t.Helper()
	run := seedRun(t, st, wf, "manual", nil)
	if err := st.PutNodeRun(context.Background(), NodeRun{
		RunID: run.ID, NodeID: "parse", ItemIndex: -1,
		Status: status, Via: via, Output: nodeJSON(out),
	}); err != nil {
		t.Fatalf("put node run: %v", err)
	}
	return run
}

// TestLoadScriptBaselineCut covers the profile_accept cut of REQ-DG-001: only
// runs numbered at or after the newest profile_accept decision join the
// baseline, newest first.
func TestLoadScriptBaselineCut(t *testing.T) {
	st := NewMemStore()
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, scriptGraph(nil))
	for i := 1; i <= 6; i++ {
		seedBaselineRun(t, st, wf, map[string]any{"n": i}, NodeSucceeded, ViaScript)
	}
	if err := st.PutDecision(context.Background(), Decision{
		ID: "d_accept", WorkflowID: "w", NodeID: "parse", Kind: "script",
		Action: "profile_accept", RunNumber: 4, CreatedAt: 20,
	}); err != nil {
		t.Fatal(err)
	}

	got, since := loadScriptBaseline(context.Background(), st, "w", "parse", "")
	if len(got) != 3 {
		t.Fatalf("baseline=%v want 3 outputs (runs 4..6)", got)
	}
	for i, want := range []int{6, 5, 4} {
		if n := got[i].(map[string]any)["n"]; n != float64(want) {
			t.Fatalf("baseline[%d]=%v want run %d", i, n, want)
		}
	}
	if since != 6 {
		t.Fatalf("sinceCheck=%d want 6 (the cut is ignored)", since)
	}
}

// TestLoadScriptBaselineCap covers the 5-output cap of REQ-DG-001.
func TestLoadScriptBaselineCap(t *testing.T) {
	st := NewMemStore()
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, scriptGraph(nil))
	for i := 1; i <= 7; i++ {
		seedBaselineRun(t, st, wf, map[string]any{"n": i}, NodeSucceeded, ViaScript)
	}

	got, _ := loadScriptBaseline(context.Background(), st, "w", "parse", "")
	if len(got) != scriptBaselineMax {
		t.Fatalf("baseline=%d want %d", len(got), scriptBaselineMax)
	}
	if n := got[0].(map[string]any)["n"]; n != float64(7) {
		t.Fatalf("baseline[0]=%v want the newest run 7", n)
	}
	if n := got[4].(map[string]any)["n"]; n != float64(3) {
		t.Fatalf("baseline[4]=%v want run 3", n)
	}
}

// TestLoadScriptBaselineSkipRun covers the skipRunID skip: the current run is
// not part of its own baseline.
func TestLoadScriptBaselineSkipRun(t *testing.T) {
	st := NewMemStore()
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, scriptGraph(nil))
	seedBaselineRun(t, st, wf, map[string]any{"n": 1}, NodeSucceeded, ViaScript)
	run2 := seedBaselineRun(t, st, wf, map[string]any{"n": 2}, NodeSucceeded, ViaScript)
	seedBaselineRun(t, st, wf, map[string]any{"n": 3}, NodeSucceeded, ViaScript)

	got, since := loadScriptBaseline(context.Background(), st, "w", "parse", run2.ID)
	if len(got) != 2 {
		t.Fatalf("baseline=%v want 2 outputs", got)
	}
	if n := got[0].(map[string]any)["n"]; n != float64(3) {
		t.Fatalf("baseline[0]=%v want run 3", n)
	}
	if n := got[1].(map[string]any)["n"]; n != float64(1) {
		t.Fatalf("baseline[1]=%v want run 1", n)
	}
	if since != 2 {
		t.Fatalf("sinceCheck=%d want 2", since)
	}
}

// TestLoadScriptBaselineSinceCheck covers the audit count of REQ-DG-007: it
// counts script runs newest first and stops at the newest fallback or audit run.
func TestLoadScriptBaselineSinceCheck(t *testing.T) {
	cases := []struct {
		name string
		vias []string
		want int
	}{
		{"all script", []string{ViaScript, ViaScript, ViaScript}, 3},
		{"stops at fallback", []string{ViaScript, ViaFallback, ViaScript, ViaScript, ViaScript}, 3},
		{"stops at audit immediately", []string{ViaScript, ViaScript, ViaAudit}, 0},
		{"empty via does not count or stop", []string{ViaScript, "", ViaScript}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := NewMemStore()
			wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "W", Guardian: "g"}, scriptGraph(nil))
			for i, via := range tc.vias {
				seedBaselineRun(t, st, wf, map[string]any{"n": i}, NodeSucceeded, via)
			}
			_, since := loadScriptBaseline(context.Background(), st, "w", "parse", "")
			if since != tc.want {
				t.Fatalf("sinceCheck=%d want %d (vias %v)", since, tc.want, tc.vias)
			}
		})
	}
}

// TestLoadScriptBaselineStoreError covers the store error path: no baseline and
// a zero count, so the node still runs (REQ-DG-001).
func TestLoadScriptBaselineStoreError(t *testing.T) {
	got, since := loadScriptBaseline(context.Background(), errStore{}, "w", "parse", "")
	if got != nil || since != 0 {
		t.Fatalf("baseline=%v since=%d want nil/0", got, since)
	}
}

// errStore is a Store whose run read always fails.
type errStore struct{ Store }

func (errStore) ListRuns(context.Context, RunFilter) ([]Run, error) {
	return nil, ErrNotFound
}
