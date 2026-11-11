package workflow

// This file holds the workflow list overview (API-WF-001, REQ-WF-011): the
// per-workflow summary rows, the aggregate stats and the Needs attention list
// of pending decisions.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Overview builds the list page payload of API-WF-001 (REQ-WF-001,
// REQ-WF-008, REQ-WF-011).
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	wfs, err := s.store.ListWorkflows(ctx)
	if err != nil {
		return Overview{}, err
	}
	now := s.now()
	since7 := now.Add(-7 * 24 * time.Hour).UnixMilli()
	since14 := now.Add(-14 * 24 * time.Hour).UnixMilli()
	allRuns, _ := s.store.ListRuns(ctx, RunFilter{Since: since14})

	overview := Overview{Workflows: []Summary{}, Attention: []Attention{}}
	names := make(map[string]string, len(wfs))
	for _, wf := range wfs {
		names[wf.ID] = wf.Name
		overview.Stats.Total++
		switch wf.Status {
		case StatusActive:
			overview.Stats.Active++
		case StatusPaused:
			overview.Stats.Paused++
		case StatusError:
			overview.Stats.Error++
		}
		ver, err := s.store.GetVersion(ctx, wf.ID, wf.Version)
		if err != nil {
			continue
		}
		runs, _ := s.store.ListRuns(ctx, RunFilter{WorkflowID: wf.ID, Since: since7})
		overview.Workflows = append(overview.Workflows, summarize(wf, ver.Graph, runs))
	}

	overview.Stats.Success7d = successRate(allRuns, since7, 0)
	overview.Stats.SuccessPrev7d = successRate(allRuns, since14, since7)
	overview.Stats.RunsToday, overview.Stats.RunsTodayOK, overview.Stats.RunsTodayFailed, overview.Stats.RunsTodayRunning = runsToday(allRuns, now)

	pending, _ := s.store.ListDecisions(ctx, DecisionFilter{Status: "pending"})
	nameCache := map[string]string{}
	for _, d := range pending {
		switch d.Kind {
		case "approval":
			overview.Stats.Approvals++
		case "guardian":
			overview.Stats.Decisions++
		}
		overview.Attention = append(overview.Attention, Attention{
			Decision:     d,
			WorkflowName: names[d.WorkflowID],
			NodeName:     s.decisionNodeName(ctx, d, nameCache),
		})
	}
	overview.Stats.WaitingOnYou = overview.Stats.Approvals + overview.Stats.Decisions
	return overview, nil
}

// decisionNodeName resolves the node name of a decision from the version graph
// of its run, falling back to the node id (API-WF-001).
func (s *Service) decisionNodeName(ctx context.Context, d Decision, cache map[string]string) string {
	key := d.RunID + "\x00" + d.NodeID
	if name, ok := cache[key]; ok {
		return name
	}
	name := d.NodeID
	if run, err := s.store.GetRun(ctx, d.RunID); err == nil {
		if g, err := runGraph(ctx, s.store, run); err == nil {
			name = graphNodeName(g, d.NodeID)
		}
	}
	cache[key] = name
	return name
}

// summarize builds one workflow_summary row from its current graph and runs of
// the last seven days.
func summarize(wf Workflow, graph Graph, runs []Run) Summary {
	sum := Summary{
		ID:          wf.ID,
		Name:        wf.Name,
		Description: wf.Description,
		Guardian:    wf.Guardian,
		Status:      wf.Status,
		Version:     wf.Version,
		NodeCount:   len(graph.Nodes),
		Triggers:    []TriggerInfo{},
		Bots:        workflowBots(wf, graph),
		UpdatedAt:   wf.UpdatedAt,
	}
	for _, n := range graph.Nodes {
		if spec, ok := Spec(n.Type); ok && spec.Trigger {
			sum.Triggers = append(sum.Triggers, triggerInfo(wf.ID, n))
		}
	}

	sum.Runs7d = len(runs)
	ok, failed := 0, 0
	var newest *Run
	for i := range runs {
		r := runs[i]
		switch r.Status {
		case RunRunning:
			sum.Running = true
		case RunWaiting:
			sum.Waiting = true
		case RunSucceeded:
			ok++
		case RunFailed:
			failed++
		}
		if newest == nil || r.Number > newest.Number {
			copied := r
			newest = &copied
		}
	}
	if total := ok + failed; total > 0 {
		rate := float64(ok) / float64(total)
		sum.Success7d = &rate
	}
	sum.LastRun = newest
	return sum
}

// triggerInfo renders one trigger row of a summary (contract workflow_summary).
func triggerInfo(workflowID string, n Node) TriggerInfo {
	info := TriggerInfo{Type: n.Type, Enabled: paramEnabled(n.Params)}
	switch n.Type {
	case "trigger.schedule":
		info.Label = "Schedule"
		cron, _ := n.Params["cron"].(string)
		info.Detail = DescribeCron(cron)
	case "trigger.interval":
		info.Label = "Interval"
		info.Detail = fmt.Sprintf("Every %d min", int(paramFloat(n.Params, "minutes", 0)))
	case "trigger.webhook":
		info.Label = "Webhook"
		info.Detail = "POST /hooks/" + workflowID
	case "trigger.event":
		info.Label = "Event"
		kind, _ := n.Params["kind"].(string)
		if agent, _ := n.Params["agent"].(string); agent != "" {
			kind += " · " + agent
		}
		info.Detail = kind
	case "trigger.chat":
		info.Label = "Chat command"
		command, _ := n.Params["command"].(string)
		info.Detail = "/" + strings.TrimPrefix(strings.TrimSpace(command), "/")
	case "trigger.manual":
		info.Label = "Run button"
		info.Detail = "From the Editor"
	}
	return info
}

// workflowBots lists the guardian then the other agents used by bot and worker
// nodes, without repeating the guardian (contract workflow_summary).
func workflowBots(wf Workflow, g Graph) []string {
	bots := []string{wf.Guardian}
	seen := map[string]bool{wf.Guardian: true}
	for _, n := range g.Nodes {
		if n.Type != "bot" && n.Type != "worker" {
			continue
		}
		agent, _ := n.Params["agent"].(string)
		if agent == "" {
			agent = wf.Guardian
		}
		if !seen[agent] {
			seen[agent] = true
			bots = append(bots, agent)
		}
	}
	return bots
}

// graphNodeName names a node in a graph, falling back to its id.
func graphNodeName(g Graph, nodeID string) string {
	for _, n := range g.Nodes {
		if n.ID == nodeID {
			return nodeName(n)
		}
	}
	return nodeID
}

// successRate returns succeeded/(succeeded+failed) for the runs started in
// [from, to); to == 0 means no upper bound. It is nil when nothing finished.
func successRate(runs []Run, from, to int64) *float64 {
	ok, failed := 0, 0
	for _, r := range runs {
		if r.StartedAt < from {
			continue
		}
		if to != 0 && r.StartedAt >= to {
			continue
		}
		switch r.Status {
		case RunSucceeded:
			ok++
		case RunFailed:
			failed++
		}
	}
	if ok+failed == 0 {
		return nil
	}
	rate := float64(ok) / float64(ok+failed)
	return &rate
}

// runsToday counts the runs started since local midnight, split by outcome.
func runsToday(runs []Run, now time.Time) (total, ok, failed, running int) {
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).UnixMilli()
	for _, r := range runs {
		if r.StartedAt < midnight {
			continue
		}
		total++
		switch r.Status {
		case RunSucceeded:
			ok++
		case RunFailed:
			failed++
		case RunRunning, RunQueued, RunWaiting:
			running++
		}
	}
	return total, ok, failed, running
}
