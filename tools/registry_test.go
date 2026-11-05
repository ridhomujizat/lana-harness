package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

func TestDefaultGate(t *testing.T) {
	p := NewPipeline(core.NewHooks())
	read, err := p.PreExecute(context.Background(), core.ToolsPreExecuteInput{Call: core.ToolCall{Name: "read_file", Risk: core.RiskRead}})
	if err != nil {
		t.Fatal(err)
	}
	if !read.Allowed || read.Status != core.ToolStatusSuccess {
		t.Fatalf("read=%+v", read)
	}
	write, err := p.PreExecute(context.Background(), core.ToolsPreExecuteInput{Call: core.ToolCall{Name: "write_file", Risk: core.RiskWrite}})
	if err != nil {
		t.Fatal(err)
	}
	if write.Allowed || write.Status != core.ToolStatusRejected || !strings.Contains(write.Reason, "default fail-closed") {
		t.Fatalf("write=%+v", write)
	}
}

func TestDenyListenerStopsChain(t *testing.T) {
	hooks := core.NewHooks()
	p := NewPipeline(hooks)
	hooks.ToolsPreExecute.Use(func(ctx context.Context, in core.ToolsPreExecuteInput, next core.Next[core.ToolsPreExecuteInput, core.ToolDecision]) (core.ToolDecision, error) {
		return core.ToolDecision{Allowed: false, Status: core.ToolStatusRejected, Reason: "blocked by listener"}, nil
	})
	got, err := p.PreExecute(context.Background(), core.ToolsPreExecuteInput{Call: core.ToolCall{Name: "read_file", Risk: core.RiskRead}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Allowed || got.Reason != "blocked by listener" {
		t.Fatalf("got=%+v", got)
	}
}

type dummyTool struct {
	name string
	call func(context.Context, core.ToolCtx, json.RawMessage) (core.ToolOutput, error)
}

func (d dummyTool) Spec() core.ToolSpec            { return core.ToolSpec{Name: d.name} }
func (d dummyTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }
func (d dummyTool) Call(ctx context.Context, tc core.ToolCtx, input json.RawMessage) (core.ToolOutput, error) {
	if d.call != nil {
		return d.call(ctx, tc, input)
	}
	return core.ToolOutput{Status: core.ToolStatusSuccess}, nil
}

func TestRegistryRejectsDuplicate(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(dummyTool{name: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(dummyTool{name: "x"}); err == nil {
		t.Fatal("duplicate name accepted")
	}
	if _, err := r.Specs([]string{"y"}); err == nil {
		t.Fatal("unregistered tool did not error")
	}
}

func TestRegistryUnregister(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(dummyTool{name: "x"}); err != nil {
		t.Fatal(err)
	}
	if !r.Unregister("x") {
		t.Fatal("Unregister(x)=false, want true")
	}
	if _, ok := r.Get("x"); ok {
		t.Fatal("x is still registered")
	}
	if r.Unregister("x") {
		t.Fatal("Unregister(x)=true on the second call")
	}
	// A name freed by Unregister can be registered again.
	if err := r.Register(dummyTool{name: "x"}); err != nil {
		t.Fatalf("re-register: %v", err)
	}
}

func TestRegistryConcurrent(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("tool%d", i%4)
			_ = r.Register(dummyTool{name: name})
			_ = r.Names()
			_, _ = r.Get(name)
			_, _ = r.Specs([]string{name})
			r.Unregister(name)
		}(i)
	}
	wg.Wait()
}

func TestExecuteRecoversPanic(t *testing.T) {
	hooks := core.NewHooks()
	p := NewPipeline(hooks)
	out, err := p.Execute(context.Background(), core.ToolsExecuteInput{
		Tool: dummyTool{name: "boom", call: func(context.Context, core.ToolCtx, json.RawMessage) (core.ToolOutput, error) {
			panic("boom")
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "panic") {
		t.Fatalf("out=%+v", out)
	}
}
