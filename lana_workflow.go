package lana

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/agent"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/pathenv"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
	"github.com/ridhomujizat/lana-harness/tools"
)

// The runtime side of workflows (design §Runtime glue, REQ-WF-005, REQ-WF-009).

// workflowParentHops is how many parents above a tool call's session the
// workflow tool gate walks to find the workflow session, so a worker's
// subagents count (REQ-WF-009).
const workflowParentHops = 3

// bashDefaultTimeout is the default Bash call timeout when the node sets none.
const bashDefaultTimeout = 120 * time.Second

// browseDefaultTimeout is the default Browse call timeout when the node sets
// none.
const browseDefaultTimeout = 30 * time.Second

// bashOutputLimit is how much combined output Bash keeps, the last bytes.
const bashOutputLimit = 64 << 10

// browseStderrLimit is how much of obscura's stderr a failed Browse reports.
const browseStderrLimit = 500

// workflowSession is the live state of one RunAgent session. lock is the set of
// tools that run without asking, tools restricts the worker to a tool list
// (nil = the agent defaults), and maxSteps bounds the tool calls (0 = the agent
// default) (REQ-WF-005, REQ-WF-009).
type workflowSession struct {
	lock     map[string]bool
	tools    map[string]bool
	maxSteps int
	steps    int
}

// WorkflowDeps returns the workflow engine's view of the runtime (design
// §Runtime glue).
func (rt *Runtime) WorkflowDeps() workflow.Deps { return workflowDeps{rt: rt} }

// workflowDeps adapts *Runtime to workflow.Deps.
type workflowDeps struct{ rt *Runtime }

// RunAgent runs one prompt in a fresh session on channel "workflow" and blocks
// until the turn ends (REQ-WF-005).
func (d workflowDeps) RunAgent(ctx context.Context, req workflow.AgentRequest) (workflow.AgentResult, error) {
	rt := d.rt
	session, err := rt.CreateChannelSession(ctx, "workflow", req.WorkflowID, req.Agent)
	if err != nil {
		return workflow.AgentResult{}, err
	}
	entry := &workflowSession{lock: toolSet(req.Lock), maxSteps: req.MaxSteps}
	if req.Tools != nil {
		entry.tools = toolSet(req.Tools)
	}
	rt.wfMu.Lock()
	rt.wfSessions[session.ID] = entry
	rt.wfMu.Unlock()
	defer func() {
		rt.wfMu.Lock()
		delete(rt.wfSessions, session.ID)
		rt.wfMu.Unlock()
	}()
	if req.Model != "" {
		if err := rt.SetModel(ctx, session.ID, req.Model); err != nil {
			return workflow.AgentResult{}, err
		}
	}
	result, err := rt.Run(ctx, Request{Agent: req.Agent, Prompt: req.Prompt, SessionID: session.ID})
	agentResult := workflow.AgentResult{
		Text:       result.Text,
		SessionID:  session.ID,
		Tokens:     int64(result.Usage.InputTokens + result.Usage.OutputTokens),
		StopReason: result.StopReason,
	}
	if err != nil {
		return agentResult, err
	}
	return agentResult, nil
}

// CallTool runs one registered tool by name and returns its text output
// (REQ-WF-005).
func (d workflowDeps) CallTool(ctx context.Context, req workflow.ToolRequest) (string, error) {
	rt := d.rt
	tool, ok := rt.coreCtx.Tools.Get(req.Name)
	if !ok {
		return "", fmt.Errorf("unknown tool %s", req.Name)
	}
	workspace, err := rt.workflowWorkspace(req.WorkflowID)
	if err != nil {
		return "", err
	}
	output, err := tool.Call(ctx, core.ToolCtx{
		Workspace: workspace,
		Home:      rt.home,
		Jobs:      rt.workflowJobs(),
		Knowledge: rt.workflowKnowledge(),
	}, req.Input)
	if err != nil {
		return "", err
	}
	return output.Output, nil
}

// Bash runs a shell command in the workflow workspace (REQ-WF-005). A non-zero
// exit is reported in BashResult; only a timeout or a start error is an error.
func (d workflowDeps) Bash(ctx context.Context, req workflow.BashRequest) (workflow.BashResult, error) {
	rt := d.rt
	workspace, err := rt.workflowWorkspace(req.WorkflowID)
	if err != nil {
		return workflow.BashResult{}, err
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = bashDefaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "bash", "-c", req.Command)
	cmd.Dir = workspace
	cmd.Env = pathenv.Env(rt.toolboxPathDirs())
	out, err := cmd.CombinedOutput()
	result := workflow.BashResult{Output: tailBytes(out, bashOutputLimit)}
	if runCtx.Err() != nil {
		return result, fmt.Errorf("bash: timeout after %s", timeout)
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		return result, err
	}
	return result, nil
}

