package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/claudemcp"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
)

const claudeUsage = "error: use lana claude mcp-sync --account <name>"

// cmdClaude runs `lana claude ...`.
func cmdClaude(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, claudeUsage)
		return 2
	}
	switch args[0] {
	case "mcp-sync":
		home, _ := os.UserHomeDir()
		return cmdClaudeMCPSync(args[1:], home, os.Stdout, os.Stderr)
	default:
		fmt.Fprintf(os.Stderr, "error: unknown claude command: %s\n", args[0])
		return 2
	}
}

// cmdClaudeMCPSync runs `lana claude mcp-sync --account <name>` (spec 81
// REQ-SMC-005): the REQ-SMC-004 mirror of the built-in account's MCP servers
// (home/.claude.json) into the named Claude Code account, by hand. It exits 0
// also when it leaves the account untouched, with one short reason on stderr.
// Server entries are never printed.
func cmdClaudeMCPSync(args []string, home string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("claude mcp-sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	account := fs.String("account", "", "Claude Code account name")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 || *account == "" {
		_, _ = fmt.Fprintln(stderr, claudeUsage)
		return 2
	}
	lanaHome, err := homeDir()
	if err != nil {
		return fail(err, 1)
	}
	dir, reason := claudeAccountDir(lanaHome, *account)
	if reason != "" {
		_, _ = fmt.Fprintln(stderr, "lana claude mcp-sync: "+reason)
		return 0
	}
	wrote, err := claudemcp.Mirror(home, dir)
	switch {
	case err != nil:
		_, _ = fmt.Fprintln(stderr, "lana claude mcp-sync: "+err.Error()+"; account left as it was")
	case wrote:
		_, _ = fmt.Fprintf(stdout, "MCP servers of the built-in account mirrored into %s\n", *account)
	default:
		_, _ = fmt.Fprintf(stdout, "%s already has the built-in account's MCP servers\n", *account)
	}
	return 0
}

// claudeAccountDir returns the config dir of the named Claude Code account
// ([providers.<name>] kind = "claude-code", not the built-in one), else a
// short reason.
func claudeAccountDir(lanaHome, name string) (string, string) {
	if name == core.ClaudeCodeProvider {
		return "", "the built-in account is the source, not a target"
	}
	res, err := config.Load(context.Background(), config.Options{Home: lanaHome})
	if err != nil {
		return "", "config could not be loaded"
	}
	p, ok := res.Config.Providers[name]
	if !ok || p.Kind != core.ClaudeCodeKind {
		return "", fmt.Sprintf("%s is not a Claude Code account", name)
	}
	return llmcall.ClaudeConfigDir(lanaHome, name, p), ""
}
