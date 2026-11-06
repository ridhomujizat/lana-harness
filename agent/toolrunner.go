package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/jobs"
	"github.com/ridhomujizat/lana-harness/internal/spill"
)

// inlineRunes is the largest tool result kept inline before the rest is spilled.
const inlineRunes = 16000

// spiller saves a full tool output and returns the path of the saved file.
type spiller interface {
	Save(sessionID, tool, callID, text string) (string, error)
}

// ToolRunner executes a batch of tools in one step.
type ToolRunner struct {
	hooks *core.Hooks
	tools core.ToolRegistry
	// store saves large tool outputs. Nil means a store under <home>/spill;
	// tests replace it to exercise the save-failure path.
	store spiller
}

// NewToolRunner creates a ToolRunner.
func NewToolRunner(hooks *core.Hooks, toolReg core.ToolRegistry) *ToolRunner {
	return &ToolRunner{hooks: hooks, tools: toolReg}
}

type callState struct {
	call   core.ToolCall
	tool   core.Tool
	output core.ToolOutput
	ready  bool
}

// setOutput records a call's result and its pre-spill digest.
func (s *callState) setOutput(out core.ToolOutput) {
	out.Digest = outputDigest(out)
	s.output = out
}

// outputDigest is the hex SHA-256 of a result's status and output text
// (REQ-LPG-003). It is computed before spill so an unchanged result keeps the
// same digest across calls.
func outputDigest(out core.ToolOutput) string {
	sum := sha256.Sum256([]byte(string(out.Status) + "\x00" + out.Output))
	return hex.EncodeToString(sum[:])
}

// Run executes the tool pipeline for all calls and returns outputs in call order.
func (tr *ToolRunner) Run(ctx context.Context, calls []core.ToolCall, enabled []string, tc core.ToolCtx) []core.ToolOutput {
	allowed := map[string]bool{}
	for _, name := range enabled {
		allowed[name] = true
	}
	states := make([]*callState, len(calls))
	for i, call := range calls {
		state := &callState{call: call}
		states[i] = state
		if !allowed[call.Name] {
			repaired := repairToolName(call.Name, enabled)
			if repaired == "" {
				state.setOutput(core.ToolOutput{Status: core.ToolStatusError, Output: unknownToolText(call.Name, enabled), UnknownName: true})
				continue
			}
			slog.Info("tool name repaired", "from", call.Name, "to", repaired)
			state.call.Name = repaired
		}
		tool, ok := tr.tools.Get(state.call.Name)
		if !ok {
			state.setOutput(core.ToolOutput{Status: core.ToolStatusError, Output: unknownToolText(state.call.Name, enabled), UnknownName: true})
			continue
		}
		if err := validateInput(tool.Spec().InputSchema, call.Input); err != nil {
			state.setOutput(core.ToolOutput{Status: core.ToolStatusError, Output: "invalid input: " + err.Error()})
			continue
		}
		state.tool = tool
		state.call.Risk = tool.Risk(call.Input)
		decision, err := tr.hooks.ToolsPreExecute.Call(ctx, core.ToolsPreExecuteInput{AgentName: tc.AgentName, Call: state.call})
		if err != nil {
			state.setOutput(core.ToolOutput{Status: core.ToolStatusError, Output: err.Error()})
			continue
		}
		if !decision.Allowed {
			status := decision.Status
			if status == "" {
				status = core.ToolStatusRejected
			}
			state.setOutput(core.ToolOutput{Status: status, Output: decision.Reason})
			continue
		}
		state.ready = true
	}

	if ctx.Err() != nil {
		for _, state := range states {
			if state.ready {
				state.setOutput(stopToolOutput(ctx))
				state.ready = false
			}
		}
	} else {
		for i := 0; i < len(states); {
			state := states[i]
			if !state.ready {
				i++
				continue
			}
			if batchable(state) {
				j := i
				var batch []*callState
				for j < len(states) && states[j].ready && batchable(states[j]) {
					batch = append(batch, states[j])
					j++
				}
				tr.runBatch(ctx, batch, tc)
				i = j
				continue
			}
			tr.executeOne(ctx, state, tc)
			i++
		}
	}

	out := make([]core.ToolOutput, len(states))
	for i, state := range states {
		if state.output.Status == "" {
			state.setOutput(core.ToolOutput{Status: core.ToolStatusError, Output: "tool produced no output"})
		}
		out[i] = state.output
	}
	return out
}

// batchable reports whether a ready call may run in a parallel batch with
// the other ready calls of the step. RiskRead calls are batched as before;
// every other tool opts in through core.ParallelTool.
func batchable(state *callState) bool {
	if state.call.Risk == core.RiskRead {
		return true
	}
	tool, ok := state.tool.(core.ParallelTool)
	return ok && tool.ParallelSafe()
}

func (tr *ToolRunner) runBatch(ctx context.Context, batch []*callState, tc core.ToolCtx) {
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, state := range batch {
		wg.Add(1)
		go func(s *callState) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
				tr.executeOne(ctx, s, tc)
			case <-ctx.Done():
				s.setOutput(stopToolOutput(ctx))
			}
		}(state)
	}
	wg.Wait()
}

