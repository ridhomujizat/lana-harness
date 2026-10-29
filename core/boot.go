package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// BootInput adalah masukan Boot.
type BootInput struct {
	Config  Config
	Secrets Secrets
	Profile string
}

// ShutdownTimeout adalah batas total Shutdown.
const ShutdownTimeout = 30 * time.Second

// shutdownTimeout adalah variabel uji untuk budget shutdown.
var shutdownTimeout = ShutdownTimeout

type plannedModule struct {
	row  ModuleRow
	mod  Module
	prov []ServiceKey
	req  []ServiceKey
}

type installedModule struct {
	row      ModuleRow
	mod      Module
	releases []func()
}

// Boot membuat Context. Tidak ada modul yang di-install jika rencana boot gagal.
func Boot(ctx context.Context, in BootInput) (*Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b := newBuilder(in.Config)
	b.hooks.ApprovalRequest.SetDefault(func(context.Context, ApprovalRequest) (ApprovalOutcome, error) {
		return OutcomeUnavailable, nil
	})
	plan, err := planBoot(in.Config.Modules)
	if err != nil {
		return nil, err
	}
	var installed []*installedModule
	for _, pm := range plan {
		b.mu.Lock()
		b.currentID = pm.row.ID
		b.mu.Unlock()
		start := time.Now()
		err := pm.mod.Install(b, ModuleConfig{ID: pm.row.ID, Module: pm.row.Module, Values: pm.row.Config})
		if err != nil {
			rollback(installed, b)
			return nil, fmt.Errorf("modul %s gagal dipasang: %w", pm.row.ID, err)
		}
		installed = append(installed, &installedModule{row: pm.row, mod: pm.mod, releases: b.takeOwned(pm.row.ID)})
		slog.Info("modul terpasang", "id", pm.row.ID, "module", pm.row.Module, "durasi", time.Since(start))
	}

	services := b.serviceSnapshot()
	out := &Context{
		Hooks:     b.hooks,
		Config:    in.Config,
		Secrets:   in.Secrets,
		Profile:   in.Profile,
		services:  services,
		installed: installed,
	}
	if err := assignContext(out, services); err != nil {
		rollback(installed, b)
		return nil, err
	}
	return out, nil
}

func assignContext(c *Context, services map[ServiceKey]any) error {
	assign := func(key ServiceKey, into any) error {
		svc, ok := services[key]
		if !ok {
			return nil
		}
		switch dst := into.(type) {
		case *LLMRegistry:
			v, ok := svc.(LLMRegistry)
			if !ok {
				return fmt.Errorf("service '%s' bukan tipe LLMRegistry", key)
			}
			*dst = v
		case *ToolRegistry:
			v, ok := svc.(ToolRegistry)
			if !ok {
				return fmt.Errorf("service '%s' bukan tipe ToolRegistry", key)
			}
			*dst = v
		case *SessionStore:
			v, ok := svc.(SessionStore)
			if !ok {
				return fmt.Errorf("service '%s' bukan tipe SessionStore", key)
			}
			*dst = v
		case *AgentRegistry:
			v, ok := svc.(AgentRegistry)
			if !ok {
				return fmt.Errorf("service '%s' bukan tipe AgentRegistry", key)
			}
			*dst = v
		case *ApprovalService:
			v, ok := svc.(ApprovalService)
			if !ok {
				return fmt.Errorf("service '%s' bukan tipe ApprovalService", key)
			}
			*dst = v
		}
		return nil
	}
	for _, item := range []struct {
		key  ServiceKey
		into any
	}{
		{ServiceLLM, &c.LLM},
		{ServiceTools, &c.Tools},
		{ServiceSessions, &c.Sessions},
		{ServiceAgents, &c.Agents},
		{ServiceApproval, &c.Approval},
	} {
		if err := assign(item.key, item.into); err != nil {
			return err
		}
	}
	return nil
}

