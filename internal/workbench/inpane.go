package workbench

import "os"

// envLookup reads the process environment; a variable so tests can inject.
var envLookup = os.LookupEnv

// InPane reports whether the process runs in a Lana Workbench pane, with the
// pane's session id (REQ-WA-002). Both markers must be set, and the process's
// controlling terminal must be the pty the pane named. The controlling
// terminal is read from the process itself, not from its standard streams, so
// a Claude Code hook (whose stdin and stdout are pipes) is still recognized. A
// herdr, tmux or screen started inside a pane opens its own terminal, so it
// has another controlling tty and is not in the pane.
//
// selfTTYDev and statRdev are the platform halves, set per platform and
// variables so tests can inject a controlling terminal and a path's device.
func InPane() (sessionID string, ok bool) {
	id, ok := envLookup("LANA_WORKBENCH_SESSION")
	if !ok || id == "" {
		return "", false
	}
	tty, ok := envLookup("LANA_WORKBENCH_TTY")
	if !ok || tty == "" {
		return "", false
	}
	self, ok := selfTTYDev()
	if !ok {
		return "", false
	}
	dev, ok := statRdev(tty)
	if !ok {
		return "", false
	}
	if self != dev {
		return "", false
	}
	return id, true
}
