package core

// StandardTools is the toolset every agent gets unless its toolset is
// "custom": files, shell, web, memory, knowledge, and asking the owner
// (Hermes-style default; owner decision 2026-11-11).
var StandardTools = []string{
	"read_file", "write_file", "edit_file", "glob", "grep",
	"bash",
	"web_fetch",
	"memory_read", "memory_write",
	"knowledge_search", "knowledge_read", "knowledge_write",
	"ask_user",
}

// ToolsetCustom limits an agent to its own tools list.
const ToolsetCustom = "custom"

// EffectiveTools is the agent's tools list with the standard toolset added
// (unless toolset is "custom"), without duplicates, in a stable order:
// standard tools first, then the agent's own. disabled_tools is applied
// later, where the turn builds its offer.
func EffectiveTools(cfg AgentConfig) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	if cfg.Toolset != ToolsetCustom {
		for _, name := range StandardTools {
			add(name)
		}
	}
	for _, name := range cfg.Tools {
		add(name)
	}
	return out
}

// DefaultPolicy is the permission of an agent that sets none.
const DefaultPolicy = PolicyAuto