func (tr *ToolRunner) executeOne(ctx context.Context, state *callState, tc core.ToolCtx) {
	if ctx.Err() != nil {
		state.setOutput(stopToolOutput(ctx))
		return
	}
	tc.CallID = state.call.CallID
	input := core.ToolsExecuteInput{Ctx: tc, Call: state.call, Tool: state.tool}
	output, err := tr.hooks.ToolsExecute.Call(ctx, input)
	if err != nil {
		state.setOutput(core.ToolOutput{Status: core.ToolStatusError, Output: err.Error()})
		return
	}
	post, err := tr.hooks.ToolsPostExecute.Call(ctx, core.ToolsPostExecuteInput{Call: state.call, Output: output})
	if err != nil {
		state.setOutput(core.ToolOutput{Status: core.ToolStatusError, Output: err.Error()})
		return
	}
	if ctx.Err() != nil {
		state.setOutput(stopToolOutput(ctx))
		return
	}
	state.setOutput(post)
	tr.spillOutput(state, tc)
}

// spillOutput replaces a large tool result with a head/tail preview and a
// notice that points at the full text saved on disk. read_file results are
// never spilled, and a save failure keeps the original result.
func (tr *ToolRunner) spillOutput(state *callState, tc core.ToolCtx) {
	if state.call.Name == "read_file" {
		return
	}
	text := state.output.Output
	if utf8.RuneCountInString(text) <= inlineRunes {
		return
	}
	store := tr.store
	if store == nil {
		if tc.Home == "" {
			return
		}
		store = spill.Store{Root: filepath.Join(tc.Home, "spill")}
	}
	path, err := store.Save(tc.SessionID, state.call.Name, state.call.CallID, text)
	if err != nil {
		slog.Warn("tool output spill failed", "tool", state.call.Name, "session_id", tc.SessionID, "error", err)
		return
	}
	state.output.Output = spillPreview(text, path)
}

// spillPreview returns a head/tail preview of text plus a notice naming the
// saved file. The result is at most inlineRunes runes, and a rune is never cut
// in half.
func spillPreview(text, path string) string {
	total := len(text)
	var notice, head, tail string
	kept := 0
	for i := 0; i < 4; i++ {
		omitted := total - kept
		if omitted < 0 {
			omitted = 0
		}
		notice = fmt.Sprintf("(Omitted %d bytes. Full output stored at: %s. Use read_file with offset/limit, or grep this path.)", omitted, path)
		left := inlineRunes - utf8.RuneCountInString(notice) - 2
		if left < 0 {
			left = 0
		}
		headN := left * 2 / 3
		head, tail = cutRunes(text, headN, left-headN)
		size := len(head) + len(tail)
		if i > 0 && size == kept {
			break
		}
		kept = size
	}
	return head + "\n" + notice + "\n" + tail
}

// cutRunes returns the first headN runes and the last tailN runes of s. The
// two pieces never overlap.
func cutRunes(s string, headN, tailN int) (string, string) {
	runes := []rune(s)
	if headN > len(runes) {
		headN = len(runes)
	}
	if tailN > len(runes)-headN {
		tailN = len(runes) - headN
	}
	head := string(runes[:headN])
	if tailN == 0 {
		return head, ""
	}
	return head, string(runes[len(runes)-tailN:])
}

// JobService is the background-job port of the agent package: core.JobService
// plus the per-turn <context> line (REQ-JOB-015, REQ-JOB-017).
type JobService interface {
	core.JobService
	ContextLine(sessionID string) string
}

// jobAdapter adapts *jobs.Registry to core.JobService so core and tools never
// import the registry (REQ-JOB-010).
type jobAdapter struct{ reg *jobs.Registry }

// NewJobService returns the adapter the runtime installs with SetJobs.
func NewJobService(reg *jobs.Registry) JobService { return jobAdapter{reg: reg} }

func (a jobAdapter) Start(sessionID, workspace, command string) (core.JobView, error) {
	job, err := a.reg.Start(sessionID, workspace, command)
	if err != nil {
		return core.JobView{}, err
	}
	return toJobView(*job), nil
}

func (a jobAdapter) List(sessionID string) []core.JobView {
	list := a.reg.List(sessionID)
	out := make([]core.JobView, 0, len(list))
	for _, job := range list {
		out = append(out, toJobView(job))
	}
	return out
}

func (a jobAdapter) ReadNew(sessionID, id string, max int) (string, int64, core.JobView, error) {
	text, dropped, job, err := a.reg.ReadNew(sessionID, id, max)
	if err != nil {
		return "", 0, core.JobView{}, err
	}
	return text, dropped, toJobView(job), nil
}

func (a jobAdapter) Tail(sessionID, id string, max int) (string, error) {
	return a.reg.Tail(sessionID, id, max)
}

func (a jobAdapter) Wait(ctx context.Context, sessionID, id string, d time.Duration) core.JobView {
	return toJobView(a.reg.Wait(ctx, sessionID, id, d))
}

func (a jobAdapter) Kill(sessionID, id string) (core.JobView, error) {
	job, err := a.reg.Kill(sessionID, id)
	if err != nil {
		return core.JobView{}, err
	}
	return toJobView(job), nil
}

func (a jobAdapter) ContextLine(sessionID string) string {
	return a.reg.ContextLine(sessionID)
}

func toJobView(job jobs.Job) core.JobView {
	return core.JobView{
		ID:        job.ID,
		Command:   job.Command,
		LogPath:   job.LogPath,
		PID:       job.PID,
		StartedAt: job.StartedAt,
		EndedAt:   job.EndedAt,
		Status:    string(job.Status),
		ExitCode:  job.ExitCode,
	}
}
