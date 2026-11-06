package lana

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/toolbox"
	"github.com/ridhomujizat/lana-harness/tools"
)

// The runtime side of the toolbox (spec #34, design §Tools in sessions and
// workflows): the command tools of the installed packages, the tools_request
// built-in, and the Obscura resolution the workflow browser node uses.

// toolsRequestToolName is the built-in an agent calls to ask the owner to
// install a package from the Tools page (REQ-TB-003).
const toolsRequestToolName = "tools_request"

// toolsRequestDescription is the model-facing description of tools_request.
const toolsRequestDescription = "Ask the owner to install a program from the Tools page so you can use its commands."

// toolsRequestSpec is the input schema of tools_request.
var toolsRequestSpec = json.RawMessage(`{"type":"object","properties":{"package":{"type":"string","description":"Package id as shown on the Tools page"},"reason":{"type":"string","description":"Why you need this program"}},"required":["package"],"additionalProperties":false}`)

// toolsRequestTimeout bounds how long tools_request waits for the install job
// it started (REQ-TB-003).
const toolsRequestTimeout = 10 * time.Minute

// toolsRequestPoll is how often tools_request checks the install job.
const toolsRequestPoll = time.Second

// Toolbox returns the runtime's toolbox service (design §Runtime glue). It is
// never nil after New.
func (rt *Runtime) Toolbox() *toolbox.Service { return rt.toolbox }

// toolboxRoot is the tools directory of the runtime: <LANA_HOME>/tools, or a
// shared temp dir under os.TempDir() when the runtime has no home folder
// (REQ-TB-001).
func (rt *Runtime) toolboxRoot() string {
	if rt.home != "" {
		return filepath.Join(rt.home, "tools")
	}
	return filepath.Join(os.TempDir(), "lana-tools")
}

// toolboxPathDirs returns the bin dir of every installed package whose binary
// was resolved from that bin dir, so shell commands can call it by plain name.
// A package found on the system PATH needs nothing. It is computed on every
// call, so installs and uninstalls take effect without a restart.
func (rt *Runtime) toolboxPathDirs() []string {
	if rt.toolbox == nil {
		return nil
	}
	store := rt.toolbox.Store()
	var dirs []string
	for _, pkg := range rt.toolbox.Installed() {
		bin := store.BinDir(pkg.Manifest.ID)
		if pkg.Status.BinaryPath != "" && filepath.Dir(pkg.Status.BinaryPath) == bin {
			dirs = append(dirs, bin)
		}
	}
	return dirs
}

// wireToolboxPath puts the installed packages' bin dirs on the PATH of the
// bash tool and the background jobs. Only PATH changes; the bash risk and
// approvals stay the same.
func (rt *Runtime) wireToolboxPath() {
	if rt.jobs != nil {
		rt.jobs.ExtraPath = rt.toolboxPathDirs
	}
	if rt.coreCtx == nil || rt.coreCtx.Tools == nil {
		return
	}
	if bash, ok := rt.coreCtx.Tools.Get("bash"); ok {
		if setter, ok := bash.(tools.ExtraPathSetter); ok {
			setter.SetExtraPath(rt.toolboxPathDirs)
		}
	}
}

// toolboxSecret resolves a manifest "secret:<NAME>" env value through the
// runtime's secret store (REQ-TB-007). A runtime without a store resolves
// nothing.
func (rt *Runtime) toolboxSecret(name string) (string, bool) {
	if rt.coreCtx == nil || rt.coreCtx.Secrets == nil {
		return "", false
	}
	secret, ok := rt.coreCtx.Secrets.Lookup(name)
	if !ok {
		return "", false
	}
	return secret.Reveal(), true
}

