package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The guardian chat session of spec 51: one persistent agent session per
// workflow (REQ-WFC-001), a hidden context note before each owner message
// (REQ-WFC-002), and the workflow_builder operations the guardian edits and
// tests the workflow with (REQ-WFC-003 to REQ-WFC-006).

// ErrNotAssistantSession is returned when a workflow_builder call does not
// come from a workflow's guardian chat session.
var ErrNotAssistantSession = errors.New("workflow: not a workflow guardian chat session")

// ErrAssistantUnavailable is returned when the runtime has no guardian chat
// sessions (Deps without AssistantRuntime).
var ErrAssistantUnavailable = errors.New("workflow: the guardian chat is not available")

// assistNote is what the last note told a guardian chat session.
type assistNote struct {
	version int
	runID   string
}

// AssistantChat returns the workflow's guardian chat session id ("" before
// the first use) and its chat messages: change cards and the messages from
// before spec 51 (API-WF-017 revision).
func (s *Service) AssistantChat(ctx context.Context, id string) (string, []ChatMessage, error) {
	wf, err := s.store.GetWorkflow(ctx, id)
	if err != nil {
		return "", nil, err
	}
	msgs, err := s.store.ListChat(ctx, id, 200)
	if err != nil {
		return "", nil, err
	}
	sid := wf.AssistantSession
	if rt, ok := s.deps.(AssistantRuntime); ok && sid != "" && !rt.AssistantSessionExists(ctx, sid, wf.Guardian) {
		sid = ""
	}
	return sid, msgs, nil
}

// EnsureAssistantSession returns the workflow's guardian chat session,
// creating it on first use or when the old one was deleted (REQ-WFC-001).
func (s *Service) EnsureAssistantSession(ctx context.Context, id string) (string, error) {
	rt, ok := s.deps.(AssistantRuntime)
	if !ok {
		return "", ErrAssistantUnavailable
	}
	lock := s.runLock("assistant:" + id)
	lock.Lock()
	defer lock.Unlock()
	wf, err := s.store.GetWorkflow(ctx, id)
	if err != nil {
		return "", err
	}
	if wf.AssistantSession != "" && rt.AssistantSessionExists(ctx, wf.AssistantSession, wf.Guardian) {
		return wf.AssistantSession, nil
	}
	return s.newAssistantSessionLocked(ctx, rt, wf)
}

// NewAssistantSession replaces the workflow's guardian chat session with a
// fresh one (REQ-WFC-001, the /new command). The old session stays in the
// history.
func (s *Service) NewAssistantSession(ctx context.Context, id string) (string, error) {
	rt, ok := s.deps.(AssistantRuntime)
	if !ok {
		return "", ErrAssistantUnavailable
	}
	lock := s.runLock("assistant:" + id)
	lock.Lock()
	defer lock.Unlock()
	wf, err := s.store.GetWorkflow(ctx, id)
	if err != nil {
		return "", err
	}
	return s.newAssistantSessionLocked(ctx, rt, wf)
}

func (s *Service) newAssistantSessionLocked(ctx context.Context, rt AssistantRuntime, wf Workflow) (string, error) {
	sid, err := rt.CreateAssistantSession(ctx, wf)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrGuardianUnavailable, err)
	}
	if err := s.SetAssistantSession(ctx, wf.ID, sid); err != nil {
		return "", err
	}
	s.broadcast(wf.ID, "")
	return sid, nil
}

