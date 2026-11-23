package claudechat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func resetCommands(t *testing.T) {
	t.Helper()
	commandsMu.Lock()
	commandsCache = map[string]commandsEntry{}
	commandsMu.Unlock()
	old := commandsTTL
	commandsTTL = 0
	t.Cleanup(func() { commandsTTL = old })
}

func writeText(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func findCommand(cmds []Command, name string) (Command, bool) {
	for _, c := range cmds {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

func TestCommandsBuiltins(t *testing.T) {
	resetCommands(t)
	cmds := Commands(t.TempDir(), t.TempDir())
	if len(cmds) != len(builtinCommands) {
		t.Fatalf("got %d commands, want %d built-ins", len(cmds), len(builtinCommands))
	}
	for _, c := range cmds {
		if c.Source != "built-in" {
			t.Errorf("command %q source = %q, want built-in", c.Name, c.Source)
		}
		if !strings.HasPrefix(c.Name, "/") {
			t.Errorf("built-in name %q has no leading slash", c.Name)
		}
	}
	for _, want := range []string{"/clear", "/compact", "/model", "/version"} {
		if _, ok := findCommand(cmds, want); !ok {
			t.Errorf("built-in %q missing", want)
		}
	}
	if c, _ := findCommand(cmds, "/compact"); c.Description != "Free up context by summarizing the conversation so far" {
		t.Errorf("/compact description = %q", c.Description)
	}
}

func TestCommandsScan(t *testing.T) {
	resetCommands(t)
	home := t.TempDir()
	cwd := t.TempDir()

	writeText(t, filepath.Join(home, ".claude", "skills", "myskill", "SKILL.md"),
		"---\nname: myskill\ndescription: A user skill\n---\nbody\n")
	writeText(t, filepath.Join(home, ".claude", "commands", "top.md"), "Do the thing\n")
	writeText(t, filepath.Join(home, ".claude", "commands", "a", "b", "c", "d.md"), "Deep command\n")
	writeText(t, filepath.Join(home, ".claude", "commands", "a", "b", "c", "d", "e.md"), "Too deep\n")
	writeText(t, filepath.Join(cwd, ".claude", "skills", "Proj", "SKILL.md"), "Project skill line\n")
	writeText(t, filepath.Join(cwd, ".claude", "commands", "a", "b.md"),
		"---\ndescription: \"Nested desc\"\n---\nbody\n")
	writeText(t, filepath.Join(home, ".claude", "skills", "Bad Name", "SKILL.md"), "nope\n")

	cmds := Commands(cwd, home)

	if c, ok := findCommand(cmds, "myskill"); !ok || c.Description != "A user skill" || c.Source != "user skill" {
		t.Errorf("myskill = %+v, %v", c, ok)
	}
	if c, ok := findCommand(cmds, "top"); !ok || c.Description != "Do the thing" || c.Source != "user command" {
		t.Errorf("top = %+v, %v", c, ok)
	}
	if c, ok := findCommand(cmds, "a:b"); !ok || c.Description != "Nested desc" || c.Source != "project command" {
		t.Errorf("a:b = %+v, %v", c, ok)
	}
	if c, ok := findCommand(cmds, "a:b:c:d"); !ok || c.Source != "user command" {
		t.Errorf("a:b:c:d = %+v, %v", c, ok)
	}
	if _, ok := findCommand(cmds, "a:b:c:d:e"); ok {
		t.Errorf("a:b:c:d:e should be beyond the depth limit")
	}
	if c, ok := findCommand(cmds, "Proj"); !ok || c.Description != "Project skill line" || c.Source != "project skill" {
		t.Errorf("Proj = %+v, %v", c, ok)
	}
	if _, ok := findCommand(cmds, "Bad Name"); ok {
		t.Errorf("invalid skill name was not skipped")
	}
}

func TestCommandsDuplicateFirstWins(t *testing.T) {
	resetCommands(t)
	home := t.TempDir()
	cwd := t.TempDir()

	writeText(t, filepath.Join(home, ".claude", "skills", "dup", "SKILL.md"), "user dup\n")
	writeText(t, filepath.Join(cwd, ".claude", "skills", "dup", "SKILL.md"), "project dup\n")

	cmds := Commands(cwd, home)
	count := 0
	for _, c := range cmds {
		if c.Name == "dup" {
			count++
			if c.Source != "user skill" || c.Description != "user dup" {
				t.Errorf("dup = %+v, want the user skill", c)
			}
		}
	}
	if count != 1 {
		t.Fatalf("dup appears %d times, want 1", count)
	}
}

func TestCommandDescriptionFallbacks(t *testing.T) {
	resetCommands(t)
	home := t.TempDir()

	writeText(t, filepath.Join(home, ".claude", "commands", "nofront.md"), "First line here\nsecond\n")
	writeText(t, filepath.Join(home, ".claude", "commands", "emptyfront.md"),
		"---\nname: x\n---\n\nAfter frontmatter\n")
	writeText(t, filepath.Join(home, ".claude", "commands", "quoted.md"),
		"---\ndescription: 'Single quoted'\n---\nbody\n")
	writeText(t, filepath.Join(home, ".claude", "commands", "long.md"), strings.Repeat("a", 200)+"\n")

	cmds := Commands(home, home)

	if c, _ := findCommand(cmds, "nofront"); c.Description != "First line here" {
		t.Errorf("nofront = %q", c.Description)
	}
	if c, _ := findCommand(cmds, "emptyfront"); c.Description != "After frontmatter" {
		t.Errorf("emptyfront = %q", c.Description)
	}
	if c, _ := findCommand(cmds, "quoted"); c.Description != "Single quoted" {
		t.Errorf("quoted = %q", c.Description)
	}
	if c, _ := findCommand(cmds, "long"); len([]rune(c.Description)) != 120 {
		t.Errorf("long description length = %d, want 120", len([]rune(c.Description)))
	}
}

func TestCommandsCache(t *testing.T) {
	resetCommands(t)
	home := t.TempDir()
	cwd := t.TempDir()
	commandsTTL = 20 * time.Millisecond

	first := Commands(cwd, home)
	if _, ok := findCommand(first, "added"); ok {
		t.Fatalf("added present before it was written")
	}

	writeText(t, filepath.Join(cwd, ".claude", "commands", "added.md"), "New command\n")

	if _, ok := findCommand(Commands(cwd, home), "added"); ok {
		t.Fatalf("cached list picked up a new command")
	}

	time.Sleep(30 * time.Millisecond)
	if _, ok := findCommand(Commands(cwd, home), "added"); !ok {
		t.Fatalf("expired cache did not pick up the new command")
	}
}
