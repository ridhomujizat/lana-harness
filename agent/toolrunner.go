package agent

import (
	"context"
	"sync"

	"github.com/ridhomujizat/lana-harness/core"
)

// ToolRunner executes a batch of tools in one step.
type ToolRunner struct {
	hooks *core.Hooks
	tools core.ToolRegistry
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
			state.output = core.ToolOutput{Status: core.ToolStatusError, Output: "tool is not enabled for agent"}
			continue
		}
		tool, ok := tr.tools.Get(call.Name)
		if !ok {
			state.output = core.ToolOutput{Status: core.ToolStatusError, Output: "tool is not registered"}
			continue
		}
		if err := validateInput(tool.Spec().InputSchema, call.Input); err != nil {
			state.output = core.ToolOutput{Status: core.ToolStatusError, Output: "invalid input: " + err.Error()}
			continue
		}
		state.tool = tool
		state.call.Risk = tool.Risk(call.Input)
		decision, err := tr.hooks.ToolsPreExecute.Call(ctx, core.ToolsPreExecuteInput{AgentName: tc.AgentName, Call: state.call})
		if err != nil {
			state.output = core.ToolOutput{Status: core.ToolStatusError, Output: err.Error()}
			continue
		}
		if !decision.Allowed {
			status := decision.Status
			if status == "" {
				status = core.ToolStatusRejected
			}
			state.output = core.ToolOutput{Status: status, Output: decision.Reason}
			continue
		}
		state.ready = true
	}

	if ctx.Err() != nil {
		for _, state := range states {
			if state.ready {
				state.output = stopToolOutput(ctx)
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
		out[i] = state.output
		if out[i].Status == "" {
			out[i] = core.ToolOutput{Status: core.ToolStatusError, Output: "tool produced no output"}
		}
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
				s.output = stopToolOutput(ctx)
			}
		}(state)
	}
	wg.Wait()
}

func (tr *ToolRunner) executeOne(ctx context.Context, state *callState, tc core.ToolCtx) {
	if ctx.Err() != nil {
		state.output = stopToolOutput(ctx)
		return
	}
	tc.CallID = state.call.CallID
	input := core.ToolsExecuteInput{Ctx: tc, Call: state.call, Tool: state.tool}
	output, err := tr.hooks.ToolsExecute.Call(ctx, input)
	if err != nil {
		state.output = core.ToolOutput{Status: core.ToolStatusError, Output: err.Error()}
		return
	}
	post, err := tr.hooks.ToolsPostExecute.Call(ctx, core.ToolsPostExecuteInput{Call: state.call, Output: output})
	if err != nil {
		state.output = core.ToolOutput{Status: core.ToolStatusError, Output: err.Error()}
		return
	}
	if ctx.Err() != nil {
		state.output = stopToolOutput(ctx)
		return
	}
	state.output = post
}
