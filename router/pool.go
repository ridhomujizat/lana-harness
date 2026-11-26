package router

import (
	"sort"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/models"
)

// Default pool priorities (REQ-PAF-001): the built-in account first, then
// the others.
const (
	// BuiltinPoolPriority is the priority of a built-in account (claude-code,
	// antigravity) whose table sets none.
	BuiltinPoolPriority = 0
	// AccountPoolPriority is the priority of any other account that sets
	// none.
	AccountPoolPriority = 100
)

// PoolAccounts returns the accounts a pooled address of runtime
// ("claude-code" or "antigravity") may use, in pool order (REQ-PAF-001): the
// built-in account and every account of that runtime, leaving out disabled
// ones and ones with pool = false, sorted by priority (lower first) then
// name. Another runtime returns nil. It implements llmcall's pool source.
func (r *Router) PoolAccounts(runtime string) []string {
	snap := r.snap.Load()
	accounts := snap.runtimeAccounts(runtime)
	if accounts == nil {
		return nil
	}
	type account struct {
		name     string
		priority int
	}
	list := make([]account, 0, len(accounts))
	for name, pc := range accounts {
		if pc.Disabled || !pc.InPool() {
			continue
		}
		list = append(list, account{name: name, priority: poolPriority(runtime, name, pc)})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].priority != list[j].priority {
			return list[i].priority < list[j].priority
		}
		return list[i].name < list[j].name
	})
	out := make([]string, len(list))
	for i, a := range list {
		out[i] = a.name
	}
	return out
}

// AccountPriority returns the pool priority of a Claude Code or Antigravity
// account (REQ-PAF-001): its priority, else the default (0 for a built-in
// account, 100 for the others). It reports false for any other name.
func (r *Router) AccountPriority(name string) (int, bool) {
	snap := r.snap.Load()
	for _, runtime := range []string{core.ClaudeCodeKind, core.AntigravityKind} {
		if pc, ok := snap.runtimeAccounts(runtime)[name]; ok {
			return poolPriority(runtime, name, pc), true
		}
	}
	return 0, false
}

// runtimeAccounts returns every account of a runtime by name, the built-in
// one included, or nil for another runtime.
func (s *snapshot) runtimeAccounts(runtime string) map[string]core.ProviderConfig {
	switch runtime {
	case core.ClaudeCodeKind:
		out := make(map[string]core.ProviderConfig, len(s.claude)+1)
		for name, pc := range s.claude {
			out[name] = pc
		}
		out[ClaudeCode] = s.builtin
		return out
	case core.AntigravityKind:
		out := make(map[string]core.ProviderConfig, len(s.agy)+1)
		for name, pc := range s.agy {
			out[name] = pc
		}
		if _, ok := out[Antigravity]; !ok {
			out[Antigravity] = core.ProviderConfig{Kind: core.AntigravityKind}
		}
		return out
	}
	return nil
}

// poolPriority is the priority of an account: its own, else the default.
func poolPriority(runtime, name string, pc core.ProviderConfig) int {
	if pc.Priority != nil {
		return *pc.Priority
	}
	if name == runtime {
		// The built-in account is named after its runtime.
		return BuiltinPoolPriority
	}
	return AccountPoolPriority
}

// APIPool returns the connection pool of the API connection name, in pool
// order (spec 80 REQ-ACP-001): the enabled API connections with name's kind
// and effective base URL that do not set pool = false, sorted by priority
// (lower first; default 100) then name. It returns nil when name is not an
// enabled API connection in the pool, or when its pool has fewer than two
// connections: its address then uses only name.
func (r *Router) APIPool(name string) []string {
	return r.snap.Load().apiPool(name)
}

// apiPool is APIPool on one snapshot.
func (s *snapshot) apiPool(name string) []string {
	entry, ok := s.providers[name]
	if !ok || !entry.apiConnection() || entry.cfg.Disabled || !entry.cfg.InPool() {
		return nil
	}
	group := models.PoolGroup(entry.kind, entry.cfg.BaseURL)
	type member struct {
		name     string
		priority int
	}
	var list []member
	for other, e := range s.providers {
		if !e.apiConnection() || e.cfg.Disabled || !e.cfg.InPool() {
			continue
		}
		if e.kind != entry.kind || models.PoolGroup(e.kind, e.cfg.BaseURL) != group {
			continue
		}
		priority := AccountPoolPriority
		if e.cfg.Priority != nil {
			priority = *e.cfg.Priority
		}
		list = append(list, member{name: other, priority: priority})
	}
	if len(list) < 2 {
		return nil
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].priority != list[j].priority {
			return list[i].priority < list[j].priority
		}
		return list[i].name < list[j].name
	})
	out := make([]string, len(list))
	for i, m := range list {
		out[i] = m.name
	}
	return out
}

// apiConnection reports whether the entry is an API connection of the
// config (spec 80): not one added with WithProviders.
func (e *providerEntry) apiConnection() bool {
	return e != nil && !e.custom && e.kind != core.ClaudeCodeKind && e.kind != core.AntigravityKind
}
