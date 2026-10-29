package config

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigRejectsBadPattern(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[agents.coder]\nmodel = \"m\"\n[agents.coder.rules]\nallow = [\"bash(git *\"]\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error bukan Errors: %v", err)
	}
	if !strings.Contains(err.Error(), "agents.coder.rules.allow.0") {
		t.Fatalf("error tidak menyebut key: %v", err)
	}
}
