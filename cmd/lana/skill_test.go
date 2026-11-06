package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

// writeTestSkill writes a SKILL.md under dir/name with the given content.
func writeTestSkill(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name, "SKILL.md")
	if err := agenthome.WriteFile(path, content); err != nil {
		t.Fatal(err)
	}
}

func TestSkillNew(t *testing.T) {
	home := initHome(t)
	path := filepath.Join(home, "skills", "git-workflow", "SKILL.md")

	out, code := captureStdout(t, func() int { return cmdSkill([]string{"new", "git-workflow"}) })
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	wantOut := fmt.Sprintf("created %s\nadd \"git-workflow\" to skills = [...] of an agent to use it\n", path)
	if out != wantOut {
		t.Fatalf("stdout = %q, want %q", out, wantOut)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "---\nname: git-workflow\ndescription: TODO: describe when to use this skill\n---\n# git-workflow\n\nSteps:\n1. \n"
	if string(data) != want {
		t.Fatalf("SKILL.md = %q, want %q", data, want)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("folder mode = %v, %v", info, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, %v", info, err)
	}

	stderr, code := captureStderr(t, func() int { return cmdSkill([]string{"new", "git-workflow"}) })
	if code != 2 {
		t.Fatalf("exists exit = %d, want 2", code)
	}
	if stderr != "error: skill \"git-workflow\" already exists\n" {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestSkillNewInvalidName(t *testing.T) {
	initHome(t)
	stderr, code := captureStderr(t, func() int { return cmdSkill([]string{"new", "Bad Name"}) })
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	want := "error: invalid skill name \"Bad Name\": use a-z, 0-9, - and _, start with a letter, max 64 characters\n"
	if stderr != want {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
}

func TestSkillList(t *testing.T) {
	home := initHome(t)
	writeTestSkill(t, agenthome.GlobalSkillsDir(home), "git-workflow", "---\nname: git-workflow\ndescription: Do git.\n---\nbody\n")
	writeTestSkill(t, agenthome.GlobalSkillsDir(home), "broken-global", "---\nname: broken-global\n---\nbody\n")
	if _, code := captureStdout(t, func() int { return agentNew([]string{"helper", "--yes"}, false) }); code != 0 {
		t.Fatalf("agent new exit = %d", code)
	}
	writeTestSkill(t, agenthome.AgentSkillsDir(home, "helper"), "disk-cleanup", "---\nname: disk-cleanup\ndescription: Free disk.\n---\nbody\n")
	writeTestSkill(t, agenthome.AgentSkillsDir(home, "helper"), "broken-agent", "---\nname: broken-agent\ndescription: \n---\nbody\n")

	out, code := captureStdout(t, skillList)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	want := "git-workflow  global  Do git.\n" +
		"broken-global  global  invalid: missing description\n" +
		"disk-cleanup  agent:helper  Free disk.\n" +
		"broken-agent  agent:helper  invalid: missing description\n"
	if out != want {
		t.Fatalf("stdout = %q, want %q", out, want)
	}
}

func TestSkillListEmpty(t *testing.T) {
	initHome(t)
	out, code := captureStdout(t, func() int { return cmdSkill([]string{"list"}) })
	if code != 0 || out != "no skills\n" {
		t.Fatalf("exit = %d, stdout = %q", code, out)
	}
}

func TestSkillShow(t *testing.T) {
	home := initHome(t)
	globalText := "---\nname: git-workflow\ndescription: Do git.\n---\nglobal body\n"
	writeTestSkill(t, agenthome.GlobalSkillsDir(home), "git-workflow", globalText)

	out, code := captureStdout(t, func() int { return cmdSkill([]string{"show", "git-workflow"}) })
	if code != 0 || out != globalText {
		t.Fatalf("global exit = %d, stdout = %q", code, out)
	}

	// show reads the global file as-is, even when its frontmatter is invalid.
	writeTestSkill(t, agenthome.GlobalSkillsDir(home), "notes", "plain text\n")
	out, code = captureStdout(t, func() int { return cmdSkill([]string{"show", "notes"}) })
	if code != 0 || out != "plain text\n" {
		t.Fatalf("raw global exit = %d, stdout = %q", code, out)
	}

	if _, code := captureStdout(t, func() int { return agentNew([]string{"helper", "--yes"}, false) }); code != 0 {
		t.Fatalf("agent new exit = %d", code)
	}
	agentText := "---\nname: disk-cleanup\ndescription: Free disk.\n---\nagent body\n"
	writeTestSkill(t, agenthome.AgentSkillsDir(home, "helper"), "disk-cleanup", agentText)

	for _, args := range [][]string{
		{"show", "disk-cleanup", "--agent", "helper"},
		{"show", "--agent", "helper", "disk-cleanup"},
	} {
		out, code := captureStdout(t, func() int { return cmdSkill(args) })
		if code != 0 || out != agentText {
			t.Fatalf("%v exit = %d, stdout = %q", args, code, out)
		}
	}

	stderr, code := captureStderr(t, func() int { return cmdSkill([]string{"show", "disk-cleanup"}) })
	if code != 2 || stderr != "error: unknown skill \"disk-cleanup\"\n" {
		t.Fatalf("global unknown exit = %d, stderr = %q", code, stderr)
	}
	stderr, code = captureStderr(t, func() int { return cmdSkill([]string{"show", "disk-cleanup", "--agent", "ghost"}) })
	if code != 2 || stderr != "error: unknown agent \"ghost\"\n" {
		t.Fatalf("unknown agent exit = %d, stderr = %q", code, stderr)
	}
	stderr, code = captureStderr(t, func() int { return cmdSkill([]string{"show"}) })
	if code != 2 || !strings.Contains(stderr, "use lana skill show <name>") {
		t.Fatalf("usage exit = %d, stderr = %q", code, stderr)
	}
}

func TestConfigCheckSkillWarnings(t *testing.T) {
	home := initHome(t)
	writeTestSkill(t, agenthome.GlobalSkillsDir(home), "broken-global", "---\nname: broken-global\n---\nbody\n")
	writeTestSkill(t, agenthome.AgentSkillsDir(home, "default"), "broken-agent", "no frontmatter\n")

	out, code := captureStdout(t, func() int { return cmdConfig([]string{"check"}) })
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{
		fmt.Sprintf("· warning: skill %s: missing description", filepath.Join(agenthome.GlobalSkillsDir(home), "broken-global")),
		fmt.Sprintf("· warning: skill %s: missing frontmatter", filepath.Join(agenthome.AgentSkillsDir(home, "default"), "broken-agent")),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout does not contain %q:\n%s", want, out)
		}
	}
}
