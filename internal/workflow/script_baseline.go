package workflow

// This file holds the drift guard baseline reader of a script_parse node (spec
// #45, REQ-DG-001, REQ-DG-007, design section "Engine"). The engine uses it to
// check a script output against the node's recent good outputs (profile drift)
// and to decide whether the next run is an audit.

import "context"

// scriptBaselineLimit is how many newest runs the reader scans (design
// "Engine").
const scriptBaselineLimit = 30

// scriptBaselineMax is how many good outputs a baseline keeps (REQ-DG-001).
const scriptBaselineMax = 5

// scriptBaseline is the drift guard baseline of one script_parse node: its good
// outputs newest first and the number of script runs since the newest fallback
// or audit run (REQ-DG-001, REQ-DG-007).
type scriptBaseline struct {
	outputs    []any
	sinceCheck int
}

// baseline returns the cached baseline of a node, reading it from the store on
// first use (REQ-DG-001). The cache keeps the store read to one per node per run.
func (ex *runExec) baseline(ctx context.Context, nodeID string) ([]any, int) {
	if ex.baselines == nil {
		ex.baselines = map[string]scriptBaseline{}
	}
	if b, ok := ex.baselines[nodeID]; ok {
		return b.outputs, b.sinceCheck
	}
	outputs, since := loadScriptBaseline(ctx, ex.e.store, ex.wf.ID, nodeID, ex.run.ID)
	ex.baselines[nodeID] = scriptBaseline{outputs: outputs, sinceCheck: since}
	return outputs, since
}

// loadScriptBaseline reads the baseline profile of a script_parse node and the
// count of script runs since the node's newest fallback or audit run
// (REQ-DG-001, REQ-DG-007). It scans the newest scriptBaselineLimit runs of the
// workflow newest first and skips skipRunID. A good output is a succeeded node
// run of nodeID with via script, fallback or audit; the baseline keeps the
// newest scriptBaselineMax good outputs from runs numbered at or after the
// newest profile_accept decision of the node, or from any run when there is
// none. The count runs over the same runs, ignoring the profile_accept cut, and
// stops at the newest fallback or audit run. A store error yields no baseline
// and a zero count, so the node still runs.
func loadScriptBaseline(ctx context.Context, st Store, wfID, nodeID, skipRunID string) ([]any, int) {
	runs, err := st.ListRuns(ctx, RunFilter{WorkflowID: wfID, Limit: scriptBaselineLimit})
	if err != nil {
		return nil, 0
	}
	cut, err := profileAcceptCut(ctx, st, wfID, nodeID)
	if err != nil {
		return nil, 0
	}

	var outputs []any
	for _, run := range runs {
		if run.ID == skipRunID || run.Number < cut {
			continue
		}
		if len(outputs) >= scriptBaselineMax {
			break
		}
		nr, ok := nodeRunOf(ctx, st, run.ID, nodeID)
		if !ok || nr.Status != NodeSucceeded || !goodScriptVia(nr.Via) {
			continue
		}
		outputs = append(outputs, normalize(nr.Output))
	}

	sinceCheck := 0
countLoop:
	for _, run := range runs {
		if run.ID == skipRunID {
			continue
		}
		nr, ok := nodeRunOf(ctx, st, run.ID, nodeID)
		if !ok {
			continue
		}
		switch nr.Via {
		case ViaScript:
			sinceCheck++
		case ViaFallback, ViaAudit:
			break countLoop
		}
	}
	return outputs, sinceCheck
}

// goodScriptVia reports whether a via value names a good output (REQ-DG-001).
func goodScriptVia(via string) bool {
	return via == ViaScript || via == ViaFallback || via == ViaAudit
}

// nodeRunOf returns the node run of nodeID in one run. The first match wins;
// ok is false when the run has no such node run or the store errors.
func nodeRunOf(ctx context.Context, st Store, runID, nodeID string) (NodeRun, bool) {
	nrs, err := st.ListNodeRuns(ctx, runID)
	if err != nil {
		return NodeRun{}, false
	}
	for _, nr := range nrs {
		if nr.NodeID == nodeID {
			return nr, true
		}
	}
	return NodeRun{}, false
}

// profileAcceptCut returns the run number of the newest profile_accept decision
// of a node, or 0 when there is none (REQ-DG-001). Runs numbered at or after it
// form the baseline, so a confirmed profile change restarts the baseline at the
// run that accepted it.
func profileAcceptCut(ctx context.Context, st Store, wfID, nodeID string) (int, error) {
	decs, err := st.ListDecisions(ctx, DecisionFilter{WorkflowID: wfID})
	if err != nil {
		return 0, err
	}
	for _, d := range decs {
		if d.Kind == "script" && d.Action == "profile_accept" && d.NodeID == nodeID {
			return d.RunNumber, nil
		}
	}
	return 0, nil
}
