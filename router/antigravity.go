package router

import (
	"fmt"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
	"github.com/ridhomujizat/lana-harness/internal/models"
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
// (API-AGY-026, spec 76 REQ-MES-005): the account's reported model list
// (SetRuntimeModels), each as "<account>/<id>" with the label "Antigravity ·
// <name>". agy lists one model per effort ("gemini-4-neon-low" …
// "-xhigh"); those are listed once as the base model ("gemini-4-neon",
// "Antigravity · Gemini 4 Neon") with the suffixes as its efforts, default
// "high" when listed, else the first. A model without an effort suffix is
// listed as it is, without efforts; so are the variants of a base agy lists
// itself. It never returns a catalog; an account that reported nothing lists
// nothing.
func (r *Router) antigravityModels(account string) []ModelInfo {
	reported := r.RuntimeModels(account)
	valid := make([]core.RuntimeModel, 0, len(reported))
	listed := map[string]bool{}
	for _, m := range reported {
		id := m.Value
		if id == "" || listed[id] || (id != "default" && !llmcall.ValidAntigravityModel(id)) {
			continue
		}
		listed[id] = true
		valid = append(valid, m)
	}
	out := make([]ModelInfo, 0, len(valid))
	bases := map[string]int{}
	for _, m := range valid {
		id := m.Value
		label := strings.TrimSpace(m.DisplayName)
		if label == "" {
			label = id
		}
		if base, level, ok := models.SplitEffortVariant(id); ok && !listed[base] {
			if i, seen := bases[base]; seen {
				out[i].Efforts = append(out[i].Efforts, level)
				continue
			}
			bases[base] = len(out)
			out = append(out, ModelInfo{
				ID:      base,
				Address: account + "/" + base,
				Name:    AntigravityLabelPrefix + variantLabel(label, base),
				Source:  AntigravitySourceRuntime,
				Efforts: []string{level},
			})
			continue
		}
		out = append(out, ModelInfo{
			ID:      id,
			Address: account + "/" + id,
			Name:    AntigravityLabelPrefix + label,
			Source:  AntigravitySourceRuntime,
		})
	}
	for _, i := range bases {
		out[i].Efforts = models.OrderVariantEfforts(out[i].Efforts)
		out[i].DefaultEffort = models.VariantDefault(out[i].Efforts)
	}
	return out
}

// variantLabel is the label of a base model from one variant's label: the
// trailing " (High)" part dropped ("Gemini 4 Neon (High)" → "Gemini 4 Neon").
// A label without it, or the id itself, gives the base id.
func variantLabel(label, base string) string {
	if strings.HasSuffix(label, ")") {
		if i := strings.LastIndex(label, " ("); i > 0 {
			return strings.TrimSpace(label[:i])
		}
	}
	return base
}
