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
	// Team is true when the turn can delegate: it runs as a team, or the agent
	// lists at least one subagent. Either way the task tools are offered.
	Team bool
	// CalendarRegistered is true when the registry has the calendar tool.
	CalendarRegistered bool
	// SendFileRegistered is true when the registry has the send_file tool.
	SendFileRegistered bool
	// Discussion is true for a team lead's root turn (spec 47 REQ-RDS-010).
	Discussion bool
	// WorkflowBuilder is true for a root turn on channel "workflow" when the
	// registry has the workflow_builder tool (spec 51 REQ-WFC-003). The tool
	// itself refuses sessions that are not a guardian chat.
	WorkflowBuilder bool
}

// DiscussTools are the room discussion tools a team lead is offered.
var DiscussTools = []string{"discuss_start", "discuss_ask", "discuss_more", "discuss_end"}

// CalendarTool is the calendar tool name. REQ-CAL-009: every agent is offered
// it without listing it in [agents.<name>].tools, root turn or child session,
// unless the owner lists it in disabled_tools.
const CalendarTool = "calendar"

// SendFileTool is the send_file tool name. REQ-AGF-002: every agent is offered
// it without listing it, like the calendar tool, unless the owner lists it in
// disabled_tools.
const SendFileTool = "send_file"

// WorkflowBuilderTool is the workflow_builder tool name (spec 51).
const WorkflowBuilderTool = "workflow_builder"

// AutoOfferedTools returns the auto-offered built-in tools in offer order.
// Tools listed in [agents.<name>].tools are not part of this list; callers
// combine both sources and then apply disabled_tools.
func AutoOfferedTools(state AutoOfferState) []string {
	names := make([]string, 0, 6)
	if state.VisibleSkills {
		names = append(names, "skill_load")
	}
	if state.Root && state.MCPManageRegistered {
		names = append(names, "mcp_manage")
	}
	if state.Team {
		names = append(names, "task", "task_continue")
	}
	if state.CalendarRegistered {
		names = append(names, CalendarTool)
	}
	if state.SendFileRegistered {
		names = append(names, SendFileTool)
	}
	if state.Discussion {
		names = append(names, DiscussTools...)
	}
	if state.WorkflowBuilder {
		names = append(names, WorkflowBuilderTool)
	}
	return names
}
