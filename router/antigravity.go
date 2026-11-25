package router

import (
	"fmt"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
)

// Antigravity is the built-in provider of Antigravity targets (ADR 0022). It
// is never a configured provider: a turn whose first target is
// "antigravity/<model>" runs in the Antigravity runtime, outside the router.
const Antigravity = core.AntigravityProvider

// AntigravityLabelPrefix starts the picker label of an Antigravity model
// (API-AGY-026).
const AntigravityLabelPrefix = "Antigravity · "

// AntigravitySourceRuntime is the source of a Models(account) row that comes
// from the account's reported `agy models` list (API-AGY-026).
const AntigravitySourceRuntime = "runtime"

// antigravityTarget checks the model of an Antigravity target (REQ-AGY-001):
// "default" (agy's current model) or a model ID that never looks like a flag.
func antigravityTarget(target Target) error {
	if target.Model != "default" && !llmcall.ValidAntigravityModel(target.Model) {
		return fmt.Errorf("%w: invalid antigravity model %q", ErrUnknownAddress, target.Model)
	}
	return nil
}

// isAntigravity reports whether name is an Antigravity account: the built-in
// "antigravity" or a provider of kind "antigravity" (REQ-AGY-001).
func (s *snapshot) isAntigravity(name string) bool {
	if name == Antigravity {
		return true
	}
	_, ok := s.agy[name]
	return ok
}

// AntigravityAccount returns the config of an Antigravity account
// (REQ-AGY-001): a provider of kind "antigravity", or the built-in
// "antigravity" with the settings of its optional [providers.antigravity]
// table (API-AGY-003; empty but for the kind without one). It implements
// llmcall.AntigravityAccounts.
func (r *Router) AntigravityAccount(name string) (core.ProviderConfig, bool) {
	snap := r.snap.Load()
	if name == Antigravity {
		pc, ok := snap.agy[Antigravity]
		if !ok {
			return core.ProviderConfig{Kind: core.AntigravityKind}, true
		}
		return pc, true
	}
	pc, ok := snap.agy[name]
	return pc, ok
}

// AntigravityAccounts returns the names of the Antigravity accounts
// (REQ-AGY-001): the built-in "antigravity" first, then the providers of kind
// "antigravity", sorted.
func (r *Router) AntigravityAccounts() []string {
	out := []string{Antigravity}
	for _, name := range sortedKeys(r.snap.Load().agy) {
		if name != Antigravity {
			out = append(out, name)
		}
	}
	return out
}

// SetRuntimeModels sets the models an Antigravity account reported (its
// `agy models` list, REQ-AGY-009). Models(account) lists them with the
// account's addresses. A nil list or an empty account stores nothing.
func (r *Router) SetRuntimeModels(account string, models []core.RuntimeModel) {
	if account == "" {
		return
	}
	cp := append([]core.RuntimeModel(nil), models...)
	r.agyModelsMu.Lock()
	defer r.agyModelsMu.Unlock()
	if r.agyModels == nil {
		r.agyModels = map[string][]core.RuntimeModel{}
	}
	r.agyModels[account] = cp
}

// RuntimeModels returns the models the Antigravity account reported, a copy
// of the stored list. A nil or unknown account returns an empty slice.
func (r *Router) RuntimeModels(account string) []core.RuntimeModel {
	r.agyModelsMu.Lock()
	defer r.agyModelsMu.Unlock()
	return append([]core.RuntimeModel{}, r.agyModels[account]...)
}

// antigravityModels is Models(account) for an Antigravity account
// (API-AGY-026): the account's reported model list (SetRuntimeModels), each
// as "<account>/<id>" with the label "Antigravity · <name>". It never returns
// a catalog; an account that reported nothing lists nothing.
func (r *Router) antigravityModels(account string) []ModelInfo {
	reported := r.RuntimeModels(account)
	out := make([]ModelInfo, 0, len(reported))
	seen := map[string]bool{}
	for _, m := range reported {
		id := m.Value
		if id == "" || seen[id] || (id != "default" && !llmcall.ValidAntigravityModel(id)) {
			continue
		}
		seen[id] = true
		label := strings.TrimSpace(m.DisplayName)
		if label == "" {
			label = id
		}
		out = append(out, ModelInfo{
			ID:      id,
			Address: account + "/" + id,
			Name:    AntigravityLabelPrefix + label,
			Source:  AntigravitySourceRuntime,
		})
	}
	return out
}
