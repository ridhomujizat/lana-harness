package knowledge

import (
	"sort"

	"github.com/ridhomujizat/lana-harness/core"
)

// knowledgeToolNames are the tool names that mark an agent as able to run the
// knowledge upkeep passes (REQ-KNU-030, design §3).
var knowledgeToolNames = []string{"knowledge_search", "knowledge_read", "knowledge_write", "knowledge_ingest"}

// knowledgeAgent returns the agent that runs knowledge upkeep.
// REQ-KNU-030: it is knowledge.agent when set, else the first agent in sorted
// name order whose tools list has a knowledge tool. Without one it returns ""
// and a reason for the status.
func knowledgeAgent(cfg core.Config) (name string, reason string) {
	// REQ-CCT-007: a Claude Code agent runs the passes with its background
	// chain.
	if cfg.Knowledge.Agent != "" {
		return cfg.Knowledge.Agent, ""
	}
	names := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if agent := cfg.Agents[name]; hasKnowledgeTool(agent) {
			return name, ""
		}
	}
	return "", "no agent has a knowledge tool; set knowledge.agent"
}

// hasKnowledgeTool reports whether agentCfg lists any knowledge tool
// (REQ-KNU-003, REQ-KNU-030).
func hasKnowledgeTool(agentCfg core.AgentConfig) bool {
	for _, tool := range agentCfg.Tools {
		for _, name := range knowledgeToolNames {
			if tool == name {
				return true
			}
		}
	}
	return false
}

// domainAgent reports whether name is a configured agent that owns a
// knowledge domain: it exists in cfg.Agents and has a knowledge tool
// (design §4).
func domainAgent(cfg core.Config, name string) bool {
	if name == "" {
		return false
	}
	agentCfg, ok := cfg.Agents[name]
	return ok && hasKnowledgeTool(agentCfg)
}

// domainAgents returns the configured domain agents in name order
// (design §4).
func domainAgents(cfg core.Config) []string {
	out := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		if domainAgent(cfg, name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
