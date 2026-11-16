package workbench

import (
	"bytes"
	"path/filepath"
)

// agentNames are the coding agents looked for in a generic process's command
// line, so a session reports "claude" rather than "node" (REQ-WB-013).
var agentNames = map[string]bool{
	"claude": true, "codex": true, "gemini": true, "opencode": true,
}

// genericProgram reports whether a process name is too generic to be shown,
// so its command line is consulted as well.
func genericProgram(name string) bool { return name == "node" }

// agentArg returns the agent among the NUL-separated command-line arguments,
// or "" when there is none.
func agentArg(args []byte) string {
	for _, a := range bytes.Split(args, []byte{0}) {
		if name := filepath.Base(string(a)); agentNames[name] {
			return name
		}
	}
	return ""
}