// AssistantNote builds the hidden context note for the next owner message on
// the guardian chat session (REQ-WFC-002): the full guide the first time this
// process talks to the session, later only what changed. commit records the
// note as sent; call it once the message was accepted.
func (s *Service) AssistantNote(ctx context.Context, id, sessionID string) (note string, commit func(), err error) {
	wf, err := s.store.GetWorkflow(ctx, id)
	if err != nil {
		return "", nil, err
	}
	ver, err := s.store.GetVersion(ctx, id, wf.Version)
	if err != nil {
		return "", nil, err
	}
	last := s.lastRunDetail(ctx, id)
	lastID := ""
	if last != nil {
		lastID = last.Run.ID
	}

	s.assistMu.Lock()
	prev, seen := s.assistNoted[sessionID]
	s.assistMu.Unlock()

	switch {
	case !seen:
		var env AssistEnv
		if provider, ok := s.deps.(AssistEnvProvider); ok {
			ectx, ecancel := context.WithTimeout(ctx, assistEnvTimeout)
			env = provider.AssistEnv(ectx, wf)
			ecancel()
		}
		var cand *Candidate
		if c, ok, err := s.OpenCandidate(ctx, id); err == nil && ok {
			cand = &c
		}
		note = BuildAssistantGuide(wf, ver.Graph, env, last, cand)
	default:
		var g *Graph
		if prev.version != wf.Version {
			graph := ver.Graph
			g = &graph
		}
		var newer *RunDetail
		if lastID != "" && lastID != prev.runID {
			newer = last
		}
		note = BuildAssistantDelta(wf, g, newer)
	}
	sent := assistNote{version: wf.Version, runID: lastID}
	commit = func() { s.markAssistNoted(sessionID, sent) }
	return note, commit, nil
}

// markAssistNoted records what a session was told.
func (s *Service) markAssistNoted(sessionID string, n assistNote) {
	s.assistMu.Lock()
	defer s.assistMu.Unlock()
	if s.assistNoted == nil {
		s.assistNoted = map[string]assistNote{}
	}
	s.assistNoted[sessionID] = n
}

// noteSeen updates one field of what a session already knows, so the next
// note does not repeat what a tool result just showed it.
func (s *Service) noteSeen(sessionID string, update func(*assistNote)) {
	s.assistMu.Lock()
	defer s.assistMu.Unlock()
	n, ok := s.assistNoted[sessionID]
	if !ok {
		return
	}
	update(&n)
	s.assistNoted[sessionID] = n
}

// lastRunDetail returns the newest run of the workflow, or nil.
func (s *Service) lastRunDetail(ctx context.Context, id string) *RunDetail {
	runs, err := s.store.ListRuns(ctx, RunFilter{WorkflowID: id, Limit: 1})
	if err != nil || len(runs) == 0 {
		return nil
	}
	detail, err := s.RunDetail(ctx, id, runs[0].ID)
	if err != nil {
		return nil
	}
	return &detail
}

// Assist runs one guardian chat turn and waits for it (API-WF-017,
// REQ-WFC-009). The main-chat workflow tool uses it for create and edit; the
// web sends through the session instead. The result carries the guardian's
// final text and the last change it published in that turn.
func (s *Service) Assist(ctx context.Context, id string, baseVersion int, message string) (AssistResult, error) {
	rt, ok := s.deps.(AssistantRuntime)
	if !ok {
		return AssistResult{}, ErrAssistantUnavailable
	}
	wf, err := s.store.GetWorkflow(ctx, id)
	if err != nil {
		return AssistResult{}, err
	}
	if baseVersion != 0 && baseVersion != wf.Version {
		return AssistResult{}, ErrVersionConflict
	}
	sid, err := s.EnsureAssistantSession(ctx, id)
	if err != nil {
		return AssistResult{}, err
	}
	note, commit, err := s.AssistantNote(ctx, id, sid)
	if err != nil {
		return AssistResult{}, err
	}
	started := s.now().UnixMilli()
	actx, cancel := context.WithTimeout(ctx, assistTimeout)
	defer cancel()
	commit()
	res, err := rt.RunAssistantTurn(actx, sid, note, message, nil)
	var background *GuardianBackgroundError
	if errors.As(err, &background) {
		return AssistResult{}, background
	}
	if err != nil {
		return AssistResult{}, fmt.Errorf("%w: %v", ErrGuardianUnavailable, err)
	}
	msg := ChatMessage{
		WorkflowID: id, Role: "guardian", Text: strings.TrimSpace(res.Text),
		SessionID: sid, CreatedAt: s.now().UnixMilli(),
	}
	if msgs, err := s.store.ListChat(ctx, id, 20); err == nil {
		for i := len(msgs) - 1; i >= 0; i-- {
			m := msgs[i]
			if m.SessionID == sid && m.Change != nil && m.CreatedAt >= started {
				msg.Change = m.Change
				msg.TurnID = m.TurnID
				break
			}
		}
	}
	detail, err := s.Get(ctx, id)
	if err != nil {
		return AssistResult{}, err
	}
	return AssistResult{Message: msg, Workflow: detail, Tokens: res.Tokens}, nil
}

