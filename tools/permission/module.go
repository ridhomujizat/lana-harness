// Package permission menyediakan modul permission dan approval (spec #4).
package permission

import (
	"context"

	"github.com/ridhomujizat/lana-harness/core"
)

func init() {
	core.RegisterModule("permission", func() core.Module { return &permissionModule{} })
	core.RegisterModule("approval", func() core.Module { return &approvalModule{} })
}

type permissionModule struct{}

func (m *permissionModule) ID() string                                     { return "permission" }
func (m *permissionModule) Requires() []core.ServiceKey                    { return nil }
func (m *permissionModule) Provides() []core.ServiceKey                    { return nil }
func (m *permissionModule) Install(*core.Builder, core.ModuleConfig) error { return nil }
func (m *permissionModule) Stop(context.Context) error                     { return nil }

type approvalModule struct{}

func (m *approvalModule) ID() string                                     { return "approval" }
func (m *approvalModule) Requires() []core.ServiceKey                    { return nil }
func (m *approvalModule) Provides() []core.ServiceKey                    { return []core.ServiceKey{core.ServiceApproval} }
func (m *approvalModule) Install(*core.Builder, core.ModuleConfig) error { return nil }
func (m *approvalModule) Stop(context.Context) error                     { return nil }
