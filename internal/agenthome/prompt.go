package agenthome

import (
	"fmt"
	"strings"
	"time"
)

// Onboarding is the instruction added while the user file has no entries.
const Onboarding = `You do not know the user yet. In this conversation, before other work unless the user asks for something urgent:
1. Greet the user briefly and say you would like to learn a few things so you can help better.
2. Ask, one or two at a time: their name and how to address them, the language they prefer, their timezone, what they do (work or role), and how they like answers (length, tone, format).
3. Save each answer as a short entry with memory_write (target "user", action "add") as soon as you get it.
4. When done, summarize what you saved and ask what they want to do first.`

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
	if len(in.User) == 0 {
		sections = append(sections, "# Onboarding\n"+Onboarding)
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
