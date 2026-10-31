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
	MemoryCapture  bool
	Skills         []Skill
	Roster         []RosterEntry
	Agent          string
	Channel        string
	Workspace      string
	UnavailableMCP map[string]string
	// Jobs is the background-jobs context line, empty when the session has no
	// jobs (REQ-JOB-017).
	Jobs string
	Now  time.Time
}

// memoryCaptureRules is the fixed section that tells an agent with memory_write
// to save durable facts from every owner message. It is constant, so it does
// not change the prompt prefix between turns (REQ-MEM-001, REQ-LTC-001).
const memoryCaptureRules = "# Memory capture\n" +
	"After every message from the owner, check it for a new durable fact: how they want to be addressed, their name or role, language or style preferences, standing instructions (\"always\", \"never\", \"from now on\"), decisions, or corrections of something you got wrong. This includes facts that are only implied or said in passing.\n" +
	"- Save it at once with memory_write before you answer: target=user for facts about the owner, target=memory for your own notes about the environment.\n" +
	"- If it contradicts an entry already shown above, use action=replace with old_text from that entry. Do not add a second entry for the same thing.\n" +
	"- Keep each entry one short sentence. Never save passwords, API keys, or tokens."

// blockingRules is the fixed section that tells the agent how to report a
// blocker. It is runtime text, not part of the editable AGENT.md (REQ-TFB-008).
const blockingRules = "# When something blocks you\n" +
	"- If a tool or MCP server you need is unavailable, or a call to it fails, say so in your answer: name it, quote the error, say what you could not do, and say what the owner can do (for example: check the server in Settings → MCP, add a secret, or retry).\n" +
	"- Do not work around a missing or failing MCP server by calling it or its API by hand (for example with bash).\n" +
	"- If you cannot finish the request, say so plainly and say what is still needed. Never end a turn without telling the owner the result."

// Section is one named part of a system prompt. Name is the stable identity
// used to diff two prompts; Text is the rendered section, heading included.
type Section struct {
	Name string
	Text string
}

// The section names.
const (
	SectionPersona       = "persona"
	SectionTeam          = "team"
	SectionUser          = "user"
	SectionMemory        = "memory"
	SectionMemoryCapture = "memory_capture"
	SectionSkills        = "skills"
	SectionBlockers      = "blockers"
	SectionRuntime       = "runtime"
)

// BuildSections renders the named system prompt sections in order. Empty
// sections are skipped (REQ-LTC-002).
func BuildSections(in PromptInput) []Section {
	sections := []Section{}
	if persona := strings.TrimSpace(in.Persona); persona != "" {
		sections = append(sections, Section{Name: SectionPersona, Text: persona})
	}
	if len(in.Roster) > 0 {
		sections = append(sections, Section{Name: SectionTeam, Text: "# Your team\n" + roster(in.Roster)})
	}
	if len(in.User) > 0 {
		sections = append(sections, Section{Name: SectionUser, Text: "# About the user\n" + section(in.User, UserLimit)})
	}
	if len(in.Memory) > 0 {
		sections = append(sections, Section{Name: SectionMemory, Text: "# Your memory\n" + section(in.Memory, MemoryLimit)})
	}
	if in.MemoryCapture {
		sections = append(sections, Section{Name: SectionMemoryCapture, Text: memoryCaptureRules})
	}
	if len(in.Skills) > 0 {
		lines := []string{"# Skills", "Call skill_load with the skill name before following a skill."}
		for _, skill := range in.Skills {
			lines = append(lines, "- "+skill.Name+": "+skill.Description)
		}
		sections = append(sections, Section{Name: SectionSkills, Text: strings.Join(lines, "\n")})
	}
	sections = append(sections, Section{Name: SectionBlockers, Text: blockingRules})
	sections = append(sections, Section{Name: SectionRuntime, Text: "# Runtime context\n" + runtimeContext(in)})
	return sections
}

// Build renders the system prompt. Empty sections are skipped.
func Build(in PromptInput) string {
	sections := BuildSections(in)
	texts := make([]string, 0, len(sections))
	for _, section := range sections {
		texts = append(texts, section.Text)
	}
	return strings.Join(texts, "\n\n")
}

// ContextBlock renders the volatile per-turn context: the date, the
// unavailable MCP servers, and the background jobs (REQ-LTC-001, REQ-JOB-017).
// It is added to the first user message of a turn so the head prompt stays
// byte-stable.
func ContextBlock(in PromptInput) string {
	lines := []string{"<context>", "date: " + stamp(in)}
	if line := unavailableMCPLine(in.UnavailableMCP); line != "" {
		lines = append(lines, line)
	}
	if in.Jobs != "" {
		lines = append(lines, in.Jobs)
	}
	lines = append(lines, "</context>")
	return strings.Join(lines, "\n")
}

// stamp formats now like the old runtime context date line.
func stamp(in PromptInput) string {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	zone, _ := now.Zone()
	return now.Format("2006-01-02 Monday 15:04") + " " + zone + " (UTC" + now.Format("-07:00") + ")"
}

// unavailableMCPLine renders the sorted unavailable MCP servers, or "" when
// there are none.
func unavailableMCPLine(unavailable map[string]string) string {
	if len(unavailable) == 0 {
		return ""
	}
	names := make([]string, 0, len(unavailable))
	for name := range unavailable {
		names = append(names, name)
	}
	sort.Strings(names)
	servers := make([]string, 0, len(names))
	for _, name := range names {
		reason := unavailable[name]
		if runes := []rune(reason); len(runes) > 80 {
			reason = string(runes[:80])
		}
		servers = append(servers, name+" ("+reason+")")
	}
	return "unavailable MCP servers: " + strings.Join(servers, ", ")
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

// runtimeContext is the stable part of the runtime section: the agent, the
// channel, and the workspace. The date and the unavailable MCP servers moved to
// ContextBlock so the head prompt stays byte-stable (REQ-LTC-001).
func runtimeContext(in PromptInput) string {
	lines := []string{"- agent: " + in.Agent}
	if in.Channel != "" {
		lines = append(lines, "- channel: "+in.Channel)
	}
	if in.Workspace != "" {
		lines = append(lines, "- workspace: "+in.Workspace)
	}
	return strings.Join(lines, "\n")
}
