package lana

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/toolbox"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// toolboxManifest is a package whose detect binary is the shell script
// newToolboxRuntime writes into tools/fakey/bin (REQ-TB-002, REQ-TB-004).
const toolboxManifest = `
id = "fakey"
name = "Fakey"
description = "Test package"

[detect]
binary = "fakey"
version_args = ["--version"]

[[install]]
label = "test"
command = "true"

[[commands]]
name = "echo"
description = "Echo the input"
args = ["echo", "{{text}}"]
risk = "read"
output = "text"
workdir = "data"

[commands.input]
text = { type = "string", required = true }
`

// fakeBinary prints a version and, for the echo subcommand, prints its
// arguments (REQ-TB-004).
const fakeBinary = `#!/bin/sh
case "$1" in
--version) echo "1.2.3" ;;
echo) shift; echo "$@" ;;
esac
`

// newToolboxRuntime builds a runtime over a fresh home that already holds the
// fakey package and its binary, so New registers fakey_echo (REQ-TB-004).
func newToolboxRuntime(t *testing.T) *Runtime {
	t.Helper()
	home := t.TempDir()
	binDir := filepath.Join(home, "tools", "fakey", "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "fakey"), []byte(fakeBinary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "tools", "fakey", "tool.toml"), []byte(toolboxManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := `
[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.assistant]
provider = "mock"
model = "m"
`
	rt, err := New(context.Background(), Options{
		Home:      home,
		Layers:    []config.Layer{{Name: "test", TOML: []byte(cfg)}},
		Providers: map[string]core.Provider{"mock": mock.New(mock.Script{})},
	})
	if err != nil {
		t.Fatalf("lana.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

// REQ-TB-004: an installed package's command is registered after New, runs, and
// is unregistered when the package is removed.
func TestToolboxCommandTool(t *testing.T) {
	rt := newToolboxRuntime(t)
	tool, ok := rt.coreCtx.Tools.Get("fakey_echo")
	if !ok {
		t.Fatal("fakey_echo is not registered after New")
	}
	if tool.Risk(nil) != core.RiskRead {
		t.Fatalf("risk=%q, want read", tool.Risk(nil))
	}
	spec := tool.Spec()
	if spec.Name != "fakey_echo" || !strings.Contains(spec.Description, "fakey") {
		t.Fatalf("spec=%+v", spec)
	}

	out, err := tool.Call(context.Background(), core.ToolCtx{Workspace: t.TempDir()},
		json.RawMessage(`{"text":"hello world"}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if out.Status != core.ToolStatusSuccess || !strings.Contains(out.Output, "hello world") {
		t.Fatalf("out=%+v", out)
	}

	if err := rt.toolbox.Delete(context.Background(), "fakey", true); err != nil {
		t.Fatalf("delete: %v", err)
	}
	rt.syncToolboxTools()
	if _, ok := rt.coreCtx.Tools.Get("fakey_echo"); ok {
		t.Fatal("fakey_echo is still registered after uninstall")
	}
}

// REQ-TB-003: tools_request is always registered with admin risk, names the
// Tools page for an unknown package, and reports an installed package.
func TestToolsRequestTool(t *testing.T) {
	rt := newToolboxRuntime(t)
	tool, ok := rt.coreCtx.Tools.Get(toolsRequestToolName)
	if !ok {
		t.Fatal("tools_request is not registered")
	}
	if tool.Risk(nil) != core.RiskAdmin {
		t.Fatalf("tools_request risk=%q, want admin", tool.Risk(nil))
	}

	out, err := tool.Call(context.Background(), core.ToolCtx{}, json.RawMessage(`{"package":"ghost"}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if out.Status != core.ToolStatusError || !strings.Contains(out.Output, "Tools page") {
		t.Fatalf("unknown package out=%+v", out)
	}

	out, err = tool.Call(context.Background(), core.ToolCtx{}, json.RawMessage(`{"package":"fakey"}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if out.Status != core.ToolStatusSuccess || !strings.Contains(out.Output, "fakey_echo") {
		t.Fatalf("installed package out=%+v", out)
	}
}

// REQ-TB-005: the catalog lists the installed commands in an "installed" group
// after the built-in groups.
func TestToolboxCatalogGroup(t *testing.T) {
	rt := newToolboxRuntime(t)
	groups, err := rt.ToolCatalog("assistant")
	if err != nil {
		t.Fatalf("ToolCatalog: %v", err)
	}
	index := -1
	for i := range groups {
		if groups[i].ID == "installed" {
			index = i
		}
	}
	if index < 0 {
		t.Fatalf("no installed group in %v", catalogGroupIDs(groups))
	}
	if groups[index].Label != "Installed tools" {
		t.Fatalf("label=%q", groups[index].Label)
	}
	if index == 0 || groups[index-1].ID != "chat" {
		t.Fatalf("installed group is not after the built-in groups: %v", catalogGroupIDs(groups))
	}

	found := false
	for _, entry := range groups[index].Tools {
		if entry.Name != "fakey_echo" {
			continue
		}
		found = true
		if entry.Risk != "read" || entry.Enabled || entry.Source != "not_listed" {
			t.Fatalf("fakey_echo entry=%+v", entry)
		}
	}
	if !found {
		t.Fatalf("fakey_echo is not in the installed group: %+v", groups[index].Tools)
	}
}

// catalogGroupIDs returns the group ids for a failure message.
func catalogGroupIDs(groups []ToolGroup) []string {
	ids := make([]string, 0, len(groups))
	for _, group := range groups {
		ids = append(ids, group.ID)
	}
	return ids
}

// REQ-TB-006: Browse asks for the Tools page when no package provides obscura
// and obscura is not on PATH.
func TestToolboxBrowseMissing(t *testing.T) {
	rt := newToolboxRuntime(t)
	t.Setenv("PATH", t.TempDir())
	_, err := rt.WorkflowDeps().Browse(context.Background(), workflow.BrowseRequest{
		WorkflowID: "wf1", URL: "https://example.com",
	})
	if err == nil || !strings.Contains(err.Error(), "Obscura is not installed. Add it on the Tools page.") {
		t.Fatalf("Browse error=%v", err)
	}
}

// REQ-TB-004: syncToolboxTools registers the command of an installed package
// and leaves built-in tools alone.
func TestToolboxSyncKeepsBuiltins(t *testing.T) {
	rt := newToolboxRuntime(t)
	rt.syncToolboxTools()
	if _, ok := rt.coreCtx.Tools.Get("read_file"); !ok {
		t.Fatal("read_file was removed by the toolbox sync")
	}
	pkg, err := rt.toolbox.Get(context.Background(), "fakey")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if pkg.Status.State != toolbox.StateInstalled {
		t.Fatalf("status=%+v, want installed", pkg.Status)
	}
}

// An installed package's own bin dir is on PATH for the agent bash tool, the
// background jobs and the workflow bash node; after uninstall it is not
// (Tools page installs callable by plain name).
func TestToolboxBinariesOnBashPath(t *testing.T) {
	rt := newToolboxRuntime(t)
	ctx := context.Background()
	bash, ok := rt.coreCtx.Tools.Get("bash")
	if !ok {
		t.Fatal("bash tool is not registered")
	}
	ws := t.TempDir()
	out, err := bash.Call(ctx, core.ToolCtx{Workspace: ws}, json.RawMessage(`{"command":"fakey --version"}`))
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	if out.Status != core.ToolStatusSuccess || !strings.Contains(out.Output, "1.2.3") {
		t.Fatalf("bash out=%+v", out)
	}

	result, err := rt.WorkflowDeps().Bash(ctx, workflow.BashRequest{WorkflowID: "wf-path", Command: "fakey --version"})
	if err != nil {
		t.Fatalf("workflow bash: %v", err)
	}
	if result.ExitCode != 0 || !strings.Contains(result.Output, "1.2.3") {
		t.Fatalf("workflow bash result=%+v", result)
	}

	job, err := rt.jobs.Start("s-path", ws, "fakey --version")
	if err != nil {
		t.Fatalf("job start: %v", err)
	}
	waited := rt.jobs.Wait(ctx, "s-path", job.ID, 5*time.Second)
	tail, _ := rt.jobs.Tail("s-path", job.ID, 4096)
	if waited.ExitCode != 0 || !strings.Contains(tail, "1.2.3") {
		t.Fatalf("job=%+v tail=%q", waited, tail)
	}

	if err := rt.toolbox.Delete(ctx, "fakey", true); err != nil {
		t.Fatalf("delete: %v", err)
	}
	out, err = bash.Call(ctx, core.ToolCtx{Workspace: ws}, json.RawMessage(`{"command":"fakey --version"}`))
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	if out.Status != core.ToolStatusError {
		t.Fatalf("fakey still runs after uninstall: %+v", out)
	}
}
