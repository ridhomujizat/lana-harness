package workflow

// This file holds the candidate operations of the guardian chat (spec 51,
// design "Part A: service"). The guardian saves a graph as a candidate, tests
// it with candidate runs, then publishes it as a new version or discards it, so
// the current version never breaks while the guardian experiments.

import (
	"context"
	"encoding/json"
	"errors"
)

// candidateTrigger is the trigger of a guardian test run (REQ-WFC-005).
const candidateTrigger = "test"

// SaveCandidate validates g and stores it as the workflow's open candidate,
// based on the current version (REQ-WFC-003). Invalid graphs return the issues
// and store nothing (err nil). The returned change is the diff current →
// candidate: FromVersion is the base version and ToVersion stays 0, since a
// candidate is not a version yet. An empty author is "guardian".
func (s *Service) SaveCandidate(ctx context.Context, id string, g Graph, summary, author string) (Candidate, GraphChange, []Issue, error) {
	wf, err := s.store.GetWorkflow(ctx, id)
	if err != nil {
		return Candidate{}, GraphChange{}, nil, err
	}
	cur, err := s.store.GetVersion(ctx, id, wf.Version)
	if err != nil {
		return Candidate{}, GraphChange{}, nil, err
	}
	graph := g.Normalized()
	if issues := Validate(graph, s.deps.AgentExists); len(issues) > 0 {
		return Candidate{}, GraphChange{}, issues, nil
	}
	if author == "" {
		author = "guardian"
	}
	c, err := s.store.CreateCandidate(ctx, Candidate{
		WorkflowID:  id,
		BaseVersion: wf.Version,
		Graph:       graph,
		Summary:     summary,
		Author:      author,
	})
	if err != nil {
		return Candidate{}, GraphChange{}, nil, err
	}
	change := DiffGraphs(cur.Graph, graph)
	change.FromVersion = wf.Version
	change.Summary = summary
	s.broadcast(id, "")
	return c, change, nil, nil
}

// OpenCandidate returns the open candidate (ok=false when none). It returns
// ErrNotFound for an unknown workflow.
func (s *Service) OpenCandidate(ctx context.Context, id string) (Candidate, bool, error) {
	if _, err := s.store.GetWorkflow(ctx, id); err != nil {
		return Candidate{}, false, err
	}
	c, err := s.store.OpenCandidate(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return Candidate{}, false, nil
	}
	if err != nil {
		return Candidate{}, false, err
	}
	return c, true, nil
}

// TestCandidate starts a run of the open candidate with trigger "test" (or of
// the current version when there is none, candidate_id 0). It refuses with
// ErrRunActive like StartRun. It does not wait (REQ-WFC-005).
func (s *Service) TestCandidate(ctx context.Context, id string, input json.RawMessage) (Run, error) {
	c, ok, err := s.OpenCandidate(ctx, id)
	if err != nil {
		return Run{}, err
	}
	if !ok {
		return s.startRun(ctx, id, candidateTrigger, input, nil)
	}
	return s.startRun(ctx, id, candidateTrigger, input, &c)
}

// PublishCandidate saves the open candidate as a new version with
// SaveVersion(base = candidate.BaseVersion, author "guardian") (REQ-WFC-006).
// It returns ErrVersionConflict when the current version moved, ErrNotFound
// when there is no open candidate. On success the candidate is "published", an
// active workflow whose lock no longer covers the graph goes back to draft, and
// the change (diff of the base version → new version, From/To set, Summary) is
// returned. It broadcasts the change.
func (s *Service) PublishCandidate(ctx context.Context, id string) (GraphChange, Version, error) {
	c, err := s.store.OpenCandidate(ctx, id)
	if err != nil {
		return GraphChange{}, Version{}, err
	}
	base, err := s.store.GetVersion(ctx, id, c.BaseVersion)
	if err != nil {
		return GraphChange{}, Version{}, err
	}
	summary := c.Summary
	if summary == "" {
		summary = "guardian change"
	}
	newVer, err := s.store.SaveVersion(ctx, id, c.BaseVersion, c.Graph, "guardian", summary)
	if err != nil {
		return GraphChange{}, Version{}, err
	}
	now := s.now().UnixMilli()
	c.Status = CandidatePublished
	c.PublishedVersion = newVer.Version
	c.UpdatedAt = now
	if err := s.store.UpdateCandidate(ctx, c); err != nil {
		return GraphChange{}, Version{}, err
	}
	if current, err := s.store.GetWorkflow(ctx, id); err == nil {
		if current.Status == StatusActive && !LockCovers(current.Lock, c.Graph) {
			current.Status = StatusDraft
			current.UpdatedAt = now
			_ = s.store.UpdateWorkflow(ctx, current)
		}
	}
	change := DiffGraphs(base.Graph, c.Graph)
	change.FromVersion = c.BaseVersion
	change.ToVersion = newVer.Version
	change.Summary = c.Summary
	s.broadcast(id, "")
	return change, newVer, nil
}

// DiscardCandidate marks the open candidate discarded (no-op when none).
func (s *Service) DiscardCandidate(ctx context.Context, id string) error {
	c, err := s.store.OpenCandidate(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	c.Status = CandidateDiscarded
	c.UpdatedAt = s.now().UnixMilli()
	if err := s.store.UpdateCandidate(ctx, c); err != nil {
		return err
	}
	s.broadcast(id, "")
	return nil
}

// SetAssistantSession stores the guardian chat session id on the workflow
// (REQ-WFC-001).
func (s *Service) SetAssistantSession(ctx context.Context, id, sessionID string) error {
	wf, err := s.store.GetWorkflow(ctx, id)
	if err != nil {
		return err
	}
	wf.AssistantSession = sessionID
	if err := s.store.UpdateWorkflow(ctx, wf); err != nil {
		return err
	}
	s.broadcast(id, "")
	return nil
}

// AssistantWorkflow returns the workflow whose assistant_session is sessionID
// (REQ-WFC-003). An empty session id matches nothing.
func (s *Service) AssistantWorkflow(ctx context.Context, sessionID string) (Workflow, bool) {
	if sessionID == "" {
		return Workflow{}, false
	}
	wfs, err := s.store.ListWorkflows(ctx)
	if err != nil {
		return Workflow{}, false
	}
	for _, wf := range wfs {
		if wf.AssistantSession == sessionID {
			return wf, true
		}
	}
	return Workflow{}, false
}

// AppendChat stores a chat message (exported for the tool's change card,
// REQ-WFC-006). A zero CreatedAt becomes the current time.
func (s *Service) AppendChat(ctx context.Context, m ChatMessage) (ChatMessage, error) {
	if m.CreatedAt == 0 {
		m.CreatedAt = s.now().UnixMilli()
	}
	stored, err := s.store.AppendChat(ctx, m)
	if err != nil {
		return ChatMessage{}, err
	}
	s.broadcast(m.WorkflowID, "")
	return stored, nil
}
