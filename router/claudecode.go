package router

import (
	"fmt"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
	"github.com/ridhomujizat/lana-harness/internal/models"
)

// ClaudeCode is the built-in provider of Claude Code targets (ADR 0020). It
// is never a configured provider: a turn whose first target is
// "claude-code/<model>" runs in the Claude Code runtime, outside the router.
const ClaudeCode = core.ClaudeCodeProvider

// ClaudeCodeLabelPrefix starts the picker label of a Claude Code model
// (API-CCT-004).
const ClaudeCodeLabelPrefix = "Claude Code · "

// claudeCodeTarget checks the model of a claude-code target (REQ-CCT-001).
func claudeCodeTarget(target Target) error {
	if !llmcall.ValidClaudeCodeModel(target.Model) {
		return fmt.Errorf("%w: invalid claude-code model %q", ErrUnknownAddress, target.Model)
	}
	return nil
}

// SetClaudeCodeModels sets the models Claude Code reported (the initialize
// response's models list). Models("claude-code") lists them after the
// aliases; nil goes back to the catalog fallback.
func (r *Router) SetClaudeCodeModels(list []core.RuntimeModel) {
	if list == nil {
		r.claudeModels.Store(nil)
		return
	}
	cp := append([]core.RuntimeModel(nil), list...)
	r.claudeModels.Store(&cp)
}

// ClaudeCodeModels returns the models Claude Code reported, and whether a list
// was set.
func (r *Router) ClaudeCodeModels() ([]core.RuntimeModel, bool) {
	p := r.claudeModels.Load()
	if p == nil {
		return nil, false
	}
	return append([]core.RuntimeModel(nil), (*p)...), true
}

// Sources of Models("claude-code") rows.
const (
	ClaudeSourceAlias      = "alias"
	ClaudeSourceClaudeCode = "claude_code"
	ClaudeSourceCatalog    = "catalog"
	ClaudeSourceCustom     = "custom"
)

// claudeCodeModels is Models("claude-code") (API-CCT-004): the aliases opus,
// sonnet, and haiku; then the models Claude Code reported in its initialize
// response, else the Claude models of the catalog; then the IDs the owner
// added in [router] claude_code_models. Model facts (context window, vision,
// thinking, price) come from the catalog by ID; an alias takes the newest
// catalog model of its family.
func (r *Router) claudeCodeModels() []ModelInfo {
	out := make([]ModelInfo, 0, 16)
	seen := map[string]bool{}
	add := func(id, label, source string) {
		if id == "" || seen[id] || !llmcall.ValidClaudeCodeModel(id) {
			return
		}
		seen[id] = true
		info := ModelInfo{ID: id}
		if known, ok := claudeCatalogModel(id); ok {
			info = fromCatalog(known)
			info.ID = id
		}
		info.Source = source
		info.Address = ClaudeCode + "/" + id
		info.Name = ClaudeCodeLabelPrefix + label
		out = append(out, info)
	}
	for _, alias := range llmcall.ClaudeCodeAliases {
		add(alias, aliasLabel(alias), ClaudeSourceAlias)
	}
	if reported, ok := r.ClaudeCodeModels(); ok && len(reported) > 0 {
		for _, m := range reported {
			label := strings.TrimSpace(m.DisplayName)
			if label == "" {
				label = m.Value
			}
			add(m.Value, label, ClaudeSourceClaudeCode)
		}
	} else {
		for _, m := range models.ForKind("anthropic") {
			add(m.ID, strings.TrimPrefix(m.Name, "Claude "), ClaudeSourceCatalog)
		}
	}
	for _, id := range r.snap.Load().cfg.ClaudeCodeModels {
		add(strings.TrimSpace(id), strings.TrimSpace(id), ClaudeSourceCustom)
	}
	return out
}

// aliasLabel is the picker label of an alias: "Opus (latest)".
func aliasLabel(alias string) string {
	if alias == "" {
		return alias
	}
	return strings.ToUpper(alias[:1]) + alias[1:] + " (latest)"
}

// claudeCatalogModel finds the catalog entry of a Claude Code model ID. An
// alias (opus, sonnet, haiku) is the first catalog model of its family in
// picker order, which is the newest; a suffix such as "[1m]" is ignored.
func claudeCatalogModel(id string) (models.Model, bool) {
	base, _, _ := strings.Cut(id, "[")
	if known, ok := models.Lookup(base); ok {
		return known, true
	}
	for _, m := range models.ForKind("anthropic") {
		if strings.Contains(m.ID, "-"+base+"-") {
			return m, true
		}
	}
	return models.Model{}, false
}
