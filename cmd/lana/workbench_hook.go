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

// cmdWorkbenchHook runs `lana workbench hook <event>`: session-start tells a
// session it runs in a pane, and pre-edit reports the worktree an edit lands
// in (spec 69).
//
// With `--agent agy` the hooks speak Antigravity's hook protocol (spec 77
// REQ-WRA-009): session-start prints agy's injectSteps answer, and post-edit
// reports the worktree of the file an edit tool wrote. Both always print a
// JSON object agy accepts and exit 0.
func cmdWorkbenchHook(args []string) int {
	const usage = "error: use lana workbench hook session-start|pre-edit, or session-start|post-edit --agent agy"
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	agent := ""
	for i := 1; i < len(args); i++ {
		switch {
		case args[i] == "--agent" && i+1 < len(args):
			agent = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--agent="):
			agent = strings.TrimPrefix(args[i], "--agent=")
		default:
			fmt.Fprintln(os.Stderr, usage)
			return 2
		}
	}
	switch agent {
	case "", "claude":
		switch args[0] {
		case "session-start":
			return workbenchSessionStart(os.Stdin, os.Stdout)
		case "pre-edit":
			return workbenchPreEdit(os.Stdin)
		}
	case "agy":
		switch args[0] {
		case "session-start":
			return workbenchAgySessionStart(os.Stdin, os.Stdout)
		case "post-edit":
			return workbenchAgyPostEdit(os.Stdin, os.Stdout)
		}
	}
	fmt.Fprintln(os.Stderr, usage)
	return 2
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
		"shows the files and git changes of the worktree you work in. Your edits in\n" +
		"other worktrees of this repo are reported automatically. To show a worktree\n" +
		"you only read (no edits), run `lana workbench use <worktree path>`; to go back\n" +
		"to " + root + ", run `lana workbench use .`. To create a new worktree, run\n" +
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
