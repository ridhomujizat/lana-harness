//go:build darwin

package app

import "os/exec"

// openInDefaultApp opens a file with the system's default application
// (REQ-DW-082).
func openInDefaultApp(path string) { _ = exec.Command("open", path).Start() }

// revealInFinder shows a file in Finder (REQ-DW-082).
func revealInFinder(path string) { _ = exec.Command("open", "-R", path).Start() }
