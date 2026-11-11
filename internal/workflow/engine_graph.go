package workflow

// This file routes every graph read and write-back of a run through one place
// (spec 51, design "Part A: engine rules for candidate runs"). A normal run
// reads its version graph and writes back by saving a new version; a candidate
// test run (Run.CandidateID > 0) reads the candidate graph and patches the
// candidate, so the current version never changes while the guardian tests.

import (
	"context"
	"errors"
)

// runGraph returns the graph run executes: the candidate's graph for a
// candidate test run, else the graph of the run's version (REQ-WFC-005).
func runGraph(ctx context.Context, st Store, run Run) (Graph, error) {
	if run.CandidateID > 0 {
		c, err := st.GetCandidate(ctx, run.CandidateID)
		if err != nil {
			return Graph{}, err
		}
		return c.Graph, nil
	}
	ver, err := st.GetVersion(ctx, run.WorkflowID, run.Version)
	if err != nil {
		return Graph{}, err
	}
	return ver.Graph, nil
}

// graphEdit changes g in place. It returns the summary of the saved version, or
// a non-empty drop reason to save nothing, or an error to abort.
type graphEdit func(g *Graph) (summary, drop string, err error)

// patchGraph applies edit to the graph a write-back targets and stores the
// result (REQ-WFC-005). With candidateID > 0 it patches that candidate with
// UpdateCandidate and saves no version. Otherwise it loads the workflow's
// current version, applies edit to it and saves a new version by author; on
// ErrVersionConflict it reloads and tries again, up to attempts tries, and
// returns ErrVersionConflict when every try conflicted. It returns the drop
// reason edit gave, and publishes a workflow change after a save.
func (e *Engine) patchGraph(ctx context.Context, wfID string, candidateID int64, author string, attempts int, edit graphEdit) (string, error) {
	if candidateID > 0 {
		c, err := e.store.GetCandidate(ctx, candidateID)
		if err != nil {
			return "", err
		}
		g := cloneGraph(c.Graph)
		_, drop, err := edit(&g)
		if err != nil || drop != "" {
			return drop, err
		}
		c.Graph = g
		c.UpdatedAt = e.now().UnixMilli()
		if err := e.store.UpdateCandidate(ctx, c); err != nil {
			return "", err
		}
		e.changed(wfID, "")
		return "", nil
	}
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 0; attempt < attempts; attempt++ {
		wf, err := e.store.GetWorkflow(ctx, wfID)
		if err != nil {
			return "", err
		}
		cur, err := e.store.GetVersion(ctx, wfID, wf.Version)
		if err != nil {
			return "", err
		}
		g := cloneGraph(cur.Graph)
		summary, drop, err := edit(&g)
		if err != nil || drop != "" {
			return drop, err
		}
		if _, err := e.store.SaveVersion(ctx, wfID, wf.Version, g, author, summary); err != nil {
			if errors.Is(err, ErrVersionConflict) {
				continue
			}
			return "", err
		}
		e.changed(wfID, "")
		return "", nil
	}
	return "", ErrVersionConflict
}
