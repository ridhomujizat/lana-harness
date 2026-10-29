package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

func TestResolveAgent(t *testing.T) {
	cfg := core.Config{
		Agents: map[string]core.AgentConfig{
			"asisten": {Provider: "openai", Model: "m"},
			"coder":   {Provider: "openai", Model: "m"},
		},
		Routes: []core.RouteConfig{{Channel: "library", Agent: "coder"}},
	}
	if got, err := resolveAgent(cfg, "asisten"); err != nil || got != "asisten" {
		t.Fatalf("flag: %q %v", got, err)
	}
	if _, err := resolveAgent(cfg, "tidakada"); err == nil || !strings.Contains(err.Error(), "asisten") {
		t.Fatalf("unknown: %v", err)
	}
	cfg.Routes = append(cfg.Routes, core.RouteConfig{Channel: "cli", Agent: "coder"})
	if got, err := resolveAgent(cfg, ""); err != nil || got != "coder" {
		t.Fatalf("route: %q %v", got, err)
	}
	one := core.Config{Agents: map[string]core.AgentConfig{"solo": {}}}
	if got, err := resolveAgent(one, ""); err != nil || got != "solo" {
		t.Fatalf("single: %q %v", got, err)
	}
	withDefault := core.Config{Agents: map[string]core.AgentConfig{"default": {}, "lain": {}}}
	if got, err := resolveAgent(withDefault, ""); err != nil || got != "default" {
		t.Fatalf("default: %q %v", got, err)
	}
	empty := core.Config{}
	if _, err := resolveAgent(empty, ""); err == nil || !strings.Contains(err.Error(), "lana init") {
		t.Fatalf("empty: %v", err)
	}
	many := core.Config{Agents: map[string]core.AgentConfig{"a": {}, "b": {}}}
	if _, err := resolveAgent(many, ""); err == nil || !strings.Contains(err.Error(), "--agent") {
		t.Fatalf("many: %v", err)
	}
}

func TestSetupChatLoggingWritesFile(t *testing.T) {
	home := t.TempDir()
	restore := setupChatLogging(home)
	slog.Info("halo-log")
	restore()
	path := filepath.Join(home, "logs", "lana.log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "halo-log") {
		t.Fatalf("log tidak tertulis: %q", string(data))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permission = %v", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(home, "logs")); err != nil {
		t.Fatal(err)
	}
}
