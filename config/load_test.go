package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

type fakeModule struct{ name string }

func (m fakeModule) ID() string                                     { return m.name }
func (m fakeModule) Requires() []core.ServiceKey                    { return nil }
func (m fakeModule) Provides() []core.ServiceKey                    { return nil }
func (m fakeModule) Install(*core.Builder, core.ModuleConfig) error { return nil }
func (m fakeModule) Stop(context.Context) error                     { return nil }

func TestMain(m *testing.M) {
	for _, name := range []string{"llm", "tools", "sqlite_store", "agent", "permission", "approval", "compaction", "cli_runner", "test_mod"} {
		n := name
		core.RegisterModule(n, func() core.Module { return fakeModule{name: n} })
	}
	os.Exit(m.Run())
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func envMap(m map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := m[name]
		return v, ok
	}
}

func TestLayerPatchWins(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[agents.x]\nmodel = \"from-config\"\n")
	patch := filepath.Join(t.TempDir(), "a.toml")
	writeFile(t, patch, "[agents.x]\nmodel = \"from-patch\"\n")
	res, err := Load(context.Background(), Options{Home: home, Patches: []string{patch}, Env: envMap(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Config.Agents["x"].Model; got != "from-patch" {
		t.Fatalf("model=%q", got)
	}
	if got, _ := res.Origins.Of("agents.x.model"); got != patch {
		t.Fatalf("origin=%q want %q", got, patch)
	}
	if got, _ := res.Origins.Of("agents.x"); got != patch {
		t.Fatalf("table origin=%q want %q", got, patch)
	}
}

func TestMissingProfileSkipped(t *testing.T) {
	home := t.TempDir()
	res, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "profiles", "minimal.toml")
	found := false
	for _, layer := range res.Layers {
		if layer.Name == path {
			found = true
			if layer.Present {
				t.Fatal("missing profile layer marked as present")
			}
		}
	}
	if !found {
		t.Fatal("missing profile layer not recorded")
	}
}

func TestLanaHomeEnv(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[agents.x]\nmodel = \"home\"\n")
	env := envMap(map[string]string{"LANA_HOME": home})
	got, err := HomeDir(env)
	if err != nil {
		t.Fatal(err)
	}
	if got != home {
		t.Fatalf("HomeDir=%q", got)
	}
	res, err := Load(context.Background(), Options{Home: got, Env: env})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Agents["x"].Model != "home" {
		t.Fatalf("config.toml was not read from LANA_HOME")
	}
}

func TestMemoryLayerNoFiles(t *testing.T) {
	res, err := Load(context.Background(), Options{
		Layers: []Layer{{Name: "myservice", TOML: []byte("[agents.x]\nmodel = \"mem\"\n")}},
		Env:    envMap(nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Config.Agents["x"].Model != "mem" {
		t.Fatalf("model=%q", res.Config.Agents["x"].Model)
	}
	if got, _ := res.Origins.Of("agents.x.model"); got != "myservice" {
		t.Fatalf("origin=%q", got)
	}
	for _, layer := range res.Layers {
		if layer.Path != "" {
			t.Fatalf("file layer read although Home is empty: %s", layer.Path)
		}
	}
}
