package config

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestErrorsCollectAllLayers(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[agents.x]\npermision = \"ask\"\nmax_steps = 0\n")
	patch := filepath.Join(t.TempDir(), "p.toml")
	writeFile(t, patch, "[[modules]]\nid = \"x\"\nmodule = \"tidak_ada\"\n")
	_, err := Load(context.Background(), Options{Home: home, Patches: []string{patch}, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error bukan Errors: %v", err)
	}
	if len(errs) < 3 {
		t.Fatalf("hanya %d error: %v", len(errs), errs)
	}
	wantFiles := map[string]bool{filepath.Join(home, "config.toml"): false, patch: false}
	for _, e := range errs {
		if _, ok := wantFiles[e.File]; ok {
			wantFiles[e.File] = true
		}
	}
	for file, seen := range wantFiles {
		if !seen {
			t.Fatalf("tidak ada error dari %s: %v", file, errs)
		}
	}
}

func TestRefErrorLocation(t *testing.T) {
	home := t.TempDir()
	lines := []string{
		"# 1",
		"# 2",
		"# 3",
		"# 4",
		"# 5",
		"# 6",
		"# 7",
		"# 8",
		"# 9",
		"# 10",
		"[agents.coder]",
		"provider = \"x\"",
	}
	writeFile(t, filepath.Join(home, "config.toml"), strings.Join(lines, "\n")+"\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error bukan Errors: %v", err)
	}
	found := false
	for _, e := range errs {
		if e.Key == "agents.coder.provider" {
			found = true
			if e.Line != 12 {
				t.Fatalf("line=%d want 12 (%v)", e.Line, e)
			}
			if !strings.Contains(e.Error(), filepath.Join(home, "config.toml")+":12") {
				t.Fatalf("lokasi tidak lengkap: %s", e.Error())
			}
		}
	}
	if !found {
		t.Fatalf("error rujukan tidak ada: %v", errs)
	}
}

func TestUnknownKeySuggestion(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[agents.coder]\npermision = \"ask\"\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error bukan Errors: %v", err)
	}
	found := false
	for _, e := range errs {
		if e.Key == "agents.coder.permision" {
			found = true
			if !strings.Contains(e.Msg, "permission") {
				t.Fatalf("tidak ada saran: %s", e.Msg)
			}
		}
	}
	if !found {
		t.Fatalf("error key tidak ada: %v", errs)
	}
}

func TestVersionTooHigh(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "version = 2\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error bukan Errors: %v", err)
	}
	if !strings.Contains(err.Error(), "versi config 2 lebih tinggi dari yang didukung (1)") {
		t.Fatalf("pesan versi salah: %v", err)
	}
}

func TestMemoryLayerRefError(t *testing.T) {
	_, err := Load(context.Background(), Options{
		Layers: []Layer{{Name: "mem", TOML: []byte("[agents.x]\nprovider = \"nope\"\n")}},
		Env:    envMap(nil),
	})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error bukan Errors: %v", err)
	}
	for _, e := range errs {
		if e.Key == "agents.x.provider" {
			if e.File != "mem" || e.Line != 2 {
				t.Fatalf("lokasi salah: %+v", e)
			}
			return
		}
	}
	t.Fatalf("error rujukan memori tidak ada: %v", errs)
}
