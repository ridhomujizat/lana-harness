package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

const (
	noSubagentsMessage   = "no subagents are configured for this agent"
	internalErrorMessage = "internal error"
	busySubagentMessage  = "subagent is busy"
	depthLimitMessage    = "maximum subagent depth reached"
	maxSubagentDepth     = 2
)

// subagentRunner runs child sessions for one turn of the calling session.
type subagentRunner struct {
	reg     *Registry
	allowed []string
}

var _ core.SubagentRunner = (*subagentRunner)(nil)

// Spawn creates a child session, runs one turn there, and returns its answer.
func (s *subagentRunner) Spawn(ctx context.Context, req core.SpawnRequest) (core.ToolOutput, error) {
	if ctx.Err() != nil {
		return stopToolOutput(ctx), nil
	}
	if len(s.allowed) == 0 {
		return toolError(noSubagentsMessage), nil
	}
	if !containsString(s.allowed, req.Agent) {
		return toolError(fmt.Sprintf("agent '%s' is not a subagent of this agent (allowed: %s)", req.Agent, strings.Join(s.allowed, ", "))), nil
	}
	parent, err := s.reg.store.Info(ctx, req.ParentSessionID)
	if err != nil {
		return toolError(internalErrorMessage), nil
	}
	if sessionDepth(ctx, s.reg.store, parent.ID) >= maxSubagentDepth {
		return toolError(depthLimitMessage), nil
	}
	if count, ok := s.reg.acquireChild(parent.ID); !ok {
		return toolError(fmt.Sprintf("%d subagents are already running", count)), nil
	}
	defer s.reg.releaseChild(parent.ID)
	child, err := s.reg.store.Create(ctx, core.NewSession{
		Agent:           req.Agent,
		Channel:         parent.Channel,
		Address:         parent.Address,
		Workspace:       parent.Workspace,
		ParentSessionID: parent.ID,
		ParentCallID:    req.ParentCallID,
	})
	if err != nil {
		return toolError(internalErrorMessage), nil
	}
	s.reg.hub.Link(parent.ID, child.ID)
	return s.runChildTurn(ctx, child, s.taskPrompt(ctx, parent.ID, req), time.Now())
}

// taskPrompt adds what the child builds on to a task prompt (spec 47 team
// work): the last answers of the earlier child sessions the lead named in
// Context, and the child's knowledge pages that match the task.
func (s *subagentRunner) taskPrompt(ctx context.Context, parentID string, req core.SpawnRequest) string {
	var b strings.Builder
	b.WriteString(req.Prompt)
	first := true
	for _, id := range req.Context {
		info, err := s.reg.store.Info(ctx, id)
		if err != nil || info.ParentSessionID != parentID {
			continue
		}
		answer := s.reg.lastAnswer(ctx, id)
		if answer == "" {
			continue
		}
		if first {
			b.WriteString("\n\nResults from teammates to build on:")
			first = false
		}
		fmt.Fprintf(&b, "\n\n### From %s\n%s", info.Agent, answer)
	}
	if req.WithNotes {
		b.WriteString("\n\nFinish the work before you answer: carry it to the end, check it, and report what is done. Do not hand the result to a background process and answer \"in progress\"; wait for it.")
		if notes := s.reg.relevantNotes(ctx, req.Prompt, []string{req.Agent})[req.Agent]; len(notes) > 0 {
			b.WriteString("\n\nYour knowledge pages that match this task (read them with knowledge_read when you need detail):")
			for _, n := range notes {
				fmt.Fprintf(&b, "\n- %s (%s)", n.title, n.path)
			}
			b.WriteString("\nIf you relied on these pages, end your answer with one line \"Notes: <page titles, comma separated>\".")
		}
	}
	return b.String()
}

