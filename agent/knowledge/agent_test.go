package knowledge

import (
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// AC-KNU-007 (REQ-KNU-030): the default knowledge agent is the first agent in
// name order whose tools list has a knowledge tool.
func TestKnowledgeAgentDefaultSelection(t *testing.T) {
	cfg := core.Config{Agents: map[string]core.AgentConfig{
		"zeta":  {Tools: []string{"knowledge_read"}},
		"alpha": {Tools: []string{"knowledge_write"}},
		"beta":  {Tools: []string{"shell", "knowledge_search"}},
		"gamma": {Tools: []string{"shell"}},
	}}
	name, reason := knowledgeAgent(cfg)
	if name != "alpha" {
		t.Fatalf("knowledgeAgent = %q, want alpha", name)
	}
	if reason != "" {
		t.Fatalf("reason = %q, want empty", reason)
	}
}

// AC-KNU-007: knowledge.agent wins over the automatic selection.
func TestKnowledgeAgentOverrideWins(t *testing.T) {
	cfg := core.Config{
		Knowledge: core.KnowledgeConfig{Agent: "gamma"},
		Agents: map[string]core.AgentConfig{
			"alpha": {Tools: []string{"knowledge_write"}},
			"gamma": {Tools: []string{"shell"}},
		},
	}
	name, reason := knowledgeAgent(cfg)
	if name != "gamma" || reason != "" {
		t.Fatalf("knowledgeAgent = %q/%q, want gamma", name, reason)
	}
}

// AC-KNU-007: without a knowledge tool, the selection is empty with the status
// reason.
func TestKnowledgeAgentNone(t *testing.T) {
	cfg := core.Config{Agents: map[string]core.AgentConfig{
		"alpha": {Tools: []string{"shell"}},
		"beta":  {Tools: []string{"read_file"}},
	}}
	name, reason := knowledgeAgent(cfg)
	if name != "" {
		t.Fatalf("knowledgeAgent = %q, want empty", name)
	}
	if reason != "no agent has a knowledge tool; set knowledge.agent" {
		t.Fatalf("reason = %q", reason)
	}
}

// REQ-KNU-030: hasKnowledgeTool accepts any one of the four knowledge tools.
func TestHasKnowledgeTool(t *testing.T) {
	for _, tool := range knowledgeToolNames {
		if !hasKnowledgeTool(core.AgentConfig{Tools: []string{"shell", tool}}) {
			t.Fatalf("hasKnowledgeTool does not accept %q", tool)
		}
	}
	if hasKnowledgeTool(core.AgentConfig{Tools: []string{"shell"}}) {
		t.Fatal("hasKnowledgeTool accepted an agent without a knowledge tool")
	}
}

// REQ-CCR-007: the knowledge upkeep agent is never a runtime agent, whether
// picked by default or named in knowledge.agent.
func TestKnowledgeAgentSkipsRuntimeAgents(t *testing.T) {
	cfg := core.Config{Agents: map[string]core.AgentConfig{
		"alpha": {Runtime: "claude-code", Tools: []string{"knowledge_read"}},
		"beta":  {Tools: []string{"knowledge_read"}},
	}}
	if name, _ := knowledgeAgent(cfg); name != "beta" {
		t.Fatalf("knowledgeAgent = %q, want beta", name)
	}
	cfg.Knowledge.Agent = "alpha"
	name, reason := knowledgeAgent(cfg)
	if name != "" || reason == "" {
		t.Fatalf("knowledgeAgent = %q, %q", name, reason)
	}
}
