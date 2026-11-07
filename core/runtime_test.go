package core

import (
	"context"
	"testing"
)

type nopRuntime struct{}

func (nopRuntime) RunTurn(context.Context, RuntimeTurn) error { return nil }
func (nopRuntime) Interrupt(string) error                     { return nil }
func (nopRuntime) Close(string)                               {}
func (nopRuntime) Shutdown(context.Context) error             { return nil }
func (nopRuntime) Status(context.Context) RuntimeStatus       { return RuntimeStatus{} }

func TestRegisterRuntime(t *testing.T) {
	name := "test-runtime-register"
	RegisterRuntime(name, func(RuntimeDeps) (AgentRuntime, error) { return nopRuntime{}, nil })
	f, ok := LookupRuntime(name)
	if !ok {
		t.Fatal("runtime not found")
	}
	if rt, err := f(RuntimeDeps{}); err != nil || rt == nil {
		t.Fatalf("factory: %v", err)
	}
	if _, ok := LookupRuntime("missing-runtime"); ok {
		t.Fatal("missing runtime found")
	}
	found := false
	for _, n := range RuntimeNames() {
		found = found || n == name
	}
	if !found {
		t.Fatal("RuntimeNames misses the runtime")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate registration did not panic")
		}
	}()
	RegisterRuntime(name, func(RuntimeDeps) (AgentRuntime, error) { return nopRuntime{}, nil })
}