// lastAnswer is the text of the newest assistant message of a session.
func (r *Registry) lastAnswer(ctx context.Context, sessionID string) string {
	events, err := r.store.ChainEvents(ctx, sessionID)
	if err != nil {
		return ""
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind != core.KindAssistantMessage {
			continue
		}
		var msg core.AssistantMessage
		if json.Unmarshal(events[i].Payload, &msg) != nil {
			continue
		}
		var parts []string
		for _, block := range msg.Content {
			if block.Kind == core.BlockText && strings.TrimSpace(block.Text) != "" {
				parts = append(parts, block.Text)
			}
		}
		if len(parts) > 0 {
			return truncateRunes(strings.Join(parts, "\n"), 4000)
		}
	}
	return ""
}

// Continue sends a follow-up to an existing child session.
func (s *subagentRunner) Continue(ctx context.Context, req core.ContinueRequest) (core.ToolOutput, error) {
	if ctx.Err() != nil {
		return stopToolOutput(ctx), nil
	}
	if len(s.allowed) == 0 {
		return toolError(noSubagentsMessage), nil
	}
	if sessionDepth(ctx, s.reg.store, req.ParentSessionID) >= maxSubagentDepth {
		return toolError(depthLimitMessage), nil
	}
	child, err := s.reg.store.Info(ctx, req.SessionID)
	if err != nil || child.ParentSessionID != req.ParentSessionID {
		return toolError(fmt.Sprintf("session %s is not a subagent of this session", req.SessionID)), nil
	}
	if !containsString(s.allowed, child.Agent) {
		return toolError(fmt.Sprintf("agent '%s' is not a subagent of this agent (allowed: %s)", child.Agent, strings.Join(s.allowed, ", "))), nil
	}
	if count, ok := s.reg.acquireChild(req.ParentSessionID); !ok {
		return toolError(fmt.Sprintf("%d subagents are already running", count)), nil
	}
	defer s.reg.releaseChild(req.ParentSessionID)
	return s.runChildTurn(ctx, child, req.Prompt, time.Now())
}

// runChildTurn starts a turn in the child, waits for it, and builds the result.
func (s *subagentRunner) runChildTurn(ctx context.Context, child core.SessionInfo, prompt string, started time.Time) (core.ToolOutput, error) {
	if ctx.Err() != nil {
		return stopToolOutput(ctx), nil
	}
	turnID, err := s.reg.startChildTurn(ctx, child.ID, prompt)
	if err != nil {
		if errors.Is(err, ErrSessionBusy) {
			return toolError(busySubagentMessage), nil
		}
		return toolError(internalErrorMessage), nil
	}
	result, err := s.reg.Wait(ctx, turnID)
	if err != nil {
		if ctx.Err() != nil {
			return stopToolOutput(ctx), nil
		}
		return toolError(internalErrorMessage), nil
	}
	// The parent context ending cancels the child turn; a cancel of this child
	// alone ends its turn as cancelled. A shutdown interrupt surfaces as an
	// interrupted task call (REQ-TFB-001).
	if ctx.Err() != nil || result.StopReason == "cancelled" || result.StopReason == "interrupted" {
		out := stopToolOutput(ctx)
		if result.StopReason == "interrupted" {
			out.Output = "interrupted"
		}
		// A cancelled child still reports the usage of the part of its turn
		// that ran, so the parent step keeps that cost (REQ-TEAM-013).
		out.Usage = &result.Usage
		return out, nil
	}
	return s.resultOutput(ctx, child, result, started), nil
}

