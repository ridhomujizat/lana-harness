package config

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

func TestBuiltinProfileRows(t *testing.T) {
	home := t.TempDir()
	res, err := Load(context.Background(), Options{Home: home, Profile: "minimal", Env: envMap(nil)})
	if err != nil {
		t.Fatal(err)
	}
	want := []core.ModuleRow{
		{ID: "llm", Module: "llm"},
		{ID: "tools", Module: "tools"},
		{ID: "mcp", Module: "mcp"},
		{ID: "store", Module: "sqlite_store"},
		{ID: "agent", Module: "agent"},
		{ID: "permission", Module: "permission"},
		{ID: "approval", Module: "approval"},
		{ID: "compaction", Module: "compaction"},
	}
	if len(res.Config.Modules) != len(want) {
		t.Fatalf("modules=%d want %d: %+v", len(res.Config.Modules), len(want), res.Config.Modules)
	}
	for i := range want {
		got := res.Config.Modules[i]
		if got.ID != want[i].ID || got.Module != want[i].Module {
			t.Fatalf("row %d = %+v want %+v", i, got, want[i])
		}
	}
	if got, _ := res.Origins.Of("modules.llm"); got != "base" {
		t.Fatalf("origin modules.llm=%q", got)
	}

	headless, err := Load(context.Background(), Options{Home: home, Profile: "headless", Env: envMap(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if len(headless.Config.Modules) != len(want)+1 {
		t.Fatalf("headless modules=%d", len(headless.Config.Modules))
	}
	last := headless.Config.Modules[len(headless.Config.Modules)-1]
	if last.ID != "runner" || last.Module != "cli_runner" {
		t.Fatalf("runner row=%+v", last)
	}
	if got, _ := headless.Origins.Of("modules.runner"); got != "profile:headless" {
		t.Fatalf("origin runner=%q", got)
	}
}

func TestUnknownProfileError(t *testing.T) {
	_, err := Load(context.Background(), Options{Home: t.TempDir(), Profile: "xyz", Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "xyz") || !strings.Contains(msg, "headless") || !strings.Contains(msg, "minimal") {
		t.Fatalf("wrong profile message: %s", msg)
	}
	if filepath.IsAbs(msg) {
		t.Fatalf("message should not be a path: %s", msg)
	}
}
