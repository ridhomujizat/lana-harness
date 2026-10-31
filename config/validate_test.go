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
	writeFile(t, filepath.Join(home, "config.toml"), "[agents.x]\npermision = \"ask\"\nmax_steps = -1\n")
	patch := filepath.Join(t.TempDir(), "p.toml")
	writeFile(t, patch, "[[modules]]\nid = \"x\"\nmodule = \"missing_mod\"\n")
	_, err := Load(context.Background(), Options{Home: home, Patches: []string{patch}, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	if len(errs) < 3 {
		t.Fatalf("only %d errors: %v", len(errs), errs)
	}
	wantFiles := map[string]bool{filepath.Join(home, "config.toml"): false, patch: false}
	for _, e := range errs {
		if _, ok := wantFiles[e.File]; ok {
			wantFiles[e.File] = true
		}
	}
	for file, seen := range wantFiles {
		if !seen {
			t.Fatalf("no error from %s: %v", file, errs)
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
		t.Fatalf("error is not Errors: %v", err)
	}
	found := false
	for _, e := range errs {
		if e.Key == "agents.coder.provider" {
			found = true
			if e.Line != 12 {
				t.Fatalf("line=%d want 12 (%v)", e.Line, e)
			}
			if !strings.Contains(e.Error(), filepath.Join(home, "config.toml")+":12") {
				t.Fatalf("incomplete location: %s", e.Error())
			}
		}
	}
	if !found {
		t.Fatalf("reference error missing: %v", errs)
	}
}

func TestUnknownKeySuggestion(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[agents.coder]\npermision = \"ask\"\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	found := false
	for _, e := range errs {
		if e.Key == "agents.coder.permision" {
			found = true
			if !strings.Contains(e.Msg, "permission") {
				t.Fatalf("no suggestion: %s", e.Msg)
			}
		}
	}
	if !found {
		t.Fatalf("key error missing: %v", errs)
	}
}

func TestVersionTooHigh(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "version = 2\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	if !strings.Contains(err.Error(), "config version 2 is higher than the supported version (1)") {
		t.Fatalf("wrong version message: %v", err)
	}
}

func TestMemoryLayerRefError(t *testing.T) {
	_, err := Load(context.Background(), Options{
		Layers: []Layer{{Name: "mem", TOML: []byte("[agents.x]\nprovider = \"nope\"\n")}},
		Env:    envMap(nil),
	})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	for _, e := range errs {
		if e.Key == "agents.x.provider" {
			if e.File != "mem" || e.Line != 2 {
				t.Fatalf("wrong location: %+v", e)
			}
			return
		}
	}
	t.Fatalf("memory layer reference error missing: %v", errs)
}

// REQ-LTC-040: max_steps accepts 0 (no limit) and any positive value with no
// upper bound, and rejects a negative value.
func TestMaxStepsRange(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[agents.x]\nmax_steps = 300\n")
	if _, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)}); err != nil {
		t.Fatalf("max_steps = 300 rejected: %v", err)
	}

	home = t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[agents.x]\nmax_steps = -1\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	found := false
	for _, e := range errs {
		if e.Key == "agents.x.max_steps" {
			found = true
		}
	}
	if !found {
		t.Fatalf("negative max_steps not rejected: %v", errs)
	}
}

// REQ-LTC-030: a provider timeout that parses to <= 0 is rejected.
func TestProviderTimeoutsRejectNonPositive(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"),
		"[providers.p]\nkind = \"openai_compat\"\nstream_idle_timeout = \"0s\"\ntimeout = \"-1s\"\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	keys := map[string]bool{}
	for _, e := range errs {
		keys[e.Key] = true
	}
	if !keys["providers.p.stream_idle_timeout"] || !keys["providers.p.timeout"] {
		t.Fatalf("non-positive timeouts not rejected: %v", errs)
	}
}

func TestAgentMCPNotRegistered(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[agents.x]\nmcp = [\"nope\"]\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("error does not mention nope: %v", err)
	}
}
