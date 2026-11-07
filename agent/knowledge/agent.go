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
	if cfg.Knowledge.Agent != "" {
		// REQ-CCR-007: background passes never use a runtime agent.
		if agent, ok := cfg.Agents[cfg.Knowledge.Agent]; ok && agent.Runtime != "" {
			return "", "knowledge.agent '" + cfg.Knowledge.Agent + "' is a Claude Agent; background passes cannot use it"
		}
		return cfg.Knowledge.Agent, ""
	}
	names := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if agent := cfg.Agents[name]; agent.Runtime == "" && hasKnowledgeTool(agent) {
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
