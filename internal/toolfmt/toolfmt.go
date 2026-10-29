// Package toolfmt formats tool calls for display in channels.
package toolfmt

import (
	"encoding/json"
	"strings"
)

// PrimaryArg returns the main argument of a tool call on one line (API-CLI-003), or "".
func PrimaryArg(name string, input json.RawMessage) string {
	var value map[string]any
	_ = json.Unmarshal(input, &value)
	get := func(keys ...string) string {
		for _, key := range keys {
			if raw, ok := value[key]; ok {
				if text, ok := raw.(string); ok && text != "" {
					return text
				}
			}
		}
		return ""
	}
	var raw string
	switch name {
	case "bash":
		raw = get("command")
	case "read_file", "write_file", "edit_file":
		raw = get("path", "file_path")
	case "glob", "grep":
		raw = get("pattern")
	case "web_fetch":
		raw = get("url")
	case "task":
		agent, title := get("agent"), get("title")
		raw = strings.TrimSpace(agent + " · " + title)
	case "memory_read":
		raw = get("target")
	case "memory_write":
		raw = strings.TrimSpace(get("action") + " " + get("target"))
	default:
		if len(input) > 0 {
			raw = string(input)
		}
	}
	return strings.Join(strings.Fields(raw), " ")
}

// Cut shortens s to at most n runes, ending with "…" when cut.
func Cut(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	return string(runes[:n-1]) + "…"
}