// resultOutput builds the JSON output shared by task and task_continue.
func (s *subagentRunner) resultOutput(ctx context.Context, child core.SessionInfo, result TurnResult, started time.Time) core.ToolOutput {
	events, err := s.reg.store.ChainEvents(ctx, child.ID)
	if err != nil {
		return toolError(internalErrorMessage)
	}
	steps := 0
	toolCalls := 0
	tools := map[string]int{}
	var notes, files []string
	for _, event := range events {
		if event.TurnID != result.TurnID {
			continue
		}
		switch event.Kind {
		case core.KindStepStart:
			steps++
		case core.KindToolCall:
			toolCalls++
			name, ref, file := toolCallFacts(event.Payload)
			if name != "" {
				tools[name]++
			}
			if ref != "" {
				notes = append(notes, ref)
			}
			if file != "" {
				files = append(files, file)
			}
		}
	}
	answer, named := splitNotesLine(result.Text)
	payload := taskResult{
		SessionID:  child.ID,
		Agent:      child.Agent,
		Status:     result.StopReason,
		Answer:     answer,
		TurnID:     result.TurnID,
		Tools:      tools,
		Notes:      mergeNotes(named, notes),
		Files:      mergeNotes(files, nil),
		Steps:      steps,
		ToolCalls:  toolCalls,
		DurationMS: time.Since(started).Milliseconds(),
	}
	if result.Err != nil {
		payload.Error = result.Err.Message
	}
	output, err := json.Marshal(payload)
	if err != nil {
		return toolError(internalErrorMessage)
	}
	// Usage is the usage of the turns this call ran: the one child turn of
	// Spawn, or the one child turn of this task_continue call. It is not
	// cumulative across earlier calls. The parent step adds it to its own
	// usage (REQ-TEAM-013, AC-TEAM-031).
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: string(output), Usage: &result.Usage}
}

// taskResult is the JSON output of the task tool.
type taskResult struct {
	SessionID  string `json:"session_id"`
	Agent      string `json:"agent"`
	Status     string `json:"status"`
	Answer     string `json:"answer"`
	Error      string `json:"error,omitempty"`
	Steps      int    `json:"steps"`
	ToolCalls  int    `json:"tool_calls"`
	DurationMS int64  `json:"duration_ms"`
	// TurnID is the child turn that produced the answer.
	TurnID string `json:"turn_id,omitempty"`
	// Spec 47 team work: tool calls by name, the knowledge pages the child
	// used, and the files it changed.
	Tools map[string]int `json:"tools,omitempty"`
	Notes []string       `json:"notes,omitempty"`
	Files []string       `json:"files,omitempty"`
}

// toolCallFacts reads a tool/call payload: the tool name without an MCP
// prefix, the knowledge page it read, and the file it changed.
func toolCallFacts(raw json.RawMessage) (name, note, file string) {
	var call struct {
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(raw, &call) != nil {
		return "", "", ""
	}
	name = call.Name
	if i := strings.LastIndex(name, "__"); i >= 0 {
		name = name[i+2:]
	}
	var in map[string]any
	_ = json.Unmarshal(call.Input, &in)
	str := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := in[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	switch name {
	case "knowledge_read":
		if ref := str("ref", "path", "title"); ref != "" {
			note = strings.TrimSuffix(ref[strings.LastIndex(ref, "/")+1:], ".md")
		}
	case "write_file", "edit_file", "Write", "Edit", "MultiEdit":
		file = str("path", "file_path")
	}
	return name, note, file
}

func toolError(message string) core.ToolOutput {
	return core.ToolOutput{Status: core.ToolStatusError, Output: message}
}

// startChildTurn starts a turn on an existing session from the parent turn
// context.
func (r *Registry) startChildTurn(parent context.Context, sessionID, text string) (string, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return "", ErrClosed
	}
	runner := r.runnerLocked(sessionID)
	runner.mu.Lock()
	if runner.running {
		runner.mu.Unlock()
		r.mu.Unlock()
		return "", ErrSessionBusy
	}
	turnID, turnCtx := r.startTurnFrom(parent, runner)
	r.wg.Add(1)
	runner.mu.Unlock()
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		runner.runChain(turnCtx, inboxItem{text: text}, turnID, "")
	}()
	return turnID, nil
}

// sessionDepth returns the number of parent links of a session, up to the
// maximum delegation depth.
func sessionDepth(ctx context.Context, store core.SessionStore, sessionID string) int {
	depth := 0
	for depth < maxSubagentDepth {
		info, err := store.Info(ctx, sessionID)
		if err != nil || info.ParentSessionID == "" {
			break
		}
		depth++
		sessionID = info.ParentSessionID
	}
	return depth
}
