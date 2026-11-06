package core

import (
	"context"
	"strings"
	"testing"
)

type bootModule struct {
	id       string
	requires []ServiceKey
	provides []ServiceKey
	order    *[]string
	install  func(*Builder) error
	stop     func(context.Context) error
}

func (m *bootModule) ID() string             { return m.id }
func (m *bootModule) Requires() []ServiceKey { return m.requires }
func (m *bootModule) Provides() []ServiceKey { return m.provides }
func (m *bootModule) Stop(ctx context.Context) error {
	if m.stop != nil {
		return m.stop(ctx)
	}
	return nil
}
func (m *bootModule) Install(b *Builder, cfg ModuleConfig) error {
	if m.order != nil {
		*m.order = append(*m.order, m.id)
	}
	if m.install != nil {
		if err := m.install(b); err != nil {
			return err
		}
	}
	for _, key := range m.provides {
		if err := b.Provide(key, "svc:"+string(key)); err != nil {
			return err
		}
	}
	return nil
}

func registerBoot(t *testing.T, mod *bootModule) {
	t.Helper()
	RegisterModule(mod.id, func() Module { return mod })
}

func bootRows(t *testing.T, rows []ModuleRow) (*Context, error) {
	t.Helper()
	return Boot(context.Background(), BootInput{Config: Config{Modules: rows}, Profile: "test"})
}

func TestBootOrderDependency(t *testing.T) {
	var order []string
	a := &bootModule{id: "test.order.a", provides: []ServiceKey{"svc.a"}, order: &order}
	b := &bootModule{id: "test.order.b", requires: []ServiceKey{"svc.a"}, order: &order}
	registerBoot(t, a)
	registerBoot(t, b)
	_, err := bootRows(t, []ModuleRow{
		{ID: "b", Module: b.id},
		{ID: "a", Module: a.id},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != a.id+","+b.id {
		t.Fatalf("order=%v", order)
	}
}

func TestBootOrderRowStable(t *testing.T) {
	var order []string
	a := &bootModule{id: "test.stable.a", order: &order}
	b := &bootModule{id: "test.stable.b", order: &order}
	registerBoot(t, a)
	registerBoot(t, b)
	_, err := bootRows(t, []ModuleRow{
		{ID: "a", Module: a.id},
		{ID: "b", Module: b.id},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != a.id+","+b.id {
		t.Fatalf("order=%v", order)
	}
}

func TestBootMissingService(t *testing.T) {
	b := &bootModule{id: "test.missing.b", requires: []ServiceKey{"llm"}}
	registerBoot(t, b)
	_, err := bootRows(t, []ModuleRow{{ID: "b", Module: b.id}})
	if err == nil {
		t.Fatal("boot succeeded although a service is missing")
	}
	if !strings.Contains(err.Error(), "b") || !strings.Contains(err.Error(), "llm") {
		t.Fatalf("wrong message: %v", err)
	}
}

func TestBootCycleMessage(t *testing.T) {
	a := &bootModule{id: "test.cycle.a", provides: []ServiceKey{"svc.a"}, requires: []ServiceKey{"svc.b"}}
	b := &bootModule{id: "test.cycle.b", provides: []ServiceKey{"svc.b"}, requires: []ServiceKey{"svc.a"}}
	registerBoot(t, a)
	registerBoot(t, b)
	_, err := bootRows(t, []ModuleRow{
		{ID: "a", Module: a.id},
		{ID: "b", Module: b.id},
	})
	if err == nil {
		t.Fatal("boot succeeded although there is a cycle")
	}
	if !strings.Contains(err.Error(), "dependency cycle: a → b → a") {
		t.Fatalf("wrong cycle message: %v", err)
	}
}

func TestBootDuplicateProvider(t *testing.T) {
	a := &bootModule{id: "test.dup.a", provides: []ServiceKey{"svc.x"}}
	b := &bootModule{id: "test.dup.b", provides: []ServiceKey{"svc.x"}}
	registerBoot(t, a)
	registerBoot(t, b)
	_, err := bootRows(t, []ModuleRow{
		{ID: "a", Module: a.id},
		{ID: "b", Module: b.id},
	})
	if err == nil {
		t.Fatal("boot succeeded although a provider is duplicated")
	}
	if !strings.Contains(err.Error(), "a") || !strings.Contains(err.Error(), "b") || !strings.Contains(err.Error(), "svc.x") {
		t.Fatalf("wrong message: %v", err)
	}
}

func TestBootApprovalDefaultUnavailable(t *testing.T) {
	a := &bootModule{id: "test.approval.a"}
	registerBoot(t, a)
	ctx, err := bootRows(t, []ModuleRow{{ID: "a", Module: a.id}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := ctx.Hooks.ApprovalRequest.Call(context.Background(), ApprovalRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if out != OutcomeUnavailable {
		t.Fatalf("outcome=%q", out)
	}
}
