package agent

// AutoOfferState is the root-turn state that decides which built-in tools an
// agent is offered without listing them in [agents.<name>].tools. The turn
// tool list and the dashboard catalog both build it from the same rules.
type AutoOfferState struct {
	// Root is false for a child session; only a root turn gets mcp_manage.
	Root bool
	// MCPManageRegistered is true when the registry has mcp_manage.
	MCPManageRegistered bool
	// VisibleSkills is true when the agent has at least one visible skill.
	VisibleSkills bool
	// Team is true when the turn runs as a team.
	Team bool
}

// AutoOfferedTools returns the auto-offered built-in tools in offer order.
// Tools listed in [agents.<name>].tools are not part of this list; callers
// combine both sources and then apply disabled_tools.
func AutoOfferedTools(state AutoOfferState) []string {
	names := make([]string, 0, 4)
	if state.VisibleSkills {
		names = append(names, "skill_load")
	}
	if state.Root && state.MCPManageRegistered {
		names = append(names, "mcp_manage")
	}
	if state.Team {
		names = append(names, "task", "task_continue")
	}
	return names
}
