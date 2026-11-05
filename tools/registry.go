// Package tools provides the tool registry and the tool hook pipeline.
package tools

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/ridhomujizat/lana-harness/core"
)

// Registry is the implementation of core.ToolRegistry. It is safe for
// concurrent use, so a toolbox change can register and unregister tools while
// turns read them.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]core.Tool
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return &Registry{tools: map[string]core.Tool{}} }

// Register rejects duplicate names.
func (r *Registry) Register(t core.Tool) error {
	if t == nil {
		return fmt.Errorf("tool nil")
	}
	name := t.Spec().Name
	if name == "" {
		return fmt.Errorf("tool name is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[name]; ok {
		return fmt.Errorf("tool '%s' is already registered", name)
	}
	r.tools[name] = t
	return nil
}

// Unregister removes the tool with the given name and reports whether one was
// registered under it.
func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[name]; !ok {
		return false
	}
	delete(r.tools, name)
	return true
}

// Get returns the tool with the given name.
func (r *Registry) Get(name string) (core.Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Specs returns the specs in the order of the requested names.
func (r *Registry) Specs(names []string) ([]core.ToolSpec, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]core.ToolSpec, 0, len(names))
	for _, name := range names {
		t, ok := r.tools[name]
		if !ok {
			return nil, fmt.Errorf("tool '%s' is not registered", name)
		}
		out = append(out, t.Spec())
	}
	return out, nil
}

// Names returns the registered tool names, sorted.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Pipeline installs the default handlers for the tool hooks.
type Pipeline struct {
	hooks *core.Hooks
}

// NewPipeline installs the default handlers on the given hooks.
func NewPipeline(hooks *core.Hooks) *Pipeline {
	if hooks == nil {
		hooks = core.NewHooks()
	}
	hooks.ToolsPreExecute.SetDefault(defaultPreExecute)
	hooks.ToolsExecute.SetDefault(defaultExecute)
	hooks.ToolsPostExecute.SetDefault(defaultPostExecute)
	return &Pipeline{hooks: hooks}
}

func defaultPreExecute(_ context.Context, in core.ToolsPreExecuteInput) (core.ToolDecision, error) {
	if in.Call.Risk == core.RiskRead || in.Call.Risk == core.RiskMemory {
		return core.ToolDecision{Allowed: true, Status: core.ToolStatusSuccess}, nil
	}
	return core.ToolDecision{
		Allowed: false,
		Status:  core.ToolStatusRejected,
		Reason:  fmt.Sprintf("default fail-closed: tool %s requires approval", in.Call.Name),
	}, nil
}

func defaultExecute(ctx context.Context, in core.ToolsExecuteInput) (out core.ToolOutput, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			out = core.ToolOutput{Status: core.ToolStatusError, Output: fmt.Sprintf("panic: %v", recovered)}
			err = nil
		}
	}()
	res, callErr := in.Tool.Call(ctx, in.Ctx, in.Call.Input)
	if callErr != nil {
		return core.ToolOutput{Status: core.ToolStatusError, Output: callErr.Error()}, nil
	}
	return res, nil
}

func defaultPostExecute(_ context.Context, in core.ToolsPostExecuteInput) (core.ToolOutput, error) {
	return in.Output, nil
}

// PreExecute runs the tool gate.
func (p *Pipeline) PreExecute(ctx context.Context, in core.ToolsPreExecuteInput) (core.ToolDecision, error) {
	return p.hooks.ToolsPreExecute.Call(ctx, in)
}

// Execute runs the tool.
func (p *Pipeline) Execute(ctx context.Context, in core.ToolsExecuteInput) (core.ToolOutput, error) {
	return p.hooks.ToolsExecute.Call(ctx, in)
}

// PostExecute runs the hook after the tool.
func (p *Pipeline) PostExecute(ctx context.Context, in core.ToolsPostExecuteInput) (core.ToolOutput, error) {
	return p.hooks.ToolsPostExecute.Call(ctx, in)
}

var _ core.ToolRegistry = (*Registry)(nil)