// WebFetch fetches a URL and returns its title and markdown (REQ-WF-005).
func (d workflowDeps) WebFetch(ctx context.Context, url string) (title, markdown string, err error) {
	return tools.FetchMarkdown(ctx, url)
}

// Browse renders a page with the obscura browser engine (REQ-WF-005,
// REQ-TB-006). The binary comes from an installed package that provides it, or
// from PATH.
func (d workflowDeps) Browse(ctx context.Context, req workflow.BrowseRequest) (string, error) {
	rt := d.rt
	binary, ok := rt.toolbox.FindBinary("obscura")
	if !ok {
		return "", errors.New("Obscura is not installed. Add it on the Tools page.") //nolint:staticcheck // ST1005: the workflow node shows this exact text.
	}
	format := req.Format
	if format == "" {
		format = "text"
	}
	storageDir := filepath.Join(rt.workflowHome(), "workflows", req.WorkflowID, "browser")
	if err := os.MkdirAll(storageDir, 0o755); err != nil {
		return "", err
	}
	// --stealth and --storage-dir are global flags, so they come before the
	// fetch subcommand; --dump and --selector belong to fetch.
	args := make([]string, 0, 8)
	if req.Stealth {
		args = append(args, "--stealth")
	}
	args = append(args, "--storage-dir", storageDir, "fetch", "--dump", format)
	if req.Selector != "" {
		args = append(args, "--selector", req.Selector)
	}
	args = append(args, req.URL)
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = browseDefaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if runCtx.Err() != nil {
		return "", fmt.Errorf("browse: timeout after %s", timeout)
	}
	if err != nil {
		message := stderr.String()
		if len(message) > browseStderrLimit {
			message = message[len(message)-browseStderrLimit:]
		}
		return "", fmt.Errorf("obscura: %s", strings.TrimSpace(message))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// SaveKnowledge stores a knowledge note for the workflow (REQ-WF-005).
func (d workflowDeps) SaveKnowledge(ctx context.Context, req workflow.KnowledgeRequest) error {
	title := req.Title
	if req.Folder != "" {
		title = req.Folder + "/" + req.Title
	}
	_, err := d.rt.IngestKnowledge(ctx, KnowledgeSave{
		Channel: "workflow",
		Address: req.WorkflowID,
		Agent:   req.Agent,
		Title:   title,
		Ext:     "md",
		Text:    req.Text,
	})
	return err
}

// AgentExists reports whether an agent is configured (REQ-WF-002).
func (d workflowDeps) AgentExists(name string) bool {
	_, ok := d.rt.agentConfig(name)
	return ok
}

// ToolRisk returns the risk of a tool for empty input, as read, write, or admin
// (REQ-WF-009). An unknown tool yields "".
func (d workflowDeps) ToolRisk(name string) string {
	tool, ok := d.rt.coreCtx.Tools.Get(name)
	if !ok {
		return ""
	}
	switch tool.Risk(json.RawMessage("{}")) {
	case core.RiskRead:
		return workflow.RiskRead
	case core.RiskAdmin:
		return workflow.RiskAdmin
	default:
		return workflow.RiskWrite
	}
}

// workflowToolGate is the prepended ToolsPreExecute listener (REQ-WF-009,
// AC-WF-016). It acts only on sessions a workflow run registered, or on their
// subagents: it enforces the worker tool list and the step budget, and lets a
// locked tool run without asking. Anything else falls through to the normal
// permission engine, which asks the owner through the approval router.
func (rt *Runtime) workflowToolGate(ctx context.Context, in core.ToolsPreExecuteInput, next core.Next[core.ToolsPreExecuteInput, core.ToolDecision]) (core.ToolDecision, error) {
	session, ok := rt.workflowSessionFor(ctx, in.Call.SessionID)
	if !ok {
		return next(ctx, in)
	}
	if session.tools != nil && !session.tools[in.Call.Name] {
		return core.ToolDecision{Allowed: false, Outcome: core.OutcomeRejected, Status: core.ToolStatusRejected, Reason: "not in this worker's tools"}, nil
	}
	rt.wfMu.Lock()
	session.steps++
	over := session.maxSteps > 0 && session.steps > session.maxSteps
	rt.wfMu.Unlock()
	if over {
		return core.ToolDecision{Allowed: false, Outcome: core.OutcomeRejected, Status: core.ToolStatusRejected, Reason: "step budget used"}, nil
	}
	// REQ-TB-003: tools_request always raises an owner approval, so a workflow
	// lock never auto-allows it.
	if in.Call.Name != toolsRequestToolName && session.lock[in.Call.Name] {
		return core.ToolDecision{Allowed: true, Outcome: core.OutcomeAllowedOnce}, nil
	}
	return next(ctx, in)
}

// workflowSessionFor returns the workflow session that owns a tool call, or of
// one of its parents up to workflowParentHops levels (REQ-WF-009).
func (rt *Runtime) workflowSessionFor(ctx context.Context, sessionID string) (*workflowSession, bool) {
	for hop := 0; sessionID != "" && hop <= workflowParentHops; hop++ {
		rt.wfMu.Lock()
		session := rt.wfSessions[sessionID]
		rt.wfMu.Unlock()
		if session != nil {
			return session, true
		}
		info, err := rt.coreCtx.Sessions.Info(ctx, sessionID)
		if err != nil {
			return nil, false
		}
		sessionID = info.ParentSessionID
	}
	return nil, false
}

// IsWorkflowSession reports whether the session runs on channel "workflow"
// (design §Runtime glue).
func (rt *Runtime) IsWorkflowSession(id string) bool {
	rt.wfMu.Lock()
	_, ok := rt.wfSessions[id]
	rt.wfMu.Unlock()
	return ok
}

// Workflows returns the workflow service (design §Runtime glue). It is never
// nil after New.
func (rt *Runtime) Workflows() *workflow.Service { return rt.workflows }

// StartWorkflows resumes durable runs, starts the scheduler, and bridges
// session events to the engine (REQ-WF-003, REQ-WF-007, design §Runtime glue).
// It is a no-op after the first call. The engine stops with ctx.
func (rt *Runtime) StartWorkflows(ctx context.Context) {
	if rt.workflows == nil {
		return
	}
	if ctx == nil {
		ctx = rt.ctx
	}
	rt.workflowsOnce.Do(func() {
		if err := rt.workflows.Start(ctx); err != nil {
			slog.Warn("workflow: start", "error", err)
		}
		go rt.SubscribeSessionEvents(ctx, func(kind, sessionID, agent string, data json.RawMessage) {
			rt.workflows.HandleEvent(ctx, kind, sessionID, agent, data)
		})
	})
}

// SubscribeSessionEvents passes every session event whose session is not on
// channel "workflow" to fn until ctx ends. The channel and agent of a session
// are cached after the first event, so the lookup runs once per session.
func (rt *Runtime) SubscribeSessionEvents(ctx context.Context, fn func(kind, sessionID, agent string, data json.RawMessage)) {
	if fn == nil || rt.coreCtx == nil || rt.coreCtx.Hooks == nil || rt.coreCtx.Hooks.SessionEvent == nil {
		return
	}
	if ctx == nil {
		ctx = rt.ctx
	}
	type sessionMeta struct{ channel, agent string }
	metas := map[string]sessionMeta{}
	sub := rt.coreCtx.Hooks.SessionEvent.Subscribe()
	defer sub.Close()
	for {
		received, err := sub.Recv(ctx)
		if err != nil {
			return
		}
		if received.Lagged > 0 {
			continue
		}
		event := received.Value
		meta, ok := metas[event.SessionID]
		if !ok {
			info, err := rt.coreCtx.Sessions.Info(ctx, event.SessionID)
			if err != nil {
				continue
			}
			meta = sessionMeta{channel: info.Channel, agent: info.Agent}
			metas[event.SessionID] = meta
		}
		if meta.channel == "workflow" {
			continue
		}
		fn(event.Kind, event.SessionID, meta.agent, event.Payload)
	}
}

// workflowWorkspace returns the workspace a workflow's bash and tool calls run
// in: <LANA_HOME>/workspaces/workflows/<id>, or a temp dir under os.TempDir
// when the runtime has no home folder. It is created on first use.
func (rt *Runtime) workflowWorkspace(id string) (string, error) {
	dir := filepath.Join(rt.workflowHome(), "workspaces", "workflows", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// workflowHome is the base of the workflow workspace and browser storage dirs.
func (rt *Runtime) workflowHome() string {
	if rt.home != "" {
		return rt.home
	}
	return filepath.Join(os.TempDir(), "lana-workflows")
}

// workflowJobs returns the background-job port a tool call reads through. It is
// nil when the runtime has no job registry.
func (rt *Runtime) workflowJobs() core.JobService {
	if rt.jobs == nil {
		return nil
	}
	return agent.NewJobService(rt.jobs)
}

// workflowKnowledge returns the knowledge service a tool call reads through, or
// nil when the runtime has none.
func (rt *Runtime) workflowKnowledge() core.Knowledge {
	service, ok := rt.coreCtx.Lookup(core.ServiceKnowledge)
	if !ok || service == nil {
		return nil
	}
	knowledge, _ := service.(core.Knowledge)
	return knowledge
}

// toolSet turns a tool name list into a set. An empty or nil list yields an
// empty (non-nil) map.
func toolSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

// tailBytes returns the last max bytes of data as a string.
func tailBytes(data []byte, max int) string {
	if len(data) <= max {
		return string(data)
	}
	return string(data[len(data)-max:])
}
