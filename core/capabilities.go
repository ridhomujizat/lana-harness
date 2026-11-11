package core

import (
	"sort"
	"strings"
)

// AgentCapabilities names what an agent can do, in words the team lead and
// the room show: "Claude Code", "web", "files", "shell", "knowledge",
// "calendar", and each MCP server it uses (spec 47 team work). The order is
// stable: runtime first, then built-in groups, then MCP servers by name.
func AgentCapabilities(cfg AgentConfig) []string {
	var out []string
	if strings.HasPrefix(cfg.Model, "claude-code/") || cfg.Runtime == "claude-code" {
		out = append(out, "Claude Code")
	}
	has := map[string]bool{}
	for _, tool := range cfg.Tools {
		has[tool] = true
	}
	for _, tool := range cfg.DisabledTools {
		delete(has, tool)
	}
	group := func(word string, tools ...string) {
		for _, t := range tools {
			if has[t] {
				out = append(out, word)
				return
			}
		}
	}
	group("web", "web_fetch", "web_search", "browser")
	group("files", "write_file", "edit_file", "read_file")
	group("shell", "bash")
	group("knowledge", "knowledge_search", "knowledge_read", "knowledge_write")
	group("calendar", "calendar")
	group("workflows", "workflow")
	mcp := append([]string(nil), cfg.MCP...)
	sort.Strings(mcp)
	for _, name := range mcp {
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}
