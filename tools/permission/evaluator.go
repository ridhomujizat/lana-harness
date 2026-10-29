package permission

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/pattern"
)

// Engine is the tools.pre_execute listener.
type Engine struct {
	Store    core.SessionStore
	Config   core.Config
	Approval core.ApprovalService
}

// Handle decides a single tool call.
func (e *Engine) Handle(ctx context.Context, in core.ToolsPreExecuteInput, next core.Next[core.ToolsPreExecuteInput, core.ToolDecision]) (core.ToolDecision, error) {
	info, err := e.Store.Info(ctx, in.Call.SessionID)
	if err != nil {
		return reject("failed to read session: " + err.Error()), nil
	}
	events, err := e.Store.ChainEvents(ctx, in.Call.SessionID)
	if err != nil {
		return reject("failed to read log: " + err.Error()), nil
	}
	agentName := in.AgentName
	if agentName == "" {
		agentName = info.Agent
	}
	agent := e.Config.Agents[agentName]
	workspace := info.Workspace
	if workspace == "" {
		workspace = agent.Workspace
	}
	if workspace == "" {
		workspace = "."
	}
	arg, rawArg, ok := buildArgument(in.Call.Name, in.Call.Input, workspace)
	if !ok {
		return reject("invalid input"), nil
	}
	if arg.Kind == pattern.ArgPath && !WithinWorkspace(workspace, arg.Value) {
		return reject("outside workspace"), nil
	}
	if arg.Kind == pattern.ArgPath && arg.Alt != "" && !WithinWorkspace(workspace, arg.Alt) {
		return reject("outside workspace"), nil
	}

	rules := agent.Rules
	for _, raw := range rules.Deny {
		p, err := pattern.ParsePattern(raw)
		if err == nil && p.Match(in.Call.Name, arg) {
			return reject(fmt.Sprintf("deny rule: %s", raw)), nil
		}
	}
	for _, raw := range rules.Allow {
		p, err := pattern.ParsePattern(raw)
		if err != nil {
			continue
		}
		if arg.Kind == pattern.ArgPath {
			if p.MatchBoth(in.Call.Name, arg) {
				return allow(core.ApprovalOutcome("")), nil
			}
		} else if p.Match(in.Call.Name, arg) {
			return allow(core.ApprovalOutcome("")), nil
		}
	}
	for _, grant := range Grants(events) {
		if grant.Tool != in.Call.Name {
			continue
		}
		if !grant.HasArg || grant.Arg == rawArg {
			return allow(core.OutcomeAllowedSession), nil
		}
	}

	fallback := core.PolicyAsk
	if agent.Permission != "" {
		fallback = core.Policy(agent.Permission)
	}
	policy := EffectivePolicy(events, fallback)
	if policy == core.PolicyTrust {
		return allow(core.ApprovalOutcome("")), nil
	}
	if in.Call.Risk == core.RiskRead {
		return allow(core.ApprovalOutcome("")), nil
	}
	if policy == core.PolicyNever {
		return reject("policy never"), nil
	}
	// Memory writes stay inside the agent home and need no approval (spec #5 Q-002).
	if in.Call.Risk == core.RiskMemory {
		return allow(core.ApprovalOutcome("")), nil
	}
	if e.Approval == nil {
		return reject("approval is not available"), nil
	}
	outcome, err := e.Approval.Request(ctx, core.ApprovalRequest{
		SessionID: in.Call.SessionID, TurnID: in.Call.TurnID, CallID: in.Call.CallID,
		AgentName: agentName, ToolName: in.Call.Name, Risk: in.Call.Risk,
	})
	if err != nil {
		return reject("approval failed: " + err.Error()), nil
	}
	switch outcome {
	case core.OutcomeAllowedOnce, core.OutcomeAllowedSession:
		return allow(outcome), nil
	default:
		return reject("approval " + string(outcome)), nil
	}
}

func allow(outcome core.ApprovalOutcome) core.ToolDecision {
	return core.ToolDecision{Allowed: true, Outcome: outcome}
}

func reject(reason string) core.ToolDecision {
	if reason == "" {
		reason = "rejected"
	}
	return core.ToolDecision{Allowed: false, Status: core.ToolStatusRejected, Reason: reason}
}

func buildArgument(tool string, raw json.RawMessage, workspace string) (pattern.Argument, string, bool) {
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return pattern.Argument{}, "", false
	}
	pathTools := map[string]bool{"read_file": true, "write_file": true, "edit_file": true, "glob": true, "grep": true}
	if pathTools[tool] {
		text, _ := value["path"].(string)
		if text == "" {
			text = "."
		}
		arg, err := ResolvePath(workspace, text)
		if err != nil {
			return pattern.Argument{}, "", false
		}
		return arg, text, true
	}
	switch tool {
	case "bash":
		text, _ := value["command"].(string)
		return pattern.Argument{Kind: pattern.ArgCommand, Value: text}, text, true
	case "web_fetch":
		text, _ := value["url"].(string)
		return pattern.Argument{Kind: pattern.ArgURL, Value: text}, text, true
	case "task", "task_continue":
		text, _ := value["agent"].(string)
		return pattern.Argument{Kind: pattern.ArgAgent, Value: text}, text, true
	default:
		compact, err := json.Marshal(value)
		if err != nil {
			return pattern.Argument{}, "", false
		}
		return pattern.Argument{Kind: pattern.ArgRaw, Value: string(compact)}, string(compact), true
	}
}

// ResolveArgument is a test helper.
func ResolveArgument(tool string, raw json.RawMessage, workspace string) (pattern.Argument, bool) {
	arg, _, ok := buildArgument(tool, raw, workspace)
	return arg, ok
}

var _ = filepath.Separator
var _ = strings.TrimSpace
