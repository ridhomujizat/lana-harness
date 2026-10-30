package core

import "context"

// MCPServerStatus is the state of one MCP server.
type MCPServerStatus struct {
	Name   string
	State  string // State: "running", "stopped", "unavailable"
	Reason string
	Tools  int
	// RunningForMS is how long the running client has been connected. It is
	// zero unless State is "running".
	RunningForMS int64
}

// MCPManager connects MCP servers and exposes their tools.
type MCPManager interface {
	// Ensure connects the servers that are not running and returns the names of their tools
	// (mcp__<server>__<tool>) and the servers that are unavailable with a short reason.
	Ensure(ctx context.Context, servers []string) (tools []string, unavailable map[string]string)
	Status(servers []string) []MCPServerStatus
}

// MCPAdmin changes MCP servers while the runtime runs.
type MCPAdmin interface {
	TestServer(ctx context.Context, cfg MCPConfig) (tools int, err error)
	PutServer(name string, cfg MCPConfig)
	RemoveServer(name string)
	SetAgentServers(agent string, servers []string)
}
