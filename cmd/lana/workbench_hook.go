package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workbench"
)

// hookInputMax caps the hook JSON read from stdin.
const hookInputMax = 1 << 20

// hookGitTimeout bounds the git lookup, so the hook ends quickly (REQ-WA-010).
const hookGitTimeout = time.Second

// hookInput is the part of the Claude Code SessionStart hook JSON the hook
// reads.
type hookInput struct {
	Source string `json:"source"`
	Cwd    string `json:"cwd"`
}

// hookOutput is the SessionStart answer: the added context in the wrapper
// Claude Code expects.
type hookOutput struct {
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

// hookSpecificOutput carries the context the hook adds; the event name is
// fixed to SessionStart.
type hookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

// hookInPane and hookGitRoot are variables so tests can drive the hook without
// a real pane or a repo.
var (
	hookInPane  = workbench.InPane
	hookGitRoot = gitTopLevel
)

// cmdWorkbenchHook runs `lana workbench hook <event>`.
func cmdWorkbenchHook(args []string) int {
	if len(args) == 0 || args[0] != "session-start" {
		fmt.Fprintln(os.Stderr, "error: use lana workbench hook session-start")
		return 2
	}
	return workbenchSessionStart(os.Stdin, os.Stdout)
}

// workbenchSessionStart is the Claude Code SessionStart hook program: inside a
// Workbench pane it writes the context that tells the agent about the pane,
// and everywhere else it writes nothing. Either way it exits 0 and never talks
// to the daemon (REQ-WA-010).
func workbenchSessionStart(in io.Reader, out io.Writer) int {
	var input hookInput
	// A hook JSON the hook does not understand is ignored: the answer then
	// only uses the working directory.
	_ = json.NewDecoder(io.LimitReader(in, hookInputMax)).Decode(&input)
	id, ok := hookInPane()
	if !ok {
		return 0
	}
	cwd := input.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	answer := hookOutput{HookSpecificOutput: hookSpecificOutput{
		HookEventName:     "SessionStart",
		AdditionalContext: workbenchHookText(id, hookGitRoot(cwd)),
	}}
	_ = json.NewEncoder(out).Encode(answer)
	return 0
}

// workbenchHookText is the context the SessionStart hook adds inside a pane
// (REQ-WA-011).
func workbenchHookText(id, root string) string {
	return "You are running inside a Lana Workbench pane (session " + id + "). The Workbench\n" +
		"shows the files and git changes of the worktree you work in. When you start\n" +
		"working in a git worktree other than " + root + ", run\n" +
		"`lana workbench use <worktree path>` before your first edit there. When you\n" +
		"go back to " + root + ", run `lana workbench use .`. To create a new worktree, run\n" +
		"`lana workbench use --new <branch>`; it creates it in the repo's worktree\n" +
		"folder and reports it. Subagents do not need to report.\n"
}

// gitTopLevel returns the root of the git worktree that holds dir, or dir
// itself when the lookup fails. The command runs without a shell, with a short
// timeout (REQ-WA-011).
func gitTopLevel(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), hookGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel")
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if err != nil {
		return dir
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return dir
	}
	return root
}