// syncToolboxTools makes the registered tools match the toolbox: one
// <pkg>_<cmd> tool per command of every usable package, plus the always-on
// tools_request tool (REQ-TB-004). It unregisters only the names this code
// registered, so built-in tools are never touched.
func (rt *Runtime) syncToolboxTools() {
	wanted := map[string]core.Tool{toolsRequestToolName: &toolsRequestTool{rt: rt}}
	for _, pkg := range rt.toolboxCommandPackages() {
		for _, cmd := range pkg.Manifest.Commands {
			name := toolbox.ToolName(pkg.Manifest.ID, cmd.Name)
			wanted[name] = &toolboxCommandTool{rt: rt, pkg: pkg.Manifest.ID, cmd: cmd}
		}
	}

	rt.toolboxMu.Lock()
	defer rt.toolboxMu.Unlock()
	for name := range rt.toolboxNames {
		if _, ok := wanted[name]; ok {
			continue
		}
		rt.unregisterTool(name)
		delete(rt.toolboxNames, name)
	}
	for name, tool := range wanted {
		if rt.toolboxNames[name] {
			continue
		}
		if err := rt.coreCtx.Tools.Register(tool); err != nil {
			slog.Warn("toolbox: register tool", "tool", name, "error", err)
			continue
		}
		rt.toolboxNames[name] = true
	}
}

// toolboxCommandPackages returns the packages whose commands become tools:
// every installed package, plus a too-old package that still runs
// (REQ-TB-004).
func (rt *Runtime) toolboxCommandPackages() []toolbox.Package {
	out := rt.toolbox.Installed()
	seen := make(map[string]bool, len(out))
	for _, pkg := range out {
		seen[pkg.Manifest.ID] = true
	}
	for _, pkg := range rt.toolbox.List(rt.ctx) {
		if pkg.Status.State == toolbox.StateTooOld && !seen[pkg.Manifest.ID] {
			out = append(out, pkg)
		}
	}
	return out
}

// unregisterTool removes one tool through the registry's Unregister when it
// offers one.
func (rt *Runtime) unregisterTool(name string) {
	if remover, ok := rt.coreCtx.Tools.(interface{ Unregister(string) bool }); ok {
		remover.Unregister(name)
	}
}

// watchToolbox refreshes the registered toolbox tools on every toolbox change
// until the runtime closes (REQ-TB-004).
func (rt *Runtime) watchToolbox() {
	defer rt.toolboxWatch.Done()
	changes, unsubscribe := rt.toolbox.Subscribe()
	defer unsubscribe()
	for {
		select {
		case <-rt.ctx.Done():
			return
		case _, ok := <-changes:
			if !ok {
				return
			}
			rt.syncToolboxTools()
		}
	}
}

// toolboxCommandTool is one command of an installed package exposed as a
// core.Tool (REQ-TB-004). Its risk comes from the manifest and its input schema
// from the command's declared fields.
type toolboxCommandTool struct {
	rt  *Runtime
	pkg string
	cmd toolbox.Command
}

func (t *toolboxCommandTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        toolbox.ToolName(t.pkg, t.cmd.Name),
		Description: t.cmd.Description + " (" + t.pkg + ")",
		InputSchema: toolbox.InputSchema(t.cmd),
	}
}

// Risk maps the manifest risk onto the core risk class (REQ-TB-004). The
// manifest validation only allows read, write and admin; anything else counts
// as a write.
func (t *toolboxCommandTool) Risk(json.RawMessage) core.Risk {
	switch t.cmd.Risk {
	case "read":
		return core.RiskRead
	case "admin":
		return core.RiskAdmin
	default:
		return core.RiskWrite
	}
}

// Call runs the command through the toolbox with the caller's workspace
// (REQ-TB-004).
func (t *toolboxCommandTool) Call(ctx context.Context, tc core.ToolCtx, input json.RawMessage) (core.ToolOutput, error) {
	values, err := decodeToolboxInput(input)
	if err != nil {
		return toolboxError("invalid input: " + err.Error()), nil
	}
	result, err := t.rt.toolbox.Run(ctx, t.pkg, t.cmd.Name, values, tc.Workspace)
	if err != nil {
		return toolboxError(err.Error()), nil
	}
	return toolboxSuccess(result.Output), nil
}

// decodeToolboxInput decodes a tool call's JSON object into the map the toolbox
// command runner takes. An empty input is an empty map.
func decodeToolboxInput(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	if values == nil {
		values = map[string]any{}
	}
	return values, nil
}