// ---- workflow_builder operations (REQ-WFC-003) ----------------------------

// BuilderView is what workflow_builder get returns: the current version and
// the open candidate, if any.
type BuilderView struct {
	Workflow  Workflow   `json:"workflow"`
	Current   Version    `json:"current"`
	Candidate *Candidate `json:"candidate,omitempty"`
}

// assistantWorkflow resolves the workflow of a guardian chat session.
func (s *Service) assistantWorkflow(ctx context.Context, sessionID string) (Workflow, error) {
	wf, ok := s.AssistantWorkflow(ctx, sessionID)
	if !ok {
		return Workflow{}, ErrNotAssistantSession
	}
	return wf, nil
}

// BuilderGet returns the current graph and the open candidate of the
// session's workflow.
func (s *Service) BuilderGet(ctx context.Context, sessionID string) (BuilderView, error) {
	wf, err := s.assistantWorkflow(ctx, sessionID)
	if err != nil {
		return BuilderView{}, err
	}
	ver, err := s.store.GetVersion(ctx, wf.ID, wf.Version)
	if err != nil {
		return BuilderView{}, err
	}
	view := BuilderView{Workflow: wf, Current: ver}
	if c, ok, err := s.OpenCandidate(ctx, wf.ID); err == nil && ok {
		view.Candidate = &c
	}
	s.noteSeen(sessionID, func(n *assistNote) { n.version = wf.Version })
	return view, nil
}

// BuilderSave stores g as the open candidate of the session's workflow.
func (s *Service) BuilderSave(ctx context.Context, sessionID string, g Graph, summary string) (Candidate, GraphChange, []Issue, error) {
	wf, err := s.assistantWorkflow(ctx, sessionID)
	if err != nil {
		return Candidate{}, GraphChange{}, nil, err
	}
	return s.SaveCandidate(ctx, wf.ID, g, summary, "guardian")
}

// BuilderTest starts a test run of the open candidate (or the current
// version) of the session's workflow. It does not wait.
func (s *Service) BuilderTest(ctx context.Context, sessionID string, input json.RawMessage) (Run, error) {
	wf, err := s.assistantWorkflow(ctx, sessionID)
	if err != nil {
		return Run{}, err
	}
	run, err := s.TestCandidate(ctx, wf.ID, input)
	if err != nil {
		return Run{}, err
	}
	// The tool result shows this run, so the next note need not repeat it.
	s.noteSeen(sessionID, func(n *assistNote) { n.runID = run.ID })
	return run, nil
}

// BuilderPublish makes the open candidate the current version and stores the
// change card for the publishing turn (REQ-WFC-006).
func (s *Service) BuilderPublish(ctx context.Context, sessionID, turnID string) (ChatMessage, error) {
	wf, err := s.assistantWorkflow(ctx, sessionID)
	if err != nil {
		return ChatMessage{}, err
	}
	change, ver, err := s.PublishCandidate(ctx, wf.ID)
	if err != nil {
		return ChatMessage{}, err
	}
	text := change.Summary
	if text == "" {
		text = ver.Summary
	}
	msg, err := s.AppendChat(ctx, ChatMessage{
		WorkflowID: wf.ID, Role: "guardian", Text: text, Change: &change,
		SessionID: sessionID, TurnID: turnID, CreatedAt: s.now().UnixMilli(),
	})
	if err != nil {
		return ChatMessage{}, err
	}
	s.noteSeen(sessionID, func(n *assistNote) { n.version = ver.Version })
	s.broadcast(wf.ID, "")
	return msg, nil
}

// BuilderDiscard drops the open candidate of the session's workflow.
func (s *Service) BuilderDiscard(ctx context.Context, sessionID string) error {
	wf, err := s.assistantWorkflow(ctx, sessionID)
	if err != nil {
		return err
	}
	return s.DiscardCandidate(ctx, wf.ID)
}

// BuilderRun returns one run of the session's workflow, for the test wait.
func (s *Service) BuilderRun(ctx context.Context, sessionID, runID string) (RunDetail, error) {
	wf, err := s.assistantWorkflow(ctx, sessionID)
	if err != nil {
		return RunDetail{}, err
	}
	return s.RunDetail(ctx, wf.ID, runID)
}
