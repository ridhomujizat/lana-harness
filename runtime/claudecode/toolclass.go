package claudecode

import (
	"encoding/json"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
)

// ToolRisk maps a Claude Code tool name to a lana risk class (ADR 0018).
// Unknown tools and MCP tools are exec, the strictest class that still allows
// an approval.
func ToolRisk(name string) core.Risk {
	switch name {
	case "Read", "Glob", "Grep", "LS", "NotebookRead":
		return core.RiskRead
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		return core.RiskWrite
	case "Bash", "BashOutput", "KillShell":
		return core.RiskExec
	case "WebFetch", "WebSearch":
		return core.RiskNetwork
	}
	return core.RiskExec
}

// RuleString renders the rule-match string of a Claude tool call:
// "bash(<command>)" for Bash and "<lowercased tool>(<path or url>)" for the
// others. A tool without a path, URL, or command renders as the lowercased
// name alone.
func RuleString(name string, input json.RawMessage) string {
	tool := strings.ToLower(name)
	arg := ruleArg(name, decodeInput(input))
	if arg == "" {
		return tool
	}
	return tool + "(" + arg + ")"
}

// PermCall returns the lana tool name and input that lana's permission engine
// should match for a Claude tool call, so rules written for native tools apply:
// Bash → bash{command}, Read → read_file{path}, Write → write_file{path},
// Edit/MultiEdit → edit_file{path}, Glob/LS → glob{path}, Grep → grep{path},
// WebFetch → web_fetch{url}. Other tools keep their lowercased name and input.
func PermCall(name string, input json.RawMessage) (string, json.RawMessage) {
	value := decodeInput(input)
	str := func(key string) string {
		s, _ := value[key].(string)
		return s
	}
	var tool string
	var mapped map[string]any
	switch name {
	case "Bash":
		tool, mapped = "bash", map[string]any{"command": str("command")}
	case "Read", "NotebookRead":
		tool, mapped = "read_file", map[string]any{"path": firstNonEmpty(str("file_path"), str("notebook_path"))}
	case "Write":
		tool, mapped = "write_file", map[string]any{"path": str("file_path")}
	case "Edit", "MultiEdit", "NotebookEdit":
		tool, mapped = "edit_file", map[string]any{"path": firstNonEmpty(str("file_path"), str("notebook_path"))}
	case "Glob", "LS":
		tool, mapped = "glob", map[string]any{"path": str("path")}
	case "Grep":
		tool, mapped = "grep", map[string]any{"path": str("path")}
	case "WebFetch":
		tool, mapped = "web_fetch", map[string]any{"url": str("url")}
	default:
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		return strings.ToLower(name), input
	}
	raw, err := json.Marshal(mapped)
	if err != nil {
		return tool, json.RawMessage("{}")
	}
	return tool, raw
}

func ruleArg(name string, value map[string]any) string {
	str := func(key string) string {
		s, _ := value[key].(string)
		return s
	}
	switch name {
	case "Bash":
		return str("command")
	case "WebFetch":
		return str("url")
	case "WebSearch":
		return str("query")
	}
	return firstNonEmpty(str("file_path"), str("notebook_path"), str("path"), str("url"))
}

func decodeInput(input json.RawMessage) map[string]any {
	var value map[string]any
	if len(input) > 0 {
		_ = json.Unmarshal(input, &value)
	}
	if value == nil {
		value = map[string]any{}
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
