package agenthome

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// RosterEntry is one teammate in the team roster section of a prompt.
type RosterEntry struct {
	Name        string
	Description string
}

// PromptInput is the data for one system prompt.
type PromptInput struct {
	Persona        string
	User           []string
	Memory         []string
	Skills         []Skill
	Roster         []RosterEntry
	Agent          string
	Channel        string
	Workspace      string
	UnavailableMCP map[string]string
	Now            time.Time
}

// blockingRules is the fixed section that tells the agent how to report a
// blocker. It is runtime text, not part of the editable AGENT.md (REQ-TFB-008).
const blockingRules = "# When something blocks you\n" +
	"- If a tool or MCP server you need is unavailable, or a call to it fails, say so in your answer: name it, quote the error, say what you could not do, and say what the owner can do (for example: check the server in Settings → MCP, add a secret, or retry).\n" +
	"- Do not work around a missing or failing MCP server by calling it or its API by hand (for example with bash).\n" +
	"- If you cannot finish the request, say so plainly and say what is still needed. Never end a turn without telling the owner the result."

// Build renders the system prompt. Empty sections are skipped.
func Build(in PromptInput) string {
	sections := []string{}
	if persona := strings.TrimSpace(in.Persona); persona != "" {
		sections = append(sections, persona)
	}
	if len(in.Roster) > 0 {
		sections = append(sections, "# Your team\n"+roster(in.Roster))
	}
	if len(in.User) > 0 {
		sections = append(sections, "# About the user\n"+section(in.User, UserLimit))
	}
	if len(in.Memory) > 0 {
		sections = append(sections, "# Your memory\n"+section(in.Memory, MemoryLimit))
	}
	if len(in.Skills) > 0 {
		lines := []string{"# Skills", "Call skill_load with the skill name before following a skill."}
		for _, skill := range in.Skills {
			lines = append(lines, "- "+skill.Name+": "+skill.Description)
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}
	sections = append(sections, blockingRules)
	sections = append(sections, "# Runtime context\n"+runtimeContext(in))
	return strings.Join(sections, "\n\n")
}

func roster(entries []RosterEntry) string {
	lines := make([]string, 0, len(entries)+1)
	for _, entry := range entries {
		line := "- " + entry.Name
		if entry.Description != "" {
			line += ": " + entry.Description
		}
		lines = append(lines, line)
	}
	lines = append(lines, "Use the task tool to delegate work to a teammate, and task_continue to follow up.")
	return strings.Join(lines, "\n")
}

func section(entries []string, limit int) string {
	text := Join(entries)
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + fmt.Sprintf("\n[truncated: %d/%d characters; consolidate with memory_write]", len(runes), limit)
}

func runtimeContext(in PromptInput) string {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	zone, _ := now.Zone()
	lines := []string{
		"- agent: " + in.Agent,
		"- date: " + now.Format("2006-01-02 Monday 15:04") + " " + zone + " (UTC" + now.Format("-07:00") + ")",
	}
	if in.Channel != "" {
		lines = append(lines, "- channel: "+in.Channel)
	}
	if in.Workspace != "" {
		lines = append(lines, "- workspace: "+in.Workspace)
	}
	if len(in.UnavailableMCP) > 0 {
		names := make([]string, 0, len(in.UnavailableMCP))
		for name := range in.UnavailableMCP {
			names = append(names, name)
		}
		sort.Strings(names)
		servers := make([]string, 0, len(names))
		for _, name := range names {
			reason := in.UnavailableMCP[name]
			if runes := []rune(reason); len(runes) > 80 {
				reason = string(runes[:80])
			}
			servers = append(servers, name+" ("+reason+")")
		}
		lines = append(lines, "- unavailable MCP servers: "+strings.Join(servers, ", "))
	}
	return strings.Join(lines, "\n")
}
