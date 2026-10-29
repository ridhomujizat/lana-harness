// Package tools menyediakan registry tool dan pipeline hook tool.
package tools

import (
	"context"
	"fmt"
	"sort"

	"github.com/ridhomujizat/lana-harness/core"
)

// Registry adalah implementasi core.ToolRegistry.
type Registry struct {
	tools map[string]core.Tool
}

// NewRegistry membuat registry kosong.
func NewRegistry() *Registry { return &Registry{tools: map[string]core.Tool{}} }

// Register menolak nama ganda.
func (r *Registry) Register(t core.Tool) error {
	if t == nil {
		return fmt.Errorf("tool nil")
	}
	name := t.Spec().Name
	if name == "" {
		return fmt.Errorf("nama tool kosong")
	}
	if _, ok := r.tools[name]; ok {
		return fmt.Errorf("tool '%s' sudah terdaftar", name)
	}
	r.tools[name] = t
	return nil
}

// Get mengambil tool.
func (r *Registry) Get(name string) (core.Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Specs mengembalikan spesifikasi sesuai urutan nama yang diminta.
func (r *Registry) Specs(names []string) ([]core.ToolSpec, error) {
	out := make([]core.ToolSpec, 0, len(names))
	for _, name := range names {
		t, ok := r.tools[name]
		if !ok {
			return nil, fmt.Errorf("tool '%s' tidak terdaftar", name)
		}
		out = append(out, t.Spec())
	}
	return out, nil
}

// Names mengembalikan nama tool terdaftar, terurut.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Pipeline memasang handler default hook tool.
type Pipeline struct {
	hooks *core.Hooks
}

// NewPipeline memasang handler default pada hook yang diberikan.
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
	if in.Call.Risk == core.RiskRead {
		return core.ToolDecision{Allowed: true, Status: core.ToolStatusSuccess}, nil
	}
	return core.ToolDecision{
		Allowed: false,
		Status:  core.ToolStatusRejected,
		Reason:  fmt.Sprintf("default fail-closed: tool %s butuh izin", in.Call.Name),
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

// PreExecute menjalankan gerbang tool.
func (p *Pipeline) PreExecute(ctx context.Context, in core.ToolsPreExecuteInput) (core.ToolDecision, error) {
	return p.hooks.ToolsPreExecute.Call(ctx, in)
}

// Execute menjalankan tool.
func (p *Pipeline) Execute(ctx context.Context, in core.ToolsExecuteInput) (core.ToolOutput, error) {
	return p.hooks.ToolsExecute.Call(ctx, in)
}

// PostExecute menjalankan hook setelah tool.
func (p *Pipeline) PostExecute(ctx context.Context, in core.ToolsPostExecuteInput) (core.ToolOutput, error) {
	return p.hooks.ToolsPostExecute.Call(ctx, in)
}

var _ core.ToolRegistry = (*Registry)(nil)
