package workflow

import "sort"

// Lock risk levels (contract `lock.tools[].risk`).
const (
	RiskRead  = "read"
	RiskWrite = "write"
	RiskAdmin = "admin"
)

// ComputeLock builds the tool list a graph needs (REQ-WF-009): one row per
// distinct tool, with the node names that use it in graph order, sorted by tool
// name. risk names the risk of a tool the catalog does not know; a nil function
// or an empty result is treated as "write". The result is never nil.
func ComputeLock(g Graph, risk func(tool string) string) []LockTool {
	used := map[string][]string{}
	for _, n := range g.Nodes {
		for _, tool := range LockToolsOf(n) {
			if !containsString(used[tool], n.Name) {
				used[tool] = append(used[tool], n.Name)
			}
		}
	}
	tools := make([]LockTool, 0, len(used))
	for tool, names := range used {
		tools = append(tools, LockTool{Tool: tool, UsedBy: names, Risk: lockRisk(tool, risk)})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Tool < tools[j].Tool })
	return tools
}

// LockCovers reports whether lock approves every tool the graph needs
// (REQ-WF-009). A nil lock never covers.
func LockCovers(lock *Lock, g Graph) bool {
	if lock == nil {
		return false
	}
	have := LockSet(lock)
	for _, t := range ComputeLock(g, nil) {
		if !have[t.Tool] {
			return false
		}
	}
	return true
}

// LockSet returns the locked tool names as a set. A nil lock yields an empty
// (non-nil) map.
func LockSet(lock *Lock) map[string]bool {
	set := map[string]bool{}
	if lock == nil {
		return set
	}
	for _, t := range lock.Tools {
		set[t.Tool] = true
	}
	return set
}

// lockRisk returns the risk of one tool: browser and webfetch read, the
// workflow write tools write, bash admin, and anything else the risk function's
// answer, defaulting to write.
func lockRisk(tool string, risk func(tool string) string) string {
	switch tool {
	case toolBrowser, toolWebfetch:
		return RiskRead
	case toolMessage, toolKnowledge, toolStateSet:
		return RiskWrite
	case toolBash:
		return RiskAdmin
	}
	if risk != nil {
		if r := risk(tool); r != "" {
			return r
		}
	}
	return RiskWrite
}

// containsString reports whether list holds s.
func containsString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}
