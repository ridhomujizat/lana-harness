package agent

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
)

// deferThreshold is the number of deferrable tools above which a native turn
// defers them when the agent's defer_tools is absent (API-DTL-001).
const deferThreshold = 15

// planDeferral splits the offered tools of a native turn (REQ-DTL-001): base is
// the tool list the next step sends, deferrable is the set withheld until a
// load. deferTools is the agent's defer_tools value, nil when absent;
// serverDeferrable reports whether an MCP server's tools are deferrable
// (API-DTL-002); toolSearchOK reports whether tool_search may be offered this
// turn (REQ-DTL-004). Deferral is off when deferTools is false or when there is
// no deferrable tool: base is offered unchanged and deferrable is nil. When on,
// base is offered without the deferrable names plus tool_search, sorted by
// name, and deferrable is sorted.
func planDeferral(offered []string, deferTools *bool, serverDeferrable func(server string) bool, toolSearchOK bool) (base, deferrable []string) {
	for _, name := range offered {
		server, ok := mcpServerOf(name)
		if !ok || serverDeferrable == nil || !serverDeferrable(server) {
			continue
		}
		deferrable = append(deferrable, name)
	}
	if len(deferrable) == 0 || (deferTools != nil && !*deferTools) {
		return offered, nil
	}
	if deferTools == nil && len(deferrable) <= deferThreshold {
		return offered, nil
	}
	sort.Strings(deferrable)
	withheld := make(map[string]bool, len(deferrable))
	for _, name := range deferrable {
		withheld[name] = true
	}
	base = make([]string, 0, len(offered)-len(deferrable)+1)
	for _, name := range offered {
		if withheld[name] {
			continue
		}
		base = append(base, name)
	}
	if toolSearchOK && !containsString(base, "tool_search") {
		base = append(base, "tool_search")
	}
	sort.Strings(base)
	return base, deferrable
}

// mcpServerOf returns the "<server>" part of a model-facing MCP tool name
// mcp__<server>__<tool>. ok is false for any other name.
func mcpServerOf(name string) (string, bool) {
	ref, ok := mcpToolRef(name)
	if !ok {
		return "", false
	}
	server, _, _ := strings.Cut(ref, "/")
	return server, true
}

// loadedFromEvents returns the deferred tools a session chain has loaded since
// its latest compaction summary, in first-load order and without duplicates,
// keeping only names still deferrable (REQ-DTL-006). A chain without a summary
// counts from its start. Events that fail to unmarshal, and names that are not
// deferrable, are dropped.
func loadedFromEvents(events []core.SessionEvent, deferrable []string) []string {
	compaction := -1
	for i := range events {
		if events[i].Kind == core.KindCompactionSummary {
			compaction = i
		}
	}
	allowed := make(map[string]bool, len(deferrable))
	for _, name := range deferrable {
		allowed[name] = true
	}
	var out []string
	seen := map[string]bool{}
	for i := compaction + 1; i < len(events); i++ {
		if events[i].Kind != core.KindToolsLoaded {
			continue
		}
		var payload core.ToolsLoaded
		if err := json.Unmarshal(events[i].Payload, &payload); err != nil {
			continue
		}
		for _, name := range payload.Names {
			if !allowed[name] || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// toolSearchOK reports whether a native turn may offer tool_search: the tool is
// registered and the agent did not list it in disabled_tools (REQ-DTL-004).
func (rn *Runner) toolSearchOK(resolved resolvedAgent) bool {
	if _, ok := rn.reg.tools.Get("tool_search"); !ok {
		return false
	}
	return !containsString(resolved.disabled, "tool_search")
}

// mcpServerDeferrable reports whether the named MCP server's tools are
// deferrable: [mcp.<server>] defer nil or true defers them, false never does
// (API-DTL-002). A server the registry never applied counts as deferrable.
func (r *Registry) mcpServerDeferrable(server string) bool {
	r.mcpMu.Lock()
	cfg, ok := r.mcpApplied[server]
	r.mcpMu.Unlock()
	if !ok {
		return true
	}
	return cfg.Defer == nil || *cfg.Defer
}

// deferredSpecs returns the tool declaration of every deferrable tool, for
// tool_search (REQ-DTL-004). A name the registry does not know is skipped.
func (rn *Runner) deferredSpecs(names []string) []core.ToolSpec {
	specs := make([]core.ToolSpec, 0, len(names))
	for _, name := range names {
		if tool, ok := rn.reg.tools.Get(name); ok {
			specs = append(specs, tool.Spec())
		}
	}
	return specs
}

// enabledTools is the tool list a step's calls run against (REQ-DTL-005): the
// step's tools plus every deferrable tool not loaded yet, so a direct call to a
// deferred tool passes the offered check, name repair, and input validation.
func enabledTools(turnTools, deferrable []string) []string {
	enabled := make(map[string]bool, len(turnTools))
	for _, name := range turnTools {
		enabled[name] = true
	}
	out := append([]string(nil), turnTools...)
	for _, name := range deferrable {
		if !enabled[name] {
			out = append(out, name)
		}
	}
	return out
}

// callFinalName is the name a call actually ran as: its own name when it is
// enabled, else the repaired name (REQ-DTL-005). It is "" when repair found no
// single match.
func callFinalName(call core.ToolCall, enabled []string) string {
	if containsString(enabled, call.Name) {
		return call.Name
	}
	return repairToolName(call.Name, enabled)
}

// loadedThisStep returns the deferrable tools a step's outputs loaded, in load
// order (REQ-DTL-002, REQ-DTL-005): first the names a result reported
// (tool_search), then the final name of every call to a deferrable tool. Names
// already in loaded, duplicates, and non-deferrable names are dropped.
func loadedThisStep(calls []core.ToolCall, outputs []core.ToolOutput, enabled, deferrable, loaded []string) []string {
	allowed := make(map[string]bool, len(deferrable))
	for _, name := range deferrable {
		allowed[name] = true
	}
	seen := make(map[string]bool, len(loaded)+len(outputs))
	for _, name := range loaded {
		seen[name] = true
	}
	var out []string
	add := func(name string) {
		if name == "" || seen[name] || !allowed[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, output := range outputs {
		for _, name := range output.Loaded {
			add(name)
		}
	}
	for i := range calls {
		add(callFinalName(calls[i], enabled))
	}
	return out
}

// deferInvalidInput appends the tool's description and JSON input schema to the
// "invalid input:" error of a call to a not-yet-loaded deferred tool
// (REQ-DTL-005). It returns the augmented output text, or "" when the output is
// not such an error.
func (rn *Runner) deferInvalidInput(call core.ToolCall, out core.ToolOutput, enabled, deferrable, loaded []string) string {
	if out.Status != core.ToolStatusError || !strings.HasPrefix(out.Output, "invalid input:") {
		return ""
	}
	name := callFinalName(call, enabled)
	if !containsString(deferrable, name) || containsString(loaded, name) {
		return ""
	}
	tool, ok := rn.reg.tools.Get(name)
	if !ok {
		return ""
	}
	spec := tool.Spec()
	schema, err := json.Marshal(spec.InputSchema)
	if err != nil {
		return ""
	}
	return out.Output + "\n\n" + spec.Description + "\nInput schema: " + string(schema)
}
