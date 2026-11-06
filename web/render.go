package web

import (
	"encoding/json"
	"strings"

	"github.com/ridhomujizat/lana-harness/internal/toolfmt"
)

// toolArgumentLimit is the primary argument length shown for a tool call.
const toolArgumentLimit = 60

// ToolStatusWord maps a runtime tool status to the status word of REQ-WEB-013.
func ToolStatusWord(status string) string {
	switch strings.ToLower(status) {
	case "":
		return ""
	case "running":
		return "running"
	case "success", "ok":
		return "ok"
	case "canceled":
		return "cancelled"
	case "error", "rejected", "cancelled":
		return strings.ToLower(status)
	default:
		return strings.ToLower(status)
	}
}

// ToolLine renders one tool call as a single line: the tool name, its primary
// argument cut to 60 characters, and the status word. An ask_user call shows
// its question count after " · ", for example "ask_user · 2 questions", and
// only the name when it has no questions (REQ-ASK-020).
func ToolLine(name string, input json.RawMessage, status string) string {
	line := name
	if arg := toolfmt.Cut(toolfmt.PrimaryArg(name, input), toolArgumentLimit); arg != "" {
		if name == "ask_user" {
			line += " · " + arg
		} else {
			line += " " + arg
		}
	}
	if word := ToolStatusWord(status); word != "" {
		line += " · " + word
	}
	return line
}
