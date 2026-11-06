// Package toolfmt formats tool calls for display in channels.
package toolfmt

import (
	"encoding/json"
	"strconv"
	"strings"
)

// PrimaryArg returns the main argument of a tool call on one line (API-CLI-003),
// or "". An ask_user call returns its question count, for example
// "2 questions", instead of its first question (REQ-ASK-020).
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
	case "ask_user":
		if count := questionCount(value); count == 1 {
			raw = "1 question"
		} else if count > 1 {
			raw = strconv.Itoa(count) + " questions"
		}
	default:
		if len(input) > 0 {
			raw = string(input)
		}
	}
	return strings.Join(strings.Fields(raw), " ")
}

// questionCount returns the number of questions in an ask_user input. An
// unparseable input or one without a questions array has 0 questions.
func questionCount(value map[string]any) int {
	questions, _ := value["questions"].([]any)
	return len(questions)
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
