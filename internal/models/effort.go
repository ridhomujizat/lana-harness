package models

import (
	"regexp"
	"slices"
	"strings"
)

// Spec 76 (revision 2026-11-26): a model's effort levels are its own level
// names (OpenAI "minimal".."high", Anthropic "low".."max", Antigravity the
// variant suffixes agy lists). There is no shared vocabulary and no mapping
// between providers: a value the model lists is sent as is; any other value
// falls back to the model's default.

// EffortOff is lana's internal "no thinking" switch. Background calls (titles,
// knowledge, avatars) send it on any model, and each adapter turns it into
// its own off setting (OpenAI's lowest level, Anthropic's off mode, DeepSeek
// thinking disabled) as before spec 76.
const EffortOff = "off"

// effortToken is a reasoning_effort value: one token of letters, digits, "-"
// and "_", at most 20 characters, not starting with "-" or "_".
var effortToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,19}$`)

// ValidEffort reports whether value is an acceptable reasoning_effort: "" (the
// model's default) or a single token (REQ-MES-001, revision 2026-11-26).
func ValidEffort(value string) bool {
	return value == "" || effortToken.MatchString(value)
}

// PickEffort returns requested when supported lists it, else def (the
// model's default; "" sends nothing). An empty requested value returns def.
func PickEffort(requested string, supported []string, def string) string {
	if requested != "" && slices.Contains(supported, requested) {
		return requested
	}
	return def
}

// lookupStatic returns the static catalog entry of a model, never the
// registered fallback: only the static catalog knows effort levels.
func lookupStatic(model string) (Model, bool) {
	for candidate := model; candidate != ""; {
		if entry, ok := catalog[candidate]; ok {
			return entry, true
		}
		if strings.HasPrefix(candidate, "claude-") && strings.Contains(candidate, ".") {
			if entry, ok := catalog[strings.ReplaceAll(candidate, ".", "-")]; ok {
				return entry, true
			}
		}
		_, rest, ok := strings.Cut(candidate, "/")
		if !ok {
			break
		}
		candidate = rest
	}
	return Model{}, false
}

// ModelEfforts returns the effort levels a catalog model supports, in its
// own names and order, and its default (REQ-MES-002): the sent DefaultEffort,
// else the display-only DisplayDefault. known is false for a model the static
// catalog does not know; a known model without effort control returns nil.
func ModelEfforts(model string) (efforts []string, def string, known bool) {
	entry, ok := lookupStatic(model)
	if !ok {
		return nil, "", false
	}
	if len(entry.Efforts) == 0 {
		return nil, "", true
	}
	return append([]string(nil), entry.Efforts...), entry.defaultLevel(), true
}

// defaultLevel is the model's default level: DefaultEffort, else
// DisplayDefault.
func (m Model) defaultLevel() string {
	if m.DefaultEffort != "" {
		return m.DefaultEffort
	}
	return m.DisplayDefault
}

// ShownDefault returns the default level a picker shows for m (REQ-MES-002):
// DefaultEffort, else DisplayDefault; "" without effort levels.
func ShownDefault(m Model) string {
	if len(m.Efforts) == 0 {
		return ""
	}
	return m.defaultLevel()
}

// EffortFor returns the effort a request for model sends for requested
// (REQ-MES-003, revision 2026-11-26):
//
//   - "" sends nothing;
//   - "off" stays "off": the adapter applies its own off setting;
//   - a catalog model sends requested when it lists it, else its default
//     ("" when it has none, or has no effort control);
//   - a model the catalog does not know sends requested when unknown lists
//     it, else nothing; a nil unknown passes requested through.
func EffortFor(model, requested string, unknown []string) string {
	if requested == "" {
		return ""
	}
	if requested == EffortOff {
		return EffortOff
	}
	efforts, def, known := ModelEfforts(model)
	if known {
		return PickEffort(requested, efforts, def)
	}
	if unknown == nil {
		return requested
	}
	return PickEffort(requested, unknown, "")
}

// variantOrder is the order of the effort suffixes of model variants such as
// agy's "gemini-4-neon-high" (REQ-MES-005), lowest first.
var variantOrder = []string{"minimal", "low", "medium", "high", "xhigh", "max"}

// SplitEffortVariant splits a model id that ends in an effort suffix
// ("gemini-4-neon-high") into the base id and the level (REQ-MES-005). ok is
// false when the id has no such suffix or nothing before it.
func SplitEffortVariant(id string) (base, level string, ok bool) {
	i := strings.LastIndex(id, "-")
	if i <= 0 || i == len(id)-1 {
		return id, "", false
	}
	suffix := id[i+1:]
	if !slices.Contains(variantOrder, suffix) {
		return id, "", false
	}
	return id[:i], suffix, true
}

// OrderVariantEfforts returns the variant suffixes of list in level order,
// lowest first, without duplicates.
func OrderVariantEfforts(list []string) []string {
	var out []string
	for _, level := range variantOrder {
		if slices.Contains(list, level) {
			out = append(out, level)
		}
	}
	return out
}

// VariantDefault is the default level of a model offered as effort variants
// (REQ-MES-002, Antigravity): "high" when listed, else the first.
func VariantDefault(efforts []string) string {
	if slices.Contains(efforts, "high") {
		return "high"
	}
	if len(efforts) > 0 {
		return efforts[0]
	}
	return ""
}

// claudeCodeEfforts are the --effort levels of Claude Code (REQ-MES-002).
var claudeCodeEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// ClaudeCodeEffortDefault is Claude Code's default effort.
const ClaudeCodeEffortDefault = "xhigh"

// ClaudeCodeEfforts returns the --effort levels of Claude Code, in order.
func ClaudeCodeEfforts() []string {
	return append([]string(nil), claudeCodeEfforts...)
}
