// Package permission provides the permission evaluator and approvals.
package permission

import (
	"context"
	"fmt"

	"github.com/ridhomujizat/lana-harness/core"
)

func init() {
	core.RegisterModule("permission", func() core.Module { return &permissionModule{} })
	core.RegisterModule("approval", func() core.Module { return &approvalModule{} })
}

type approvalModule struct {
	service *ApprovalService
}

func (m *approvalModule) ID() string                  { return "approval" }
func (m *approvalModule) Requires() []core.ServiceKey { return []core.ServiceKey{core.ServiceSessions} }
func (m *approvalModule) Provides() []core.ServiceKey { return []core.ServiceKey{core.ServiceApproval} }
func (m *approvalModule) Install(b *core.Builder, cfg core.ModuleConfig) error {
	storeAny, ok := b.Lookup(core.ServiceSessions)
	if !ok {
		return fmt.Errorf("sessions service is not available")
	}
	store, ok := storeAny.(core.SessionStore)
	if !ok {
		return fmt.Errorf("sessions service is not a SessionStore")
	}
	m.service = NewApprovalService(store, b.Hooks(), b.Config())
	return b.Provide(core.ServiceApproval, m.service)
}
func (m *approvalModule) Stop(context.Context) error { return nil }

type permissionModule struct{}

func (m *permissionModule) ID() string { return "permission" }
func (m *permissionModule) Requires() []core.ServiceKey {
	return []core.ServiceKey{core.ServiceSessions, core.ServiceApproval}
}
func (m *permissionModule) Provides() []core.ServiceKey { return nil }
func (m *permissionModule) Install(b *core.Builder, cfg core.ModuleConfig) error {
	storeAny, ok := b.Lookup(core.ServiceSessions)
	if !ok {
		return fmt.Errorf("sessions service is not available")
	}
	store := storeAny.(core.SessionStore)
	approvalAny, ok := b.Lookup(core.ServiceApproval)
	if !ok {
		return fmt.Errorf("approval service is not available")
	}
	approval := approvalAny.(core.ApprovalService)
	engine := &Engine{Store: store, Config: b.Config(), Approval: approval}
	b.Hooks().ToolsPreExecute.Use(engine.Handle)
	return nil
}
func (m *permissionModule) Stop(context.Context) error { return nil }
