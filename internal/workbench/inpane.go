package workbench

import "os"

// envLookup reads the process environment; a variable so tests can inject.
var envLookup = os.LookupEnv

// maxPaneAncestors is how far the walk goes up the parent chain when a process
// has no controlling terminal: the process itself plus up to this many
// ancestors.
const maxPaneAncestors = 16

// InPane reports whether the process runs in a Lana Workbench pane, with the
// pane's session id (REQ-WA-002). Both markers must be set, and the controlling
// terminal of the process — or of its nearest ancestor that has one — must be
// the pty the pane named.
//
// The controlling terminal is read from the process itself, not from its
// standard streams. Claude Code runs its hooks and its Bash tool without a
// controlling terminal (their children show TTY ??), while the claude process
// itself is on the pane tty, so the walk goes up the parent chain to that
// process. A herdr, tmux or screen started inside a pane opens its own pty, so
// the first tty found is on that other pty and InPane stays false.
//
// procTTY and statRdev are the platform halves, set per platform and variables
// so tests can inject a process's controlling terminal and a path's device.
func InPane() (sessionID string, ok bool) {
	return inPaneFrom(os.Getpid())
}

// inPaneFrom reports whether pid is in a pane, with the pane's session id. It
// is InPane for a fixed pid, so tests can drive the ancestor walk without a
// real process tree.
func inPaneFrom(pid int) (string, bool) {
	id, ok := envLookup("LANA_WORKBENCH_SESSION")
	if !ok || id == "" {
		return "", false
	}
	tty, ok := envLookup("LANA_WORKBENCH_TTY")
	if !ok || tty == "" {
		return "", false
	}
	dev, ok := statRdev(tty)
	if !ok {
		return "", false
	}
	for level := 0; level <= maxPaneAncestors; level++ {
		pdev, hasTTY, ppid, ok := procTTY(pid)
		if !ok {
			return "", false
		}
		// A process that has its own controlling tty is never walked past:
		// the first tty found decides.
		if hasTTY {
			if pdev != dev {
				return "", false
			}
			return id, true
		}
		if ppid <= 1 {
			return "", false
		}
		pid = ppid
	}
	return "", false
}
