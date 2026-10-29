package core

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestShutdownReverseOrder(t *testing.T) {
	var mu sync.Mutex
	var order []string
	a := &bootModule{id: "test.shutdown.a", stop: func(context.Context) error {
		mu.Lock()
		order = append(order, "a")
		mu.Unlock()
		return nil
	}}
	b := &bootModule{id: "test.shutdown.b", stop: func(context.Context) error {
		mu.Lock()
		order = append(order, "b")
		mu.Unlock()
		return nil
	}}
	registerBoot(t, a)
	registerBoot(t, b)
	ctx, err := bootRows(t, []ModuleRow{{ID: "a", Module: a.id}, {ID: "b", Module: b.id}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ctx.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "b" || order[1] != "a" {
		t.Fatalf("order=%v", order)
	}
}

func TestBootDisabledRowNotInstalled(t *testing.T) {
	installed := false
	a := &bootModule{id: "test.disabled.a", install: func(*Builder) error {
		installed = true
		return nil
	}}
	registerBoot(t, a)
	_, err := bootRows(t, []ModuleRow{{ID: "a", Module: a.id, Disabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	if installed {
		t.Fatal("disabled module was still installed")
	}
	_, err = bootRows(t, []ModuleRow{{ID: "x", Module: "unknown.module", Disabled: true}})
	if err != nil {
		t.Fatalf("disabled row with unknown module failed boot: %v", err)
	}
}

func TestShutdownStuckModule(t *testing.T) {
	old := shutdownTimeout
	shutdownTimeout = 80 * time.Millisecond
	defer func() { shutdownTimeout = old }()
	quickStopped := make(chan struct{}, 1)
	stuck := &bootModule{id: "test.stuck.a", stop: func(ctx context.Context) error {
		<-ctx.Done()
		select {}
	}}
	quick := &bootModule{id: "test.stuck.b", stop: func(context.Context) error {
		quickStopped <- struct{}{}
		return nil
	}}
	registerBoot(t, stuck)
	registerBoot(t, quick)
	ctx, err := bootRows(t, []ModuleRow{{ID: "quick", Module: quick.id}, {ID: "stuck", Module: stuck.id}})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := ctx.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("shutdown took too long: %v", elapsed)
	}
	select {
	case <-quickStopped:
	case <-time.After(time.Second):
		t.Fatal("other module was not stopped")
	}
}

func TestModuleStopReleasesListeners(t *testing.T) {
	called := false
	a := &bootModule{id: "test.release.a"}
	a.install = func(b *Builder) error {
		release := b.Hooks().ToolsPreExecute.Use(func(ctx context.Context, in ToolsPreExecuteInput, next Next[ToolsPreExecuteInput, ToolDecision]) (ToolDecision, error) {
			called = true
			return next(ctx, in)
		})
		b.Own(release)
		return nil
	}
	registerBoot(t, a)
	ctx, err := bootRows(t, []ModuleRow{{ID: "a", Module: a.id}})
	if err != nil {
		t.Fatal(err)
	}
	ctx.Hooks.ToolsPreExecute.SetDefault(func(context.Context, ToolsPreExecuteInput) (ToolDecision, error) {
		return ToolDecision{Allowed: true}, nil
	})
	if _, err := ctx.Hooks.ToolsPreExecute.Call(context.Background(), ToolsPreExecuteInput{}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("listener did not run before shutdown")
	}
	called = false
	if err := ctx.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := ctx.Hooks.ToolsPreExecute.Call(context.Background(), ToolsPreExecuteInput{}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("module listener ran after shutdown")
	}
}