// toolsRequestTool is the built-in an agent calls to ask the owner to install a
// package (REQ-TB-003). It is always risk admin, so the permission engine
// always asks the owner, and it installs only after an approval.
type toolsRequestTool struct{ rt *Runtime }

func (t *toolsRequestTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: toolsRequestToolName, Description: toolsRequestDescription, InputSchema: toolsRequestSpec}
}

// Risk is always admin: tools_request needs a fresh owner approval and no
// workflow lock may auto-allow it (REQ-TB-003).
func (t *toolsRequestTool) Risk(json.RawMessage) core.Risk { return core.RiskAdmin }

func (t *toolsRequestTool) Call(ctx context.Context, _ core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		Package string `json:"package"`
		Reason  string `json:"reason"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return toolboxError("invalid input: " + err.Error()), nil
		}
	}
	id := strings.TrimSpace(in.Package)
	if id == "" {
		return toolboxError("invalid input: package is required"), nil
	}

	pkg, err := t.rt.toolbox.Get(ctx, id)
	if err != nil {
		if errors.Is(err, toolbox.ErrNotFound) {
			return toolboxError(fmt.Sprintf("package %s is not on the Tools page; ask the owner to add it there", id)), nil
		}
		return toolboxError(err.Error()), nil
	}
	if pkg.Status.State == toolbox.StateInstalled {
		return toolboxSuccess("already installed: " + toolboxToolNames(pkg)), nil
	}

	job, err := t.rt.toolbox.Install(ctx, id, 0)
	if err != nil {
		return toolboxError(err.Error()), nil
	}
	if err := t.waitInstall(ctx, id, job.ID); err != nil {
		return toolboxError(err.Error()), nil
	}
	pkg, err = t.rt.toolbox.Get(ctx, id)
	if err != nil {
		return toolboxError(err.Error()), nil
	}
	if pkg.Status.State != toolbox.StateInstalled {
		if pkg.Status.Error != "" {
			return toolboxError(pkg.Status.Error), nil
		}
		return toolboxError("install did not complete: " + pkg.Status.State), nil
	}
	return toolboxSuccess("installed: " + toolboxToolNames(pkg)), nil
}

// waitInstall waits for an install job to end, polling the package's jobs every
// second for up to toolsRequestTimeout or until ctx ends (REQ-TB-003).
func (t *toolsRequestTool) waitInstall(ctx context.Context, id, jobID string) error {
	deadline := time.Now().Add(toolsRequestTimeout)
	ticker := time.NewTicker(toolsRequestPoll)
	defer ticker.Stop()
	for {
		if job, ok := findToolboxJob(t.rt.toolbox.Jobs(id), jobID); ok {
			switch job.Status {
			case toolbox.JobSucceeded:
				return nil
			case toolbox.JobFailed, toolbox.JobCancelled:
				if job.Error != "" {
					return errors.New(job.Error)
				}
				return fmt.Errorf("install %s", job.Status)
			}
		}
		if !time.Now().Before(deadline) {
			return errors.New("install timed out")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// findToolboxJob returns the named job of a package.
func findToolboxJob(jobs []toolbox.Job, id string) (toolbox.Job, bool) {
	for _, job := range jobs {
		if job.ID == id {
			return job, true
		}
	}
	return toolbox.Job{}, false
}

// toolboxToolNames lists the tool names a package registers.
func toolboxToolNames(pkg toolbox.Package) string {
	names := make([]string, 0, len(pkg.Manifest.Commands))
	for _, cmd := range pkg.Manifest.Commands {
		names = append(names, toolbox.ToolName(pkg.Manifest.ID, cmd.Name))
	}
	return strings.Join(names, ", ")
}

// toolboxSuccess is the ok result of a toolbox tool call.
func toolboxSuccess(output string) core.ToolOutput {
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: output}
}

// toolboxError is the failed result of a toolbox tool call.
func toolboxError(output string) core.ToolOutput {
	return core.ToolOutput{Status: core.ToolStatusError, Output: output}
}
