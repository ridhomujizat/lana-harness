package config

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// loadSettingsTable loads a config.toml that holds one table.
func loadSettingsTable(t *testing.T, table string) error {
	t.Helper()
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), table)
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	return err
}

// errorKey reports whether the error is an Errors list that holds key.
func errorKey(t *testing.T, err error, key string) bool {
	t.Helper()
	var errs Errors
	if !errors.As(err, &errs) {
		return false
	}
	return findError(errs, key) != nil
}

// REQ-MDL-001: reasoning_effort accepts off, low, high, max, or empty, and
// rejects every other value.
func TestAgentReasoningEffortValidation(t *testing.T) {
	accepted := []string{"", "off", "low", "high", "max"}
	for _, value := range accepted {
		t.Run("accept "+value, func(t *testing.T) {
			table := "[agents.coder]\nreasoning_effort = \"" + value + "\"\n"
			if err := loadSettingsTable(t, table); err != nil {
				t.Fatalf("reasoning_effort %q rejected: %v", value, err)
			}
		})
	}
	rejected := []string{"medium", "HIGH", "Low", "none", "on"}
	for _, value := range rejected {
		t.Run("reject "+value, func(t *testing.T) {
			table := "[agents.coder]\nreasoning_effort = \"" + value + "\"\n"
			err := loadSettingsTable(t, table)
			if !errorKey(t, err, "agents.coder.reasoning_effort") {
				t.Fatalf("reasoning_effort %q not rejected: %v", value, err)
			}
		})
	}
	t.Run("reject non string", func(t *testing.T) {
		err := loadSettingsTable(t, "[agents.coder]\nreasoning_effort = 1\n")
		if !errorKey(t, err, "agents.coder.reasoning_effort") {
			t.Fatalf("integer reasoning_effort not rejected: %v", err)
		}
	})
}

// REQ-MDL-002: thinking_format accepts deepseek, none, or empty, and rejects
// every other value.
func TestProviderThinkingFormatValidation(t *testing.T) {
	accepted := []string{"", "deepseek", "none"}
	for _, value := range accepted {
		t.Run("accept "+value, func(t *testing.T) {
			table := "[providers.p]\nkind = \"openai_compat\"\nthinking_format = \"" + value + "\"\n"
			if err := loadSettingsTable(t, table); err != nil {
				t.Fatalf("thinking_format %q rejected: %v", value, err)
			}
		})
	}
	rejected := []string{"openai", "DeepSeek", "auto"}
	for _, value := range rejected {
		t.Run("reject "+value, func(t *testing.T) {
			table := "[providers.p]\nkind = \"openai_compat\"\nthinking_format = \"" + value + "\"\n"
			err := loadSettingsTable(t, table)
			if !errorKey(t, err, "providers.p.thinking_format") {
				t.Fatalf("thinking_format %q not rejected: %v", value, err)
			}
		})
	}
	t.Run("reject non string", func(t *testing.T) {
		err := loadSettingsTable(t, "[providers.p]\nkind = \"openai_compat\"\nthinking_format = 1\n")
		if !errorKey(t, err, "providers.p.thinking_format") {
			t.Fatalf("integer thinking_format not rejected: %v", err)
		}
	})
}
