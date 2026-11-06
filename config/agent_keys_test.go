package config

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// loadAgentKeys loads a config.toml that holds a single [agents.coder] table.
func loadAgentKeys(t *testing.T, table string) (*Result, error) {
	t.Helper()
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), table)
	return Load(context.Background(), Options{Home: home, Env: envMap(nil)})
}

// agentKeyErrors returns the validation errors of a single agent table.
func agentKeyErrors(t *testing.T, table string) Errors {
	t.Helper()
	_, err := loadAgentKeys(t, table)
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	return errs
}

func findError(errs Errors, key string) *Error {
	for i := range errs {
		if errs[i].Key == key {
			return &errs[i]
		}
	}
	return nil
}

func TestAgentDescriptionValidation(t *testing.T) {
	withDescription := func(value string) string {
		return "[agents.coder]\ndescription = \"" + value + "\"\n"
	}
	tests := []struct {
		name  string
		table string
		ok    bool
		msg   string
	}{
		{name: "120 characters pass", table: withDescription(strings.Repeat("a", 120)), ok: true},
		{name: "121 characters fail", table: withDescription(strings.Repeat("a", 121)), msg: "120"},
		{name: "120 multibyte characters pass", table: withDescription(strings.Repeat("é", 120)), ok: true},
		{name: "121 multibyte characters fail", table: withDescription(strings.Repeat("é", 121)), msg: "120"},
		{name: "newline fails", table: withDescription(`one\ntwo`), msg: "one line"},
		{name: "carriage return fails", table: withDescription(`one\rtwo`), msg: "one line"},
		{name: "non string fails", table: "[agents.coder]\ndescription = 1\n", msg: "must be string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadAgentKeys(t, tt.table)
			if tt.ok {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var errs Errors
			if !errors.As(err, &errs) {
				t.Fatalf("error is not Errors: %v", err)
			}
			found := findError(errs, "agents.coder.description")
			if found == nil {
				t.Fatalf("no error for agents.coder.description: %v", errs)
			}
			if !strings.Contains(found.Msg, tt.msg) {
				t.Fatalf("msg = %q, want it to contain %q", found.Msg, tt.msg)
			}
		})
	}
}

func TestAgentDisabledToolsValidation(t *testing.T) {
	withDisabled := func(value string) string {
		return "[agents.coder]\ndisabled_tools = " + value + "\n"
	}
	valid := []struct {
		name  string
		table string
	}{
		{name: "tool and mcp tool pass", table: withDisabled(`["bash", "github/create_issue"]`)},
		{name: "unknown tool is kept", table: withDisabled(`["not_a_real_tool", "server/not_a_tool"]`)},
		{name: "empty list passes", table: withDisabled(`[]`)},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := loadAgentKeys(t, tt.table); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
	invalid := []struct {
		name  string
		table string
	}{
		{name: "empty item fails", table: withDisabled(`["bash", ""]`)},
		{name: "two slashes fail", table: withDisabled(`["a/b/c"]`)},
		{name: "leading slash fails", table: withDisabled(`["/bash"]`)},
		{name: "trailing slash fails", table: withDisabled(`["github/"]`)},
		{name: "whitespace fails", table: withDisabled(`["two words"]`)},
		{name: "non array fails", table: withDisabled(`"bash"`)},
		{name: "non string item fails", table: withDisabled(`["bash", 1]`)},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			errs := agentKeyErrors(t, tt.table)
			if findError(errs, "agents.coder.disabled_tools") == nil {
				t.Fatalf("no error for agents.coder.disabled_tools: %v", errs)
			}
		})
	}
}

func TestAgentMemoryCaptureValidation(t *testing.T) {
	for _, table := range []string{
		"[agents.coder]\nmemory_capture = true\n",
		"[agents.coder]\nmemory_capture = false\n",
	} {
		if _, err := loadAgentKeys(t, table); err != nil {
			t.Fatalf("unexpected error for %q: %v", table, err)
		}
	}
	errs := agentKeyErrors(t, "[agents.coder]\nmemory_capture = \"yes\"\n")
	found := findError(errs, "agents.coder.memory_capture")
	if found == nil || !strings.Contains(found.Msg, "boolean") {
		t.Fatalf("memory_capture error = %+v", found)
	}
}

func TestAgentMemoryCaptureLoaded(t *testing.T) {
	res, err := loadAgentKeys(t, "[agents.coder]\nmemory_capture = false\n")
	if err != nil {
		t.Fatal(err)
	}
	cfg, ok := res.Config.Agents["coder"]
	if !ok || cfg.MemoryCapture == nil || *cfg.MemoryCapture {
		t.Fatalf("memory_capture = %v", cfg.MemoryCapture)
	}
}

func TestAgentWorkspaceOnlyValidation(t *testing.T) {
	for _, table := range []string{
		"[agents.coder]\nworkspace_only = true\n",
		"[agents.coder]\nworkspace_only = false\n",
	} {
		if _, err := loadAgentKeys(t, table); err != nil {
			t.Fatalf("unexpected error for %q: %v", table, err)
		}
	}
	errs := agentKeyErrors(t, "[agents.coder]\nworkspace_only = \"yes\"\n")
	found := findError(errs, "agents.coder.workspace_only")
	if found == nil || !strings.Contains(found.Msg, "boolean") {
		t.Fatalf("workspace_only error = %+v", found)
	}
}

