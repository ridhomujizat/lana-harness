package agenthome

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkillFile(t *testing.T, dir, text string) {
	t.Helper()
	if err := WriteFile(filepath.Join(dir, "SKILL.md"), text); err != nil {
		t.Fatal(err)
	}
}

func writeNamedSkill(t *testing.T, root, name, description string) {
	t.Helper()
	writeSkillFile(t, filepath.Join(root, name), "---\nname: "+name+"\ndescription: "+description+"\n---\nBody of "+name)
}

func TestParseSkill(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "git-workflow")
	writeSkillFile(t, dir, "---\nname: \"git-workflow\"\ndescription: 'Create a branch.'\nversion: 1\n---\n\n  1. Branch\n\n2. Commit\n\n")

	skill, err := ParseSkill(dir)
	if err != nil {
		t.Fatal(err)
	}
	if skill.Name != "git-workflow" || skill.Description != "Create a branch." {
		t.Fatalf("skill = %#v", skill)
	}
	if want := "  1. Branch\n\n2. Commit"; skill.Body != want {
		t.Fatalf("body = %q, want %q", skill.Body, want)
	}
	if skill.Dir != dir {
		t.Fatalf("dir = %q, want %q", skill.Dir, dir)
	}
	if skill.Scope != "" {
		t.Fatalf("scope = %q, want empty", skill.Scope)
	}
}

func TestParseSkillErrors(t *testing.T) {
	tests := []struct {
		name string
		dir  string
		text string
		want string
	}{
		{"missing frontmatter", "plain", "just a body\n", "missing frontmatter"},
		{"missing closing", "plain", "---\nname: plain\ndescription: d\n", "missing frontmatter"},
		{"missing name", "plain", "---\ndescription: d\n---\n", "missing name"},
		{"missing description", "plain", "---\nname: plain\n---\n", "missing description"},
		{"name mismatch", "folder", "---\nname: other\ndescription: d\n---\n", `name "other" does not match folder "folder"`},
		{"invalid name", "Bad_Name", "---\nname: Bad_Name\ndescription: d\n---\n", `invalid name "Bad_Name"`},
		{"description too long", "plain", "---\nname: plain\ndescription: " + strings.Repeat("x", 301) + "\n---\n", "description is longer than 300 characters"},
		{"body too long", "plain", "---\nname: plain\ndescription: d\n---\n" + strings.Repeat("x", 20001), "body is longer than 20000 characters"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), tc.dir)
			writeSkillFile(t, dir, tc.text)
			_, err := ParseSkill(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestReadSkills(t *testing.T) {
	root := t.TempDir()
	writeNamedSkill(t, root, "good", "A good skill.")
	writeSkillFile(t, filepath.Join(root, "bad"), "---\nname: bad\n---\nBody")
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".hidden"), 0o700); err != nil {
		t.Fatal(err)
	}

	skills, invalid := ReadSkills(root, "global")
	if len(skills) != 1 || skills[0].Name != "good" || skills[0].Scope != "global" {
		t.Fatalf("skills = %#v", skills)
	}
	if len(invalid) != 1 || invalid[0].Path != filepath.Join(root, "bad") || invalid[0].Err == nil {
		t.Fatalf("invalid = %#v", invalid)
	}

	skills, invalid = ReadSkills(filepath.Join(root, "missing"), "global")
	if len(skills) != 0 || len(invalid) != 0 {
		t.Fatalf("missing dir = %#v, %#v", skills, invalid)
	}
}

func TestVisibleSkills(t *testing.T) {
	home := t.TempDir()
	global := GlobalSkillsDir(home)
	writeNamedSkill(t, global, "a", "Global a.")
	writeNamedSkill(t, global, "b", "Global b.")
	writeNamedSkill(t, AgentSkillsDir(home, "dev"), "c", "Agent c.")

	skills, invalid := VisibleSkills(home, "dev", []string{"a"})
	if len(invalid) != 0 {
		t.Fatalf("invalid = %#v", invalid)
	}
	if len(skills) != 2 || skills[0].Name != "a" || skills[0].Scope != "global" || skills[1].Name != "c" || skills[1].Scope != "agent" {
		t.Fatalf("skills = %#v", skills)
	}

	writeNamedSkill(t, AgentSkillsDir(home, "dev"), "a", "Agent a.")
	skills, _ = VisibleSkills(home, "dev", []string{"a"})
	if len(skills) != 2 || skills[0].Name != "a" || skills[0].Scope != "agent" || skills[0].Description != "Agent a." || skills[1].Name != "c" {
		t.Fatalf("shadowed skills = %#v", skills)
	}
}

