package permission

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ridhomujizat/lana-harness/internal/pattern"
)

// The auto policy (owner decision 2026-11-11, Hermes-style): the standard
// tools and the team tools run without asking; a dangerous shell command, a
// write outside the workspace, and any other tool (MCP servers, workflows,
// admin) still ask the owner.

var autoTools = map[string]bool{
	"read_file": true, "glob": true, "grep": true, "write_file": true, "edit_file": true,
	"bash": true, "job_list": true, "job_output": true, "job_kill": true,
	"web_fetch": true, "web_search": true,
	"memory_read": true, "memory_write": true,
	"knowledge_search": true, "knowledge_read": true, "knowledge_write": true, "knowledge_ingest": true,
	"ask_user": true, "session_search": true, "skill_load": true, "skill_write": true,
	"task": true, "task_continue": true,
	"discuss_start": true, "discuss_ask": true, "discuss_more": true, "discuss_end": true,
	"send_file": true, "calendar": true, "usage_stats": true,
}

// autoAllows reports whether the auto policy runs a call without asking.
func autoAllows(tool string, arg pattern.Argument, workspace string) bool {
	if !autoTools[tool] {
		return false
	}
	switch arg.Kind {
	case pattern.ArgPath:
		if tool == "write_file" || tool == "edit_file" {
			if !WithinWorkspace(workspace, arg.Value) {
				return false
			}
			if arg.Alt != "" && !WithinWorkspace(workspace, arg.Alt) {
				return false
			}
		}
		return true
	case pattern.ArgCommand:
		return tool != "bash" || !DangerousCommand(arg.Value, workspace)
	}
	return true
}

var (
	pipeToShell = regexp.MustCompile(`(?i)\b(curl|wget)\b[^|]*\|\s*(sudo\s+)?(ba|z|da)?sh\b`)
	devWrite    = regexp.MustCompile(`>\s*/dev/(sd|disk|nvme|hd)`)
	forkBomb    = regexp.MustCompile(`:\(\)\s*\{`)
)

// dangerousFirst are commands that always ask, whatever their arguments.
var dangerousFirst = map[string]bool{
	"sudo": true, "su": true, "doas": true, "mkfs": true, "dd": true, "fdisk": true, "diskutil": true,
	"shutdown": true, "reboot": true, "halt": true, "poweroff": true, "launchctl": true, "systemctl": true,
	"ssh": true, "scp": true, "sftp": true, "crontab": true, "passwd": true, "chown": true,
}

// DangerousCommand reports whether a shell command needs the owner under the
// auto policy: privilege, disks, remote hosts, deleting or changing files
// outside the workspace, force-pushing, publishing, piping a download into a
// shell, and commands lana cannot analyze.
func DangerousCommand(command, workspace string) bool {
	if pipeToShell.MatchString(command) || devWrite.MatchString(command) || forkBomb.MatchString(command) {
		return true
	}
	segments, analyzable := pattern.SplitCommand(command)
	if !analyzable {
		// Subshells, background jobs and the like: split on the shell's
		// separators and check every piece instead of asking for all of them.
		segments = roughSegments(command)
	}
	for _, segment := range segments {
		if dangerousSegment(segment, workspace) {
			return true
		}
	}
	return false
}

func dangerousSegment(segment, workspace string) bool {
	fields := strings.Fields(segment)
	// Skip leading env assignments (FOO=bar cmd …).
	for len(fields) > 0 && strings.Contains(fields[0], "=") && !strings.HasPrefix(fields[0], "-") {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return false
	}
	cmd := filepath.Base(fields[0])
	args := fields[1:]
	if dangerousFirst[cmd] {
		return true
	}
	has := func(flags ...string) bool {
		for _, a := range args {
			for _, f := range flags {
				if a == f || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && len(f) == 2 && strings.Contains(a[1:], f[1:])) {
					return true
				}
			}
		}
		return false
	}
	outside := func() bool {
		for _, a := range args {
			if strings.HasPrefix(a, "-") {
				continue
			}
			if a == "/" || a == "~" || strings.HasPrefix(a, "~/") || strings.HasPrefix(a, "$HOME") || a == "*" || a == ".." || strings.HasPrefix(a, "../") {
				return true
			}
			if filepath.IsAbs(a) && !WithinWorkspace(workspace, a) {
				return true
			}
		}
		return false
	}
	switch cmd {
	case "rm", "rmdir", "shred", "srm":
		// rm -rf asks wherever it points; a plain delete inside the
		// workspace does not.
		if has("-r", "-R", "--recursive") && has("-f", "--force") {
			return true
		}
		return outside()
	case "mv", "cp", "chmod", "ln":
		return outside()
	case "git":
		if len(args) > 0 && args[0] == "push" {
			return has("-f", "--force", "--force-with-lease", "--delete", "--mirror")
		}
		if len(args) > 0 && args[0] == "clean" {
			return has("-x", "-X")
		}
	case "npm", "pnpm", "yarn", "cargo", "twine", "gem":
		return len(args) > 0 && (args[0] == "publish" || args[0] == "unpublish" || args[0] == "login")
	case "docker", "podman":
		return len(args) > 0 && (args[0] == "system" || args[0] == "rm" || args[0] == "rmi" || args[0] == "volume" || args[0] == "push")
	case "kubectl", "helm":
		return len(args) > 0 && (args[0] == "delete" || args[0] == "uninstall" || args[0] == "apply")
	case "terraform", "tofu", "pulumi":
		return len(args) > 0 && (args[0] == "apply" || args[0] == "destroy" || args[0] == "up")
	case "kill", "killall", "pkill":
		return has("-9", "-KILL") && len(args) > 0 && args[len(args)-1] == "1"
	}
	return false
}

var shellSeparators = regexp.MustCompile(`&&|\|\||[;&|()\n` + "`" + `]|\$\(`)

// roughSegments splits a command the precise splitter cannot analyze.
func roughSegments(command string) []string {
	var out []string
	for _, part := range shellSeparators.Split(command, -1) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