func TestAgentWorkspaceOnlyLoaded(t *testing.T) {
	res, err := loadAgentKeys(t, "[agents.coder]\nworkspace_only = true\n")
	if err != nil {
		t.Fatal(err)
	}
	cfg, ok := res.Config.Agents["coder"]
	if !ok || !cfg.WorkspaceOnly {
		t.Fatalf("workspace_only = %v", cfg.WorkspaceOnly)
	}
}

func TestAgentVisionValidation(t *testing.T) {
	for _, table := range []string{
		"[agents.coder]\nvision = true\n",
		"[agents.coder]\nvision = false\n",
	} {
		if _, err := loadAgentKeys(t, table); err != nil {
			t.Fatalf("unexpected error for %q: %v", table, err)
		}
	}
	errs := agentKeyErrors(t, "[agents.coder]\nvision = \"yes\"\n")
	found := findError(errs, "agents.coder.vision")
	if found == nil || !strings.Contains(found.Msg, "boolean") {
		t.Fatalf("vision error = %+v", found)
	}
}

func TestAgentDescriptionNoUnknownKey(t *testing.T) {
	table := "[agents.coder]\ndescription = \"Checks disks\"\ndisabled_tools = [\"bash\", \"github/create_issue\"]\n"
	res, err := loadAgentKeys(t, table)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Config.Agents["coder"].Description != "Checks disks" {
		t.Fatalf("description = %q", res.Config.Agents["coder"].Description)
	}
}

func TestAgentDescriptionDisabledToolsLoaded(t *testing.T) {
	table := "[agents.coder]\ndescription = \"Checks disks\"\ndisabled_tools = [\"bash\", \"github/create_issue\"]\n"
	res, err := loadAgentKeys(t, table)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	agent, ok := res.Config.Agents["coder"]
	if !ok {
		t.Fatal("agent coder not loaded")
	}
	if agent.Description != "Checks disks" {
		t.Fatalf("description = %q", agent.Description)
	}
	want := []string{"bash", "github/create_issue"}
	if len(agent.DisabledTools) != len(want) {
		t.Fatalf("disabled_tools = %v, want %v", agent.DisabledTools, want)
	}
	for i := range want {
		if agent.DisabledTools[i] != want[i] {
			t.Fatalf("disabled_tools = %v, want %v", agent.DisabledTools, want)
		}
	}
}

// TestAgentTitleValidation covers REQ-ROOM-009 and API-ROOM-006:
// agents.<name>.title is a single line of at most 60 characters.
func TestAgentTitleValidation(t *testing.T) {
	withTitle := func(value string) string {
		return "[agents.coder]\ntitle = \"" + value + "\"\n"
	}
	tests := []struct {
		name  string
		table string
		ok    bool
		msg   string
	}{
		{name: "60 characters pass", table: withTitle(strings.Repeat("a", 60)), ok: true},
		{name: "61 characters fail", table: withTitle(strings.Repeat("a", 61)), msg: "60"},
		{name: "60 multibyte characters pass", table: withTitle(strings.Repeat("é", 60)), ok: true},
		{name: "61 multibyte characters fail", table: withTitle(strings.Repeat("é", 61)), msg: "60"},
		{name: "empty passes", table: withTitle(""), ok: true},
		{name: "newline fails", table: withTitle(`one\ntwo`), msg: "one line"},
		{name: "carriage return fails", table: withTitle(`one\rtwo`), msg: "one line"},
		{name: "non string fails", table: "[agents.coder]\ntitle = 1\n", msg: "must be string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadAgentKeys(t, tt.table)
			if tt.ok {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var errs Errors
			if !errors.As(err, &errs) {
				t.Fatalf("error is not Errors: %v", err)
			}
			found := findError(errs, "agents.coder.title")
			if found == nil {
				t.Fatalf("no error for agents.coder.title: %v", errs)
			}
			if !strings.Contains(found.Msg, tt.msg) {
				t.Fatalf("msg = %q, want it to contain %q", found.Msg, tt.msg)
			}
		})
	}
}

// TestAgentColorValidation covers REQ-ROOM-009 and API-ROOM-006:
// agents.<name>.color is one of the palette values, or empty.
func TestAgentColorValidation(t *testing.T) {
	for _, color := range []string{"brand", "success", "warning", "error", "blue", "gray", ""} {
		table := "[agents.coder]\ncolor = \"" + color + "\"\n"
		if _, err := loadAgentKeys(t, table); err != nil {
			t.Fatalf("unexpected error for %q: %v", color, err)
		}
	}
	found := findError(agentKeyErrors(t, "[agents.coder]\ncolor = \"green\"\n"), "agents.coder.color")
	if found == nil || !strings.Contains(found.Msg, "green") {
		t.Fatalf("color error = %+v", found)
	}
	if findError(agentKeyErrors(t, "[agents.coder]\ncolor = 1\n"), "agents.coder.color") == nil {
		t.Fatal("no type error for agents.coder.color")
	}
}

// TestAgentTitleColorLoaded covers API-ROOM-006: both keys load into the
// effective config.
func TestAgentTitleColorLoaded(t *testing.T) {
	res, err := loadAgentKeys(t, "[agents.coder]\ntitle = \"Finance\"\ncolor = \"success\"\n")
	if err != nil {
		t.Fatal(err)
	}
	cfg := res.Config.Agents["coder"]
	if cfg.Title != "Finance" || cfg.Color != "success" {
		t.Fatalf("title/color = %q/%q", cfg.Title, cfg.Color)
	}
}
