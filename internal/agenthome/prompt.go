package agenthome

import (
	"fmt"
	"strings"
	"time"
)

// PromptInput is the data for one system prompt.
type PromptInput struct {
	Persona   string
	User      []string
	Memory    []string
	Agent     string
	Channel   string
	Workspace string
	Now       time.Time
}

// Build renders the system prompt. Empty sections are skipped.
func Build(in PromptInput) string {
	sections := []string{}
	if persona := strings.TrimSpace(in.Persona); persona != "" {
		sections = append(sections, persona)
	}
	if len(in.User) > 0 {
		sections = append(sections, "# About the user\n"+section(in.User, UserLimit))
	}
	if len(in.Memory) > 0 {
		sections = append(sections, "# Your memory\n"+section(in.Memory, MemoryLimit))
	}
	sections = append(sections, "# Runtime context\n"+runtimeContext(in))
	return strings.Join(sections, "\n\n")
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
	return strings.Join(lines, "\n")
}
