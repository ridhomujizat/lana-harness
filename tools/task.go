package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
)

var taskSpec = json.RawMessage(`{"type":"object","required":["agent","prompt"],"properties":{"agent":{"type":"string","description":"Name of an agent from the roster"},"prompt":{"type":"string","description":"Complete instructions. The agent sees nothing else from this conversation"},"title":{"type":"string","description":"Short label for the task"},"context":{"type":"array","items":{"type":"string"},"description":"Session ids from earlier task results whose answers this agent should build on"}}}`)

var taskContinueSpec = json.RawMessage(`{"type":"object","required":["session_id","prompt"],"properties":{"session_id":{"type":"string","description":"Child session id from a task result"},"prompt":{"type":"string","description":"Follow-up instructions for the child"}}}`)

const (
	taskPromptLimit = 8000
	taskTitleLimit  = 80
)

type taskTool struct{}

// NewTask creates the task tool.
func NewTask() core.Tool { return &taskTool{} }

func (t *taskTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "task", Description: "Delegates a task to a subagent in a child session and returns its answer.", InputSchema: taskSpec}
}
func (t *taskTool) Risk(json.RawMessage) core.Risk { return core.RiskExec }
func (t *taskTool) ParallelSafe() bool             { return true }

func (t *taskTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		Agent   string   `json:"agent"`
		Prompt  string   `json:"prompt"`
		Title   string   `json:"title"`
		Context []string `json:"context"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	if strings.TrimSpace(in.Agent) == "" {
		return inputErrorOutput(fmt.Errorf("agent is required"))
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return inputErrorOutput(fmt.Errorf("prompt is required"))
	}
	if utf8.RuneCountInString(in.Prompt) > taskPromptLimit {
		return inputErrorOutput(fmt.Errorf("prompt must be at most %d characters", taskPromptLimit))
	}
	if utf8.RuneCountInString(in.Title) > taskTitleLimit {
		return inputErrorOutput(fmt.Errorf("title must be at most %d characters", taskTitleLimit))
	}
	if tc.Subagents == nil {
		return errorOutput("no subagents are configured for this agent")
	}
	return tc.Subagents.Spawn(ctx, core.SpawnRequest{
		ParentSessionID: tc.SessionID,
		ParentCallID:    tc.CallID,
		Depth:           tc.Depth,
		Agent:           in.Agent,
		Prompt:          in.Prompt,
		Title:           in.Title,
		Context:         in.Context,
		WithNotes:       true,
	})
}

type taskContinueTool struct{}

// NewTaskContinue creates the task_continue tool.
func NewTaskContinue() core.Tool { return &taskContinueTool{} }

func (t *taskContinueTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "task_continue", Description: "Sends a follow-up to a subagent in an existing child session and returns its answer.", InputSchema: taskContinueSpec}
}
func (t *taskContinueTool) Risk(json.RawMessage) core.Risk { return core.RiskExec }
func (t *taskContinueTool) ParallelSafe() bool             { return true }

func (t *taskContinueTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		SessionID string `json:"session_id"`
		Prompt    string `json:"prompt"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return inputErrorOutput(fmt.Errorf("session_id is required"))
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return inputErrorOutput(fmt.Errorf("prompt is required"))
	}
	if utf8.RuneCountInString(in.Prompt) > taskPromptLimit {
		return inputErrorOutput(fmt.Errorf("prompt must be at most %d characters", taskPromptLimit))
	}
	if tc.Subagents == nil {
		return errorOutput("no subagents are configured for this agent")
	}
	return tc.Subagents.Continue(ctx, core.ContinueRequest{
		ParentSessionID: tc.SessionID,
		SessionID:       in.SessionID,
		Prompt:          in.Prompt,
	})
}
