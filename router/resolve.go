package router

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnknownAddress is wrapped by Resolve when a value names neither a combo
// nor a known provider.
var ErrUnknownAddress = errors.New("unknown provider or combo")

// Address formats a target as "provider/model".
func Address(t Target) string {
	return t.Provider + "/" + t.Model
}

// ParseAddress splits "provider/model" or the legacy "provider:model" at the
// first "/" or ":", whichever comes first. The model part may contain more
// slashes ("openrouter/openai/gpt-5-mini" is provider openrouter, model
// openai/gpt-5-mini). Both parts must be non-empty.
func ParseAddress(value string) (Target, bool) {
	i := strings.IndexAny(value, "/:")
	if i <= 0 || i == len(value)-1 {
		return Target{}, false
	}
	return Target{Provider: value[:i], Model: value[i+1:]}, true
}

// Resolve turns a combo name, a "provider/model" address, or a legacy
// "provider:model" value into a chain (REQ-RTR-003). A combo name wins over
// an address.
func (r *Router) Resolve(address string) ([]Target, error) {
	return r.snap.Load().resolve(address)
}

func (s *snapshot) resolve(address string) ([]Target, error) {
	if combo, ok := s.combos[address]; ok {
		chain := make([]Target, 0, len(combo.Models))
		for _, model := range combo.Models {
			target, _ := ParseAddress(model)
			chain = append(chain, target)
		}
		return chain, nil
	}
	target, ok := ParseAddress(address)
	if ok && s.isClaude(target.Provider) && address[len(target.Provider)] == '/' {
		// ADR 0020, REQ-CCT-010: a Claude Code account; the agent runs it in
		// the Claude Code runtime.
		if err := claudeCodeTarget(target); err != nil {
			return nil, err
		}
		return []Target{target}, nil
	}
	if !ok || s.providers[target.Provider] == nil {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAddress, address)
	}
	return []Target{target}, nil
}