// TestVisibleSkillsSkipsMissingListed covers Q-026: a listed name with no
// folder is skipped without an error and is not visible.
func TestVisibleSkillsSkipsMissingListed(t *testing.T) {
	home := t.TempDir()
	writeNamedSkill(t, GlobalSkillsDir(home), "kept", "Kept skill.")
	writeNamedSkill(t, AgentSkillsDir(home, "dev"), "own", "Agent skill.")

	skills, invalid := VisibleSkills(home, "dev", []string{"kept", "deleted"})
	if len(invalid) != 0 {
		t.Fatalf("invalid = %#v", invalid)
	}
	if len(skills) != 2 || skills[0].Name != "kept" || skills[1].Name != "own" {
		t.Fatalf("skills = %#v, want kept and own only", skills)
	}
}

func TestWriteSkill(t *testing.T) {
	home := t.TempDir()
	created, err := WriteSkill(home, "dev", "git-workflow", "  Create a branch.  ", "\n1. Branch\n")
	if err != nil || !created {
		t.Fatalf("create = %v, %v", created, err)
	}
	path := filepath.Join(AgentSkillsDir(home, "dev"), "git-workflow", "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "---\nname: git-workflow\ndescription: Create a branch.\n---\n1. Branch\n"
	if string(data) != want {
		t.Fatalf("file = %q, want %q", data, want)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v", info.Mode(), err)
	}

	created, err = WriteSkill(home, "dev", "git-workflow", "Replacement.", "New body.")
	if err != nil || created {
		t.Fatalf("replace = %v, %v", created, err)
	}

	tests := []struct {
		name        string
		description string
		body        string
		want        string
	}{
		{"Bad", "d", "b", `invalid name "Bad"`},
		{"ok", "one\nline", "b", "description must be one line"},
		{"ok", "d", "   ", "body is required"},
		{"ok", "d", strings.Repeat("x", 20001), "body is longer than 20000 characters"},
	}
	for _, tc := range tests {
		created, err := WriteSkill(home, "dev", tc.name, tc.description, tc.body)
		if created || err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%q: created = %v, err = %v, want %q", tc.name, created, err, tc.want)
		}
	}
}

func TestBuildSkills(t *testing.T) {
	got := Build(PromptInput{
		Persona: "You are helper.",
		Memory:  []string{"Uses fish"},
		Skills: []Skill{
			{Name: "git-workflow", Description: "Create a branch.", Body: "1. secret body"},
			{Name: "disk-cleanup", Description: "Free disk space."},
		},
		Agent: "helper",
	})
	memory := strings.Index(got, "# Your memory")
	skills := strings.Index(got, "# Skills")
	runtime := strings.Index(got, "# Runtime context")
	if memory < 0 || skills < 0 || runtime < 0 || memory > skills || skills > runtime {
		t.Fatalf("section order wrong:\n%s", got)
	}
	if !strings.Contains(got, "- git-workflow: Create a branch.") {
		t.Fatalf("skill line missing:\n%s", got)
	}
	if strings.Contains(got, "secret body") {
		t.Fatalf("skill body must not be in prompt:\n%s", got)
	}
	if lines := strings.Split(got, "\n"); lines[len(lines)-1] != "- agent: helper" {
		t.Fatalf("last line = %q", lines[len(lines)-1])
	}

	if strings.Contains(Build(PromptInput{Agent: "a"}), "# Skills") {
		t.Fatal("no skills must add no section")
	}
}

func TestContextBlockTruncatesReason(t *testing.T) {
	reason := strings.Repeat("é", 100)
	got := ContextBlock(PromptInput{Agent: "a", UnavailableMCP: map[string]string{"x": reason}})
	if want := "unavailable MCP servers: x (" + strings.Repeat("é", 80) + ")"; !strings.Contains(got, want) {
		t.Fatalf("context = %q, want %q", got, want)
	}
}

