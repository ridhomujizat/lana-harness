package workbench

import (
	"path/filepath"

	"github.com/ridhomujizat/lana-harness/internal/claudemcp"
)

// mirrorClaudeMCP is the spec 81 REQ-SMC-004 mirror; tests may replace it.
var mirrorClaudeMCP = claudemcp.Mirror

// mirrorRestoredClaude mirrors the built-in account's MCP servers
// (home/.claude.json) into the account of a restored claude pane before its
// resume line is typed (spec 81 REQ-SMC-005), when the pane's config dir is a
// named Lana account's: directly under <lanaHome>/claude. Any other agent or
// config dir (the built-in account, a claude-swap profile) is left alone. It
// reports whether the mirror ran; a failure leaves the account as it was and
// never stops the resume.
func mirrorRestoredClaude(agent, configDir, lanaHome, home string) bool {
	if agent != "claude" || !namedClaudeAccountDir(configDir, lanaHome) || home == "" {
		return false
	}
	_, _ = mirrorClaudeMCP(home, configDir)
	return true
}

// namedClaudeAccountDir reports whether configDir is <lanaHome>/claude/<name>.
func namedClaudeAccountDir(configDir, lanaHome string) bool {
	return namedAccountDir(configDir, lanaHome, "claude")
}

// namedAccountDir reports whether dir is <lanaHome>/<kind>/<name>.
func namedAccountDir(configDir, lanaHome, kind string) bool {
	if configDir == "" || lanaHome == "" || !filepath.IsAbs(configDir) {
		return false
	}
	root, err := filepath.Abs(lanaHome)
	if err != nil {
		return false
	}
	dir := filepath.Clean(configDir)
	name := filepath.Base(dir)
	return filepath.Dir(dir) == filepath.Join(root, kind) && name != "." && name != ".."
}
