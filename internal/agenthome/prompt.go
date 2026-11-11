package agenthome

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// DiscussionPrompt is what a team lead's prompt says about room discussions
// (spec 47 REQ-RDS-017, REQ-RDS-041).
type DiscussionPrompt struct {
	Team    string
	Purpose string
	// Mode is "auto" (the lead decides) or "ask" (only when the owner asks).
	Mode   string
	Rounds int
	// Extra is "once", "ask" or "never".
	Extra string
	// Memory is the room memory lines, or "".
	Memory string
}

// RosterEntry is one teammate in the team roster section of a prompt.
type RosterEntry struct {
	Name        string
	Description string
}

// ToolNote is the prompt guidance of one offered tool (REQ-ECX-010).
type ToolNote struct {
	Name string
	Text string
}

// PromptInput is the data for one system prompt.
type PromptInput struct {
	Persona   string
	User      []string
	Memory    []string
	ToolNotes []ToolNote
	// HasMCP reports whether the agent has at least one MCP server configured
	// (REQ-ECX-012).
	HasMCP bool
	Skills []Skill
	Roster []RosterEntry
	// Discussion is set for a team lead's turn (spec 47 REQ-RDS-017).
	Discussion     *DiscussionPrompt
	Agent          string
	Channel        string
	Workspace      string
	UnavailableMCP map[string]string
	// RelevantMemory holds the formatted "m<n>: <entry>" lines of the entries
	// picked for this turn's first user message (REQ-ECX-024).
	RelevantMemory []string
	// RelevantKnowledge holds the formatted "<title> (<path>): <text>" lines
	// of the knowledge pages picked for this turn's first user message
	// (REQ-KNW-050).
	RelevantKnowledge []string
	// Jobs is the background-jobs context line, empty when the session has no
	// jobs (REQ-JOB-017).
	Jobs string
	Now  time.Time
}

// blockerGeneralRules is always in the head prompt: how to report a request
// that cannot be finished, and when tool status belongs in an answer
// (REQ-ECX-001, REQ-ECX-012).
const blockerGeneralRules = "# When something blocks you\n" +
	"- When you cannot finish the request, say so plainly and say what is still needed, so the owner can decide the next step.\n" +
	"- End each turn by telling the owner the result, including partial results, because the owner may not see the tool calls.\n" +
	"- Talk about tools, MCP servers, or their status only when the current request needs them or the owner asks; unrelated status reports are noise."

// blockerMCPRules is added to the blocker section only when the agent has an
// MCP server (REQ-ECX-012).
const blockerMCPRules = "- When an MCP server you need is unavailable or a call to it fails, say so in your answer: name it, quote the error, say what you could not do, and say what the owner can do (for example: check the server in Settings → MCP, add a secret, or retry).\n" +
	"- Do not work around a missing or failing MCP server by calling it or its API by hand (for example with bash), because that bypasses the server's own setup and status."

// Section is one named part of a system prompt. Name is the stable identity
// used to diff two prompts; Text is the rendered section, heading included.
type Section struct {
	Name string
	Text string
}

// The section names.
const (
	SectionPersona   = "persona"
	SectionTeam      = "team"
	SectionUser      = "user"
	SectionMemory    = "memory"
	SectionSkills    = "skills"
	SectionToolNotes = "tool_notes"
	SectionBlockers  = "blockers"
	SectionRuntime   = "runtime"
)

// BuildSections renders the named system prompt sections in order. Empty
// sections are skipped (REQ-LTC-002).
func BuildSections(in PromptInput) []Section {
	sections := []Section{}
	if persona := strings.TrimSpace(in.Persona); persona != "" {
		sections = append(sections, Section{Name: SectionPersona, Text: persona})
	}
	if len(in.Roster) > 0 {
		text := "# Your team\n" + roster(in.Roster)
		if in.Discussion != nil {
			text += "\n\n" + discussionGuide(*in.Discussion)
		}
		sections = append(sections, Section{Name: SectionTeam, Text: text})
	}
	if len(in.User) > 0 {
		sections = append(sections, Section{Name: SectionUser, Text: "# About the user\n" + section(in.User, UserLimit)})
	}
	if len(in.Memory) > 0 {
		text, _ := MemoryView(in.Memory)
		sections = append(sections, Section{Name: SectionMemory, Text: "# Your memory\n" + text})
	}
	if len(in.Skills) > 0 {
		lines := []string{"# Skills"}
		for _, skill := range in.Skills {
			lines = append(lines, "- "+skill.Name+": "+skill.Description)
		}
		sections = append(sections, Section{Name: SectionSkills, Text: strings.Join(lines, "\n")})
	}
	if notes := toolNotes(in.ToolNotes); notes != "" {
		sections = append(sections, Section{Name: SectionToolNotes, Text: notes})
	}
	sections = append(sections, Section{Name: SectionBlockers, Text: blockerRules(in.HasMCP)})
	sections = append(sections, Section{Name: SectionRuntime, Text: "# Runtime context\n" + runtimeContext(in)})
	return sections
}