// REQ-DKN-050: a proposal stores a SKILL.md like WriteSkill writes plus a
// proposal.json; a second proposal for the same name replaces the pending one;
// the list is newest first and reports whether the agent already has the skill.
func TestProposeSkillAndList(t *testing.T) {
	home := t.TempDir()
	if _, err := WriteSkill(home, "dev", "existing", "Existing skill.", "Body."); err != nil {
		t.Fatal(err)
	}
	if err := ProposeSkill(home, "dev", "payroll-check", "Check payroll.", "1. Do it.", SkillProposalMeta{SessionID: "s1", OwnerTurn: true, CreatedAt: 100}); err != nil {
		t.Fatal(err)
	}
	if err := ProposeSkill(home, "dev", "existing", "Update it.", "New body.", SkillProposalMeta{SessionID: "s2", CreatedAt: 300}); err != nil {
		t.Fatal(err)
	}

	proposals, err := SkillProposals(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 2 {
		t.Fatalf("proposals = %+v", proposals)
	}
	if proposals[0].Name != "existing" || proposals[0].CreatedAt != 300 || proposals[0].Agent != "dev" || !proposals[0].Exists {
		t.Fatalf("first = %+v", proposals[0])
	}
	if proposals[1].Name != "payroll-check" || proposals[1].SessionID != "s1" || !proposals[1].OwnerTurn || proposals[1].Exists {
		t.Fatalf("second = %+v", proposals[1])
	}

	// A second proposal with the same name replaces the pending one.
	if err := ProposeSkill(home, "dev", "payroll-check", "Check payroll again.", "1. Better.", SkillProposalMeta{SessionID: "s3", CreatedAt: 400}); err != nil {
		t.Fatal(err)
	}
	proposals, err = SkillProposals(home)
	if err != nil || len(proposals) != 2 {
		t.Fatalf("after replace = %+v, %v", proposals, err)
	}
	if proposals[0].Name != "payroll-check" || proposals[0].Description != "Check payroll again." {
		t.Fatalf("replaced first = %+v", proposals[0])
	}
}

// REQ-DKN-050: ProposeSkill validates like WriteSkill and refuses a bad agent.
func TestProposeSkillValidation(t *testing.T) {
	home := t.TempDir()
	tests := []struct {
		agent string
		name  string
		desc  string
		body  string
		want  string
	}{
		{"dev", "Bad", "d", "b", `invalid name "Bad"`},
		{"dev", "ok", "one\nline", "b", "description must be one line"},
		{"dev", "ok", "d", "   ", "body is required"},
		{"dev", "ok", "d", strings.Repeat("x", 20001), "body is longer than 20000 characters"},
		{"Bad_Name", "ok", "d", "b", `invalid agent name`},
	}
	for _, tc := range tests {
		err := ProposeSkill(home, tc.agent, tc.name, tc.desc, tc.body, SkillProposalMeta{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s/%s: err = %v, want %q", tc.agent, tc.name, err, tc.want)
		}
	}
}

// REQ-DKN-051: approve writes the skill and removes the proposal; reject drops
// the proposal; a missing proposal is an os.ErrNotExist error.
func TestApproveAndRejectSkill(t *testing.T) {
	home := t.TempDir()
	if err := ProposeSkill(home, "dev", "payroll-check", "Check payroll.", "1. Do it.", SkillProposalMeta{SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if err := ApproveSkill(home, "dev", "payroll-check"); err != nil {
		t.Fatal(err)
	}
	skill, err := ParseSkill(filepath.Join(AgentSkillsDir(home, "dev"), "payroll-check"))
	if err != nil || skill.Description != "Check payroll." || skill.Body != "1. Do it." {
		t.Fatalf("approved skill = %+v, %v", skill, err)
	}
	if _, err := os.Stat(proposeDir(home, "dev", "payroll-check")); !os.IsNotExist(err) {
		t.Fatalf("proposal folder still exists: %v", err)
	}
	if err := ApproveSkill(home, "dev", "payroll-check"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second approve err = %v, want not exist", err)
	}

	if err := ProposeSkill(home, "dev", "disk-cleanup", "Free space.", "Body.", SkillProposalMeta{}); err != nil {
		t.Fatal(err)
	}
	if err := RejectSkill(home, "dev", "disk-cleanup"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(AgentSkillsDir(home, "dev"), "disk-cleanup")); !os.IsNotExist(err) {
		t.Fatalf("rejected skill was written: %v", err)
	}
	if _, err := os.Stat(proposeDir(home, "dev", "disk-cleanup")); !os.IsNotExist(err) {
		t.Fatalf("proposal folder still exists: %v", err)
	}
	if err := RejectSkill(home, "dev", "disk-cleanup"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second reject err = %v, want not exist", err)
	}
}

// REQ-DKN-051: skill_load and the prompt never see a proposed skill.
func TestVisibleSkillsSkipsProposals(t *testing.T) {
	home := t.TempDir()
	writeNamedSkill(t, AgentSkillsDir(home, "dev"), "kept", "Kept skill.")
	if err := ProposeSkill(home, "dev", "payroll-check", "Check payroll.", "Body.", SkillProposalMeta{}); err != nil {
		t.Fatal(err)
	}

	skills, invalid := VisibleSkills(home, "dev", []string{"kept", "payroll-check"})
	if len(invalid) != 0 {
		t.Fatalf("invalid = %#v", invalid)
	}
	if len(skills) != 1 || skills[0].Name != "kept" {
		t.Fatalf("skills = %#v, want kept only", skills)
	}
}
