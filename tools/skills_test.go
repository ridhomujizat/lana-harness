package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

func callSkill(t *testing.T, tool core.Tool, tc core.ToolCtx, input string) core.ToolOutput {
	t.Helper()
	out, err := tool.Call(context.Background(), tc, json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func writeSkillFile(t *testing.T, path, name, description, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	text := "---\nname: " + name + "\ndescription: " + description + "\n---\n" + body + "\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSkillTools(t *testing.T) {
	home := t.TempDir()
	writeSkillFile(t, filepath.Join(home, "skills", "a", "SKILL.md"), "a", "Skill a.", "Do a.")
	writeSkillFile(t, filepath.Join(home, "skills", "b", "SKILL.md"), "b", "Skill b.", "Do b.")
	cDir := filepath.Join(home, "agents", "worker", "skills", "c")
	writeSkillFile(t, filepath.Join(cDir, "SKILL.md"), "c", "Skill c.", "Do c.")
	for _, rel := range []string{"run.sh", "ref/notes.md"} {
		path := filepath.Join(cDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tc := core.ToolCtx{AgentName: "worker", Home: home, Skills: []string{"a"}}
	load, write := NewSkillLoad(), NewSkillWrite()

	out := callSkill(t, load, tc, `{"name":"a"}`)
	if out.Status != core.ToolStatusSuccess || !strings.Contains(out.Output, "(global)") {
		t.Fatalf("load a = %+v", out)
	}
	out = callSkill(t, load, tc, `{"name":"c"}`)
	if out.Status != core.ToolStatusSuccess || !strings.Contains(out.Output, "files: ref/notes.md, run.sh") {
		t.Fatalf("load c = %+v", out)
	}
	out = callSkill(t, load, tc, `{"name":"b"}`)
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, `unknown skill "b" (available: a, c)`) {
		t.Fatalf("load b = %+v", out)
	}

	out = callSkill(t, write, tc, `{"name":"x","description":"Use x.","body":"Step 1."}`)
	if out.Status != core.ToolStatusSuccess || out.Output != "created skill x" {
		t.Fatalf("create x = %+v", out)
	}
	out = callSkill(t, write, tc, `{"name":"x","description":"Use x.","body":"Step 1."}`)
	if out.Status != core.ToolStatusSuccess || out.Output != "updated skill x" {
		t.Fatalf("update x = %+v", out)
	}
	if _, err := os.Stat(filepath.Join(home, "agents", "worker", "skills", "x", "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md not written: %v", err)
	}

	out = callSkill(t, write, tc, `{"name":"Bad","description":"Use bad.","body":"Step 1."}`)
	if out.Status != core.ToolStatusError || !strings.HasPrefix(out.Output, "invalid input: ") {
		t.Fatalf("invalid name = %+v", out)
	}
}

func TestSkillUnavailableWithoutHome(t *testing.T) {
	tc := core.ToolCtx{AgentName: "worker"}
	out := callSkill(t, NewSkillLoad(), tc, `{"name":"a"}`)
	if out.Status != core.ToolStatusError || out.Output != "skills are not available" {
		t.Fatalf("load = %+v", out)
	}
	out = callSkill(t, NewSkillWrite(), tc, `{"name":"a","description":"Use a.","body":"Do a."}`)
	if out.Status != core.ToolStatusError || out.Output != "skills are not available" {
		t.Fatalf("write = %+v", out)
	}
}
