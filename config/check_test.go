package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

func TestCheckAgreesWithLoad(t *testing.T) {
	tests := []struct {
		name string
		toml string
	}{
		{
			name: "valid",
			toml: "[providers.mock]\nkind = \"openai_compat\"\nbase_url = \"mock://local\"\n\n[agents.coder]\nprovider = \"mock\"\nmodel = \"coder-model\"\n",
		},
		{
			name: "invalid provider",
			toml: "[agents.coder]\nprovider = \"nope\"\nmodel = \"coder-model\"\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			writeFile(t, filepath.Join(home, "config.toml"), tt.toml)

			loaded, loadErr := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
			checked, checkErr := Check(context.Background(), home, []byte(tt.toml))
			if (loadErr == nil) != (checkErr == nil) {
				t.Fatalf("Load error = %v, Check error = %v", loadErr, checkErr)
			}
			if loadErr != nil {
				var loadErrs, checkErrs Errors
				if !errors.As(loadErr, &loadErrs) {
					t.Fatalf("Load error is not Errors: %v", loadErr)
				}
				if !errors.As(checkErr, &checkErrs) {
					t.Fatalf("Check error is not Errors: %v", checkErr)
				}
				if !reflect.DeepEqual(loadErrs, checkErrs) {
					t.Fatalf("Load errors = %#v, Check errors = %#v", loadErrs, checkErrs)
				}
				return
			}
			if got, want := checked.Config.Agents["coder"].Model, loaded.Config.Agents["coder"].Model; got != want {
				t.Fatalf("Check model = %q, Load model = %q", got, want)
			}
		})
	}
}

func TestCheckDoesNotWriteFile(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.toml")
	onDisk := "[agents.saved]\nmodel = \"on-disk\"\n"
	writeFile(t, path, onDisk)

	candidate := []byte("[agents.saved]\nmodel = \"candidate\"\n")
	result, err := Check(context.Background(), home, candidate)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got := result.Config.Agents["saved"].Model; got != "candidate" {
		t.Fatalf("Check model = %q", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != onDisk {
		t.Fatalf("Check changed config.toml: %q", data)
	}
}

// TestLegacyUserWarningsCoversACHOME010 covers AC-HOME-010: config check warns
// for a per-agent USER.md that still has entries and names the shared file.
func TestLegacyUserWarningsCoversACHOME010(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "agents", "worker", "USER.md"), "Name: Old\n§\nLanguage: Indonesian\n")
	writeFile(t, filepath.Join(home, "agents", "helper", "USER.md"), "")
	cfg := core.Config{Agents: map[string]core.AgentConfig{"worker": {}, "helper": {}}}
	warnings := LegacyUserWarnings(home, cfg)
	if len(warnings) != 1 {
		t.Fatalf("warnings = %q", warnings)
	}
	for _, want := range []string{"agents/worker/USER.md is not read", agenthome.SharedUser(home)} {
		if !strings.Contains(warnings[0], want) {
			t.Fatalf("warning %q lacks %q", warnings[0], want)
		}
	}
}
