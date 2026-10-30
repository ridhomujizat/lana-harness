package mcpconfig

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Render returns the [mcp.<name>] table for def in the same format as the web
// MCP save handler writes.
func Render(name string, def Definition) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[mcp.%s]\n", tomlKey(name))
	writeString(&b, "command", def.Command)
	if len(def.Args) > 0 {
		fmt.Fprintf(&b, "args = %s\n", RenderList(def.Args))
	}
	writeString(&b, "url", def.URL)
	writeValueTable(&b, "env", def.Env)
	writeValueTable(&b, "headers", def.Headers)
	return b.String()
}

// RenderList returns values as a TOML string array.
func RenderList(values []string) string {
	data, err := toml.Marshal(values)
	if err != nil {
		return "[]"
	}
	return strings.TrimSuffix(string(data), "\n")
}

func writeValueTable(b *strings.Builder, name string, values map[string]string) {
	if len(values) == 0 {
		return
	}
	keys := SortedKeys(values)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, tomlKey(key)+" = "+tomlString(values[key]))
	}
	fmt.Fprintf(b, "%s = { %s }\n", name, strings.Join(parts, ", "))
}

func writeString(b *strings.Builder, key, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(b, "%s = %s\n", key, tomlString(value))
}

// SortedKeys returns the sorted key names of values, or an empty slice for an
// empty map.
func SortedKeys(values map[string]string) []string {
	if len(values) == 0 {
		return []string{}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// tomlKey writes a bare key when it can, otherwise a quoted key.
func tomlKey(key string) string {
	bare := key != ""
	for i := 0; i < len(key); i++ {
		if !isBareKeyChar(key[i]) {
			bare = false
			break
		}
	}
	if bare {
		return key
	}
	data, err := toml.Marshal(key)
	if err != nil {
		return `""`
	}
	return strings.TrimSuffix(string(data), "\n")
}

func isBareKeyChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

func tomlString(value string) string {
	data, err := toml.Marshal(value)
	if err != nil {
		return `""`
	}
	return strings.TrimSuffix(string(data), "\n")
}
