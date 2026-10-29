package config

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func loadWith(t *testing.T, home string, patches ...string) (*Result, error) {
	t.Helper()
	return Load(context.Background(), Options{Home: home, Patches: patches, Env: envMap(nil)})
}

func findModule(t *testing.T, res *Result, id string) int {
	t.Helper()
	for i, row := range res.Config.Modules {
		if row.ID == id {
			return i
		}
	}
	t.Fatalf("modul %q tidak ditemukan", id)
	return -1
}

func TestMergeModuleRowReplacesConfig(t *testing.T) {
	res, err := Load(context.Background(), Options{
		Layers: []Layer{
			{Name: "m1", TOML: []byte("[[modules]]\nid = \"store\"\nmodule = \"sqlite_store\"\nconfig = { path = \"a.db\", wal = true }\n")},
			{Name: "m2", TOML: []byte("[[modules]]\nid = \"store\"\nmodule = \"sqlite_store\"\nconfig = { path = \"b.db\" }\n")},
		},
		Env: envMap(nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	i := findModule(t, res, "store")
	cfg := res.Config.Modules[i].Config
	if cfg["path"] != "b.db" {
		t.Fatalf("path=%v", cfg["path"])
	}
	if _, ok := cfg["wal"]; ok {
		t.Fatalf("config lama tidak diganti: %v", cfg)
	}
}

func TestMergeModuleAfter(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[[modules]]\nid = \"x\"\nmodule = \"test_mod\"\nafter = \"store\"\n")
	res, err := loadWith(t, home)
	if err != nil {
		t.Fatal(err)
	}
	store := findModule(t, res, "store")
	x := findModule(t, res, "x")
	if x != store+1 {
		t.Fatalf("x di indeks %d, store di %d", x, store)
	}
}

func TestMergeAgentTablesRecursive(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[agents.a]\nmodel = \"m1\"\ntools = [\"read_file\"]\n")
	patch := filepath.Join(t.TempDir(), "p.toml")
	writeFile(t, patch, "[agents.a]\nmodel = \"m2\"\n")
	res, err := loadWith(t, home, patch)
	if err != nil {
		t.Fatal(err)
	}
	a := res.Config.Agents["a"]
	if a.Model != "m2" {
		t.Fatalf("model=%q", a.Model)
	}
	if len(a.Tools) != 1 || a.Tools[0] != "read_file" {
		t.Fatalf("tools=%v", a.Tools)
	}
}

func TestMergeRoutesReplaced(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[agents.a]\nmodel = \"m\"\n\n[[routes]]\nchannel = \"cli\"\nagent = \"a\"\n\n[[routes]]\nchannel = \"telegram\"\nagent = \"a\"\n")
	patch := filepath.Join(t.TempDir(), "p.toml")
	writeFile(t, patch, "[[routes]]\nchannel = \"slack\"\nagent = \"a\"\n")
	res, err := loadWith(t, home, patch)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Config.Routes) != 1 || res.Config.Routes[0].Channel != "slack" {
		t.Fatalf("routes=%v", res.Config.Routes)
	}
}

func TestMergeAfterUnknownID(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[[modules]]\nid = \"x\"\nmodule = \"test_mod\"\nafter = \"nope\"\n")
	_, err := loadWith(t, home)
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error bukan Errors: %v", err)
	}
	if !strings.Contains(err.Error(), "id 'nope' tidak ada") {
		t.Fatalf("pesan salah: %v", err)
	}
}
