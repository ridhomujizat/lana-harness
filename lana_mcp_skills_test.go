package lana

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ridhomujizat/lana-harness/config"
)

func writeSkill(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeMCPStatusAndSkills(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, filepath.Join(home, "skills", "demo", "SKILL.md"),
		"---\nname: demo\ndescription: Demo skill.\n---\nDo it.\n")
	cfg := `[providers.local]
kind = "openai_compat"
base_url = "http://127.0.0.1:1"

[agents.assistant]
toolset = "custom"
provider = "local"
model = "m"
skills = ["demo"]

[agents.with_mcp]
toolset = "custom"
provider = "local"
model = "m"
mcp = ["fake"]

[agents.plain]
toolset = "custom"
provider = "local"
model = "m"

[mcp.fake]
command = "echo"
`
	rt, err := New(context.Background(), Options{Home: home, Layers: []config.Layer{{Name: "test", TOML: []byte(cfg)}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	if got := rt.MCPStatus("plain"); got != nil {
		t.Fatalf("plain MCPStatus = %+v, want nil", got)
	}
	status := rt.MCPStatus("with_mcp")
	if len(status) != 1 || status[0].Name != "fake" || status[0].State != "stopped" {
		t.Fatalf("with_mcp MCPStatus = %+v", status)
	}

	skills := rt.Skills("assistant")
	if len(skills) != 1 || skills[0].Name != "demo" || skills[0].Scope != "global" {
		t.Fatalf("assistant skills = %+v", skills)
	}
	if got := rt.Skills("plain"); len(got) != 0 {
		t.Fatalf("plain skills = %+v, want none", got)
	}
	if got := rt.Skills("ghost"); got != nil {
		t.Fatalf("unknown agent skills = %+v, want nil", got)
	}
}

func TestRuntimeSkillsWithoutHome(t *testing.T) {
	rt := newTestRuntime(t, "http://127.0.0.1:1", nil)
	if got := rt.Skills("assistant"); got != nil {
		t.Fatalf("skills without home = %+v, want nil", got)
	}
	if got := rt.MCPStatus("assistant"); got != nil {
		t.Fatalf("MCPStatus without mcp = %+v, want nil", got)
	}
}
