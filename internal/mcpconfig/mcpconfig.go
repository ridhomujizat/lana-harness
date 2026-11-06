// Package mcpconfig parses and renders the MCP part of config.toml. The web
// handlers and the agent MCP tool share these helpers so the file format has a
// single implementation.
package mcpconfig

import (
	"bytes"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// NameRE is the MCP server name rule of API-WEB-027 and API-AMCP-001.
var NameRE = regexp.MustCompile(`^[a-z0-9-]+$`)

// Definition is the definition of one MCP server. Env and Header values are
// inline; the web handlers never write or return them.
type Definition struct {
	Type    string
	Command string
	Args    []string
	URL     string
	Env     map[string]string
	Headers map[string]string
}

// Document is the part of config.toml the MCP handlers read.
type Document struct {
	MCP    map[string]map[string]any `toml:"mcp"`
	Agents map[string]map[string]any `toml:"agents"`
}

// Parse returns the MCP and agents tables of a config.toml document. Empty or
// whitespace-only data is a valid empty document.
func Parse(raw []byte) (Document, error) {
	document := Document{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return document, nil
	}
	if err := toml.Unmarshal(raw, &document); err != nil {
		return Document{}, err
	}
	return document, nil
}

// FromMap builds a definition from one parsed [mcp.<name>] table.
func FromMap(table map[string]any) Definition {
	def := Definition{
		Command: mapString(table, "command"),
		Args:    stringList(table["args"]),
		URL:     mapString(table, "url"),
		Env:     stringMap(table["env"]),
		Headers: stringMap(table["headers"]),
	}
	def.Type = definitionType(def)
	return def
}

// Normalize drops the fields that do not belong to the definition's type.
func Normalize(def *Definition) {
	if def.Type == "command" {
		def.URL = ""
		def.Headers = nil
		return
	}
	def.Command = ""
	def.Args = nil
	def.Env = nil
}

// AgentList returns the mcp list of agent, or nil when the agent has no list.
func AgentList(doc Document, agent string) []string {
	table, ok := doc.Agents[agent]
	if !ok {
		return nil
	}
	return stringList(table["mcp"])
}

// Usage maps each server name to the sorted agents that list it.
func Usage(doc Document) map[string][]string {
	usage := map[string][]string{}
	names := make([]string, 0, len(doc.Agents))
	for name := range doc.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, server := range AgentList(doc, name) {
			usage[server] = append(usage[server], name)
		}
	}
	return usage
}

// RedactValues replaces every inline value in message with "***". It is a last
// line of defense for an error that echoes an env or header value.
func RedactValues(message string, values ...map[string]string) string {
	for _, table := range values {
		for _, value := range table {
			if value == "" || value == "***" {
				continue
			}
			message = strings.ReplaceAll(message, value, "***")
		}
	}
	return message
}

// mapString returns the string value of key, or "" when the value is missing or
// is not a string.
func mapString(table map[string]any, key string) string {
	value, _ := table[key].(string)
	return value
}

// stringList returns a copy of a TOML string array.
func stringList(value any) []string {
	switch list := value.(type) {
	case []string:
		return append([]string(nil), list...)
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

// stringMap returns the string entries of a TOML table, or nil when there are
// none.
func stringMap(value any) map[string]string {
	table, ok := value.(map[string]any)
	if !ok || len(table) == 0 {
		return nil
	}
	out := make(map[string]string, len(table))
	for key, item := range table {
		if text, ok := item.(string); ok {
			out[key] = text
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// definitionType returns "url" for an HTTP server and "command" otherwise.
func definitionType(def Definition) string {
	if def.URL != "" {
		return "url"
	}
	return "command"
}
