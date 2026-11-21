package agenthome

import (
	"strings"
	"testing"
)

// projectText is a rendered project section (REQ-PCX-002).
const projectText = "# Project instructions\n" +
	"These are project instructions written for coding agents. Follow the ones that apply to your tools.\n\n" +
	"<project-context path=\"/home/me/lana/AGENTS.md\">\nrepo instructions\n</project-context>"

// REQ-PCX-002: the project section sits after the skills section and before
// the tool notes, and its text is used as-is.
func TestBuildSectionsProjectPlacement(t *testing.T) {
	sections := BuildSections(PromptInput{
		Persona:   "persona",
		Skills:    []Skill{{Name: "s", Description: "d"}},
		Project:   projectText,
		ToolNotes: []ToolNote{{Name: "bash", Text: "note"}},
	})
	names := make([]string, 0, len(sections))
	for _, section := range sections {
		names = append(names, section.Name)
	}
	want := []string{SectionPersona, SectionSkills, SectionProject, SectionToolNotes, SectionBlockers, SectionRuntime}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("section names = %v, want %v", names, want)
	}
	for _, section := range sections {
		if section.Name == SectionProject && section.Text != projectText {
			t.Fatalf("project text = %q, want %q", section.Text, projectText)
		}
	}
}

// REQ-PCX-002: an empty Project adds no section.
func TestBuildSectionsNoProjectWhenEmpty(t *testing.T) {
	for _, section := range BuildSections(PromptInput{Persona: "persona"}) {
		if section.Name == SectionProject {
			t.Fatal("empty Project must add no section")
		}
	}
}
