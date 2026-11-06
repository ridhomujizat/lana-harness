package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/mcp"
	"github.com/ridhomujizat/lana-harness/tools"
)

func cmdMCP(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: use lana mcp list|tools <server>")
		return 2
	}
	switch args[0] {
	case "list":
		return mcpList()
	case "tools":
		return mcpTools(args[1:])
	}
	fmt.Fprintf(os.Stderr, "error: unknown mcp command %q (use list, tools)\n", args[0])
	return 2
}

// mcpList prints the configured servers and the agents that use them.
func mcpList() int {
	_, cfg, code := loadConfigForAgent()
	if code != 0 {
		return code
	}
	if len(cfg.MCP) == 0 {
		fmt.Println("no MCP servers configured")
		return 0
	}
	names := make([]string, 0, len(cfg.MCP))
	for name := range cfg.MCP {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		server := cfg.MCP[name]
		kind, target := "stdio", server.Command
		if server.URL != "" {
			kind, target = "http", server.URL
		} else if len(server.Args) > 0 {
			target = strings.Join(append([]string{server.Command}, server.Args...), " ")
		}
		agents := make([]string, 0)
		for _, agentName := range agentNames(cfg) {
			if containsString(cfg.Agents[agentName].MCP, name) {
				agents = append(agents, agentName)
			}
		}
		agentList := "-"
		if len(agents) > 0 {
			agentList = strings.Join(agents, ",")
		}
		fmt.Printf("%s  %s  %s  agents=%s\n", name, kind, target, agentList)
	}
	return 0
}

// mcpTools connects one server, lists its tools, and closes it again.
func mcpTools(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "error: use lana mcp tools <server>")
		return 2
	}
	name := args[0]
	home, cfg, code := loadConfigForAgent()
	if code != 0 {
		return code
	}
	serverCfg, ok := cfg.MCP[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "error: unknown MCP server %q\n", name)
		return 2
	}

	registry := tools.NewRegistry()
	manager := mcp.NewManager(map[string]core.MCPConfig{name: serverCfg}, home, registry)
	defer func() { _ = manager.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	names, unavailable := manager.Ensure(ctx, []string{name})
	if reason, ok := unavailable[name]; ok {
		fmt.Fprintf(os.Stderr, "error: mcp %s: %s\n", name, reason)
		return 1
	}
	sort.Strings(names)
	for _, toolName := range names {
		tool, ok := registry.Get(toolName)
		if !ok {
			continue
		}
		fmt.Printf("%s  %s  %s\n", toolName, tool.Risk(nil), truncateRunes(tool.Spec().Description, 80))
	}
	return 0
}

// containsString reports whether values contains want.
func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// truncateRunes cuts s to at most limit runes.
func truncateRunes(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit])
}
