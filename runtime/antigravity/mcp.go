package antigravity

import (
	"encoding/json"
	"sort"
	"strings"
)

// acpMCPServer is one entry of the mcpServers array session/new and session/load
// take (API-AGY-011).
type acpMCPServer struct {
	Type    string      `json:"type"`
	Name    string      `json:"name"`
	URL     string      `json:"url"`
	Headers []acpHeader `json:"headers,omitempty"`
}

// acpHeader is one header of an ACP MCP server.
type acpHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// convertMCP turns RuntimeTurn.OpenMCP's Claude-shaped JSON
// ({"mcpServers":{"lana":{"type":"http","url":…,"headers":{…}}}}) into the ACP
// mcpServers array: a list of servers whose headers are name/value pairs. It
// returns nil for an empty or unreadable config; the caller then sends [].
func convertMCP(config string) []acpMCPServer {
	if strings.TrimSpace(config) == "" {
		return nil
	}
	var c struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal([]byte(config), &c) != nil {
		return nil
	}
	names := make([]string, 0, len(c.MCPServers))
	for name := range c.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]acpMCPServer, 0, len(names))
	for _, name := range names {
		s := c.MCPServers[name]
		keys := make([]string, 0, len(s.Headers))
		for k := range s.Headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		headers := make([]acpHeader, 0, len(keys))
		for _, k := range keys {
			headers = append(headers, acpHeader{Name: k, Value: s.Headers[k]})
		}
		serverType := s.Type
		if serverType == "" {
			serverType = "http"
		}
		out = append(out, acpMCPServer{Type: serverType, Name: name, URL: s.URL, Headers: headers})
	}
	return out
}

// mcpServersParam returns the mcpServers value of session/new and session/load:
// the servers, or an empty array (never null).
func mcpServersParam(servers []acpMCPServer) []acpMCPServer {
	if servers == nil {
		return []acpMCPServer{}
	}
	return servers
}
