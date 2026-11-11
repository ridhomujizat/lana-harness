package core

// StandardTools is the toolset every agent gets unless its toolset is
// "custom": every built-in lana tool that works without context (owner
// decision 2026-11-12, extending the Hermes-style default of 2026-11-11):
// files, shell, web, memory and session search, knowledge, skills, asking the
// owner, sending files, the calendar, workflows and usage stats. Tools that
// need context stay auto-offered where they apply: skill_load (visible
// skills), mcp_manage (root turns), task/task_continue (delegates),
// discuss_* (team leads) and workflow_builder (the guardian chat).
var StandardTools = []string{
	"read_file", "write_file", "edit_file", "glob", "grep", "send_file",
	"bash",
	"web_search", "web_fetch",
	"memory_read", "memory_write", "session_search",
	"knowledge_search", "knowledge_read", "knowledge_write", "knowledge_ingest",
	"skill_write",
	"ask_user",
	// Calendar items and workflows, so any agent can set a reminder or build
	// and test an automation from chat (owner request 2026-11-11).
	"calendar", "workflow",
	"usage_stats",
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

// DefaultPolicy is the permission of an agent that sets none: trust, so an
// agent runs every tool without asking (owner decision 2026-11-12).
const DefaultPolicy = PolicyTrust