// blockerRules renders the general blocker rules and, when the agent has an
// MCP server, the MCP-specific rules (REQ-ECX-012).
func blockerRules(hasMCP bool) string {
	if !hasMCP {
		return blockerGeneralRules
	}
	return blockerGeneralRules + "\n" + blockerMCPRules
}

// toolNotes renders one "## <tool>" block per guidance note, in tool-name
// order. Empty notes are skipped, and "" means no section is rendered
// (REQ-ECX-010).
func toolNotes(notes []ToolNote) string {
	list := make([]ToolNote, 0, len(notes))
	for _, note := range notes {
		name := strings.TrimSpace(note.Name)
		text := strings.TrimSpace(note.Text)
		if name == "" || text == "" {
			continue
		}
		list = append(list, ToolNote{Name: name, Text: text})
	}
	if len(list) == 0 {
		return ""
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	blocks := make([]string, 0, len(list))
	for _, note := range list {
		blocks = append(blocks, "## "+note.Name+"\n"+note.Text)
	}
	return "# Tool notes\n" + strings.Join(blocks, "\n\n")
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

// ContextBlock renders the volatile per-turn context: the date, the relevant
// memory and knowledge, the unavailable MCP servers, and the background jobs
// (REQ-LTC-001, REQ-ECX-024, REQ-KNW-050, REQ-JOB-017). It is added to the
// first user message of a turn so the head prompt stays byte-stable.
func ContextBlock(in PromptInput) string {
	lines := []string{"<context>", "date: " + stamp(in)}
	if len(in.RelevantMemory) > 0 {
		lines = append(lines, "relevant memory:")
		for _, entry := range in.RelevantMemory {
			lines = append(lines, "- "+entry)
		}
	}
	if len(in.RelevantKnowledge) > 0 {
		lines = append(lines, "relevant knowledge:")
		for _, entry := range in.RelevantKnowledge {
			lines = append(lines, "- "+entry)
		}
	}
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
	lines = append(lines, "When a task fits a teammate's role, use the task tool to delegate it, and task_continue to follow up on it; this keeps each session focused on its own work.")
	return strings.Join(lines, "\n")
}

// discussionGuide tells a team lead how to run room discussions.
func discussionGuide(d DiscussionPrompt) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Room %q\n", d.Team)
	if d.Purpose != "" {
		fmt.Fprintf(&b, "Purpose: %s\n", d.Purpose)
	}
	b.WriteString("You lead this group room. The owner reads it like a group chat.\n\n## Discussions\n")
	if d.Mode == "ask" {
		b.WriteString("Open a discussion only when the owner asks for one (Discuss is on, or they say so, for example \"diskusikan\"). Otherwise answer yourself or delegate with task.\n")
	} else {
		b.WriteString("When a question needs more than one member's view (a decision, a trade-off, a plan), open a discussion instead of answering alone. Answer simple questions yourself or delegate with task. If the owner asks for a direct answer, do not open one.\n")
	}
	fmt.Fprintf(&b, `- discuss_start: the topic, the members with the angle each covers, and an opening message to the owner (who you bring in and why). Put what you say to the room in the tool fields; do not repeat it as plain text.
- discuss_ask: ask one member with a short message that starts with @Name. Pass one member's point to another when they disagree (reply_to, quote). For a large room, ask everyone once in one step (parallel), then only the members who disagree.
- Stop when members agree, or when two replies in a row bring nothing new. A difference of priorities (values, not facts) is not settled by more rounds: end and put both views to the owner.
- The round limit is %d: each member can be asked at most that many times. `, d.Rounds)
	switch d.Extra {
	case "never":
		b.WriteString("Extra rounds are off.\n")
	case "ask":
		b.WriteString("discuss_more asks the owner for one more round; use it only when the last round brought something new and one concrete question is still open.\n")
	default:
		b.WriteString("discuss_more adds one round on its own once, then asks the owner; use it only when the last round brought something new and one concrete question is still open. Never more than 5 rounds.\n")
	}
	b.WriteString("- discuss_end: the reason, a one-sentence conclusion, agreed / still different / open points with the member each came from, each member's part, and notes worth keeping in room memory (decisions, facts, open questions; mark a note that replaces an older one). Then answer the owner in 2–4 sentences.\n")
	if d.Memory != "" {
		fmt.Fprintf(&b, "\n## Room memory\n%s\n", d.Memory)
	}
	return strings.TrimRight(b.String(), "\n")
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
