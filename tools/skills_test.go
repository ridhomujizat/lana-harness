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
	// The default skill_write stages a proposal; a direct agent writes instead.
	direct := tc
	direct.SkillWrite = "direct"
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

	out = callSkill(t, write, direct, `{"name":"x","description":"Use x.","body":"Step 1."}`)
	if out.Status != core.ToolStatusSuccess || out.Output != "created skill x" {
		t.Fatalf("create x = %+v", out)
	}
	out = callSkill(t, write, direct, `{"name":"x","description":"Use x.","body":"Step 1."}`)
	if out.Status != core.ToolStatusSuccess || out.Output != "updated skill x" {
		t.Fatalf("update x = %+v", out)
	}
	if _, err := os.Stat(filepath.Join(home, "agents", "worker", "skills", "x", "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md not written: %v", err)
	}

	out = callSkill(t, write, direct, `{"name":"Bad","description":"Use bad.","body":"Step 1."}`)
	if out.Status != core.ToolStatusError || !strings.HasPrefix(out.Output, "invalid input: ") {
		t.Fatalf("invalid name = %+v", out)
	}
}

// REQ-DKN-050: the default and "approve" skill_write stage a proposal that
// skill_load cannot see, and never write to the agent's skills.
func TestSkillWriteStagesProposal(t *testing.T) {
	for _, mode := range []string{"", "approve"} {
		t.Run("skill_write="+mode, func(t *testing.T) {
			home := t.TempDir()
			tc := core.ToolCtx{AgentName: "worker", Home: home, SessionID: "s1", SkillWrite: mode, OwnerTurn: true}
			out := callSkill(t, NewSkillWrite(), tc, `{"name":"x","description":"Use x.","body":"Step 1."}`)
			if out.Status != core.ToolStatusSuccess || out.Output != "proposed skill x; the owner reviews it in Knowledge → Review" {
				t.Fatalf("write = %+v", out)
			}
			if _, err := os.Stat(filepath.Join(home, "agents", "worker", "proposed-skills", "x", "SKILL.md")); err != nil {
				t.Fatalf("proposal not written: %v", err)
			}
			if _, err := os.Stat(filepath.Join(home, "agents", "worker", "skills", "x")); !os.IsNotExist(err) {
				t.Fatalf("skill written despite staged mode: %v", err)
			}
			load := callSkill(t, NewSkillLoad(), tc, `{"name":"x"}`)
			if load.Status != core.ToolStatusError || !strings.Contains(load.Output, `unknown skill "x"`) {
				t.Fatalf("load of a proposal = %+v", load)
			}
		})
	}
}

// REQ-DKN-050: skill_write = "direct" writes the skill at once.
func TestSkillWriteDirectWrites(t *testing.T) {
	home := t.TempDir()
	tc := core.ToolCtx{AgentName: "worker", Home: home, SkillWrite: "direct"}
	out := callSkill(t, NewSkillWrite(), tc, `{"name":"x","description":"Use x.","body":"Step 1."}`)
	if out.Status != core.ToolStatusSuccess || out.Output != "created skill x" {
		t.Fatalf("write = %+v", out)
	}
	if _, err := os.Stat(filepath.Join(home, "agents", "worker", "skills", "x", "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "agents", "worker", "proposed-skills", "x")); !os.IsNotExist(err) {
		t.Fatalf("proposal written despite direct mode: %v", err)
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
