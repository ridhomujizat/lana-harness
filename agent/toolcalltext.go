package agent

import (
	"encoding/json"
	"strings"
)

// The tool-call markers a model writes as plain text. DeepSeek mixes the
// fullwidth vertical bar U+FF5C with the lower one-eighth block U+2581
// (REQ-MEM-020).
const (
	deepseekCallsBegin = "<\uff5ctool\u2581calls\u2581begin\uff5c>"
	deepseekCallBegin  = "<\uff5ctool\u2581call\u2581begin\uff5c>"
	dsmlMarker         = "<\uff5cDSML\uff5c"
	xmlToolCall        = "<tool_call>"
	xmlFunctionCall    = "<function_call>"
)

// toolCallFences are the fenced-block info strings that name a tool call.
var toolCallFences = []string{"tool", "tool_call", "tool_code"}

// looksLikeToolCallText reports whether an assistant message that made no
// native tool call wrote one as plain text instead (REQ-MEM-020). It matches
// the DeepSeek and XML markers, a fenced block whose info string names a tool
// call, and a bare JSON object (or array of objects) with a name and an
// arguments or parameters field. Plain prose that mentions a tool name does
// not match.
func looksLikeToolCallText(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	for _, marker := range []string{deepseekCallsBegin, deepseekCallBegin, dsmlMarker, xmlToolCall, xmlFunctionCall} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	if hasToolCallFence(text) {
		return true
	}
	return isToolCallJSON(text)
}

// hasToolCallFence reports whether text opens a fenced block whose info string
// is one of toolCallFences. The fence must start a line, with at most three
// spaces of indentation, like CommonMark.
func hasToolCallFence(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if len(line)-len(trimmed) > 3 || !strings.HasPrefix(trimmed, "```") {
			continue
		}
		info := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
		for _, fence := range toolCallFences {
			if info == fence {
				return true
			}
		}
	}
	return false
}

// isToolCallJSON reports whether the whole trimmed text is a JSON object (or
// array of objects) that carries a tool call: a "name" plus "arguments" or
// "parameters".
func isToolCallJSON(text string) bool {
	trimmed := strings.TrimSpace(text)
	switch {
	case strings.HasPrefix(trimmed, "{"):
		var call map[string]json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &call); err != nil {
			return false
		}
		return isToolCallObject(call)
	case strings.HasPrefix(trimmed, "["):
		var calls []map[string]json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &calls); err != nil || len(calls) == 0 {
			return false
		}
		for _, call := range calls {
			if !isToolCallObject(call) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// isToolCallObject reports whether an object has a name and arguments.
func isToolCallObject(call map[string]json.RawMessage) bool {
	if _, ok := call["name"]; !ok {
		return false
	}
	if _, ok := call["arguments"]; ok {
		return true
	}
	_, ok := call["parameters"]
	return ok
}