func planBoot(rows []ModuleRow) ([]plannedModule, error) {
	var plan []plannedModule
	registered := ModuleNames()
	for _, row := range rows {
		if row.Disabled {
			continue
		}
		factory, ok := moduleFactory(row.Module)
		if !ok {
			return nil, fmt.Errorf("modul '%s' (baris %s) tidak dikenal (tersedia: %s)", row.Module, row.ID, strings.Join(registered, ", "))
		}
		mod := factory()
		plan = append(plan, plannedModule{row: row, mod: mod, prov: mod.Provides(), req: mod.Requires()})
	}
	providers := map[ServiceKey]int{}
	for i, pm := range plan {
		for _, key := range pm.prov {
			if j, ok := providers[key]; ok {
				return nil, fmt.Errorf("service '%s' dipasang lebih dari satu modul: %s, %s", key, plan[j].row.ID, pm.row.ID)
			}
			providers[key] = i
		}
	}
	for _, pm := range plan {
		for _, key := range pm.req {
			if _, ok := providers[key]; !ok {
				return nil, fmt.Errorf("modul %s membutuhkan service '%s' yang tidak dipasang modul aktif", pm.row.ID, key)
			}
		}
	}
	deps := make([][]int, len(plan))
	for i, pm := range plan {
		seen := map[int]bool{}
		for _, key := range pm.req {
			j := providers[key]
			if !seen[j] {
				seen[j] = true
				deps[i] = append(deps[i], j)
			}
		}
	}
	if cycle := findCycle(plan, deps); len(cycle) > 0 {
		ids := make([]string, len(cycle))
		for i, idx := range cycle {
			ids[i] = plan[idx].row.ID
		}
		return nil, fmt.Errorf("siklus dependency: %s", strings.Join(ids, " → "))
	}
	order := topoOrder(deps)
	if len(order) != len(plan) {
		return nil, errors.New("siklus dependency terdeteksi")
	}
	out := make([]plannedModule, 0, len(plan))
	for _, idx := range order {
		out = append(out, plan[idx])
	}
	return out, nil
}

func findCycle(plan []plannedModule, deps [][]int) []int {
	state := make([]int, len(plan))
	var stack []int
	var cycle []int
	var visit func(int) bool
	visit = func(i int) bool {
		state[i] = 1
		stack = append(stack, i)
		for _, dep := range deps[i] {
			if state[dep] == 0 {
				if visit(dep) {
					return true
				}
			} else if state[dep] == 1 {
				start := 0
				for j, idx := range stack {
					if idx == dep {
						start = j
						break
					}
				}
				cycle = append(append([]int{}, stack[start:]...), dep)
				return true
			}
		}
		stack = stack[:len(stack)-1]
		state[i] = 2
		return false
	}
	for i := range plan {
		if state[i] == 0 && visit(i) {
			return cycle
		}
	}
	return nil
}

func topoOrder(deps [][]int) []int {
	n := len(deps)
	indegree := make([]int, n)
	dependents := make([][]int, n)
	for i, list := range deps {
		indegree[i] = len(list)
		for _, dep := range list {
			dependents[dep] = append(dependents[dep], i)
		}
	}
	done := make([]bool, n)
	order := make([]int, 0, n)
	for len(order) < n {
		pick := -1
		for i := 0; i < n; i++ {
			if !done[i] && indegree[i] == 0 {
				pick = i
				break
			}
		}
		if pick < 0 {
			break
		}
		done[pick] = true
		order = append(order, pick)
		for _, next := range dependents[pick] {
			indegree[next]--
		}
	}
	return order
}

func rollback(installed []*installedModule, b *Builder) {
	for i := len(installed) - 1; i >= 0; i-- {
		im := installed[i]
		if err := im.mod.Stop(context.Background()); err != nil {
			slog.Warn("modul gagal berhenti saat rollback", "id", im.row.ID, "error", err)
		}
		releaseAll(im.releases)
	}
	installed = installed[:0]
	_ = b
}

// Shutdown menghentikan modul dalam urutan kebalikan install.
func (c *Context) Shutdown(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c.shutdownOnce.Do(func() {
		c.shutdownErr = c.doShutdown(ctx)
	})
	return c.shutdownErr
}

func (c *Context) doShutdown(ctx context.Context) error {
	budget := shutdownTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < budget {
			budget = remaining
		}
	}
	if budget < 0 {
		budget = 0
	}
	sctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var releases []func()
	for i := len(c.installed) - 1; i >= 0; i-- {
		im := c.installed[i]
		done := make(chan struct{})
		go func(m Module) {
			defer close(done)
			if err := m.Stop(sctx); err != nil {
				slog.Warn("modul gagal berhenti", "id", im.row.ID, "error", err)
			}
		}(im.mod)
		select {
		case <-done:
			slog.Info("modul berhenti", "id", im.row.ID)
		case <-sctx.Done():
			slog.Warn("modul melewati batas shutdown dan ditinggalkan", "id", im.row.ID)
		}
		releases = append(releases, im.releases...)
		im.releases = nil
	}
	for i := len(releases) - 1; i >= 0; i-- {
		if releases[i] != nil {
			releases[i]()
		}
	}
	if c.Hooks != nil && c.Hooks.SessionEvent != nil {
		c.Hooks.SessionEvent.Close()
	}
	return nil
}

func releaseAll(releases []func()) {
	for i := len(releases) - 1; i >= 0; i-- {
		if releases[i] != nil {
			releases[i]()
		}
	}
}

func sortedServiceNames(m map[ServiceKey]string) []string {
	names := make([]string, 0, len(m))
	for key := range m {
		names = append(names, string(key))
	}
	sort.Strings(names)
	return names
}

var _ = sortedServiceNames
