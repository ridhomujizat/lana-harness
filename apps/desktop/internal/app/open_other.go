//go:build !darwin

package app

import (
	"os/exec"
	"path/filepath"
)

// openInDefaultApp opens a file with the desktop's default application
// (REQ-DW-082).
func openInDefaultApp(path string) { _ = exec.Command("xdg-open", path).Start() }

// revealInFinder opens the file's folder where the system has no Finder
// (REQ-DW-082).
func revealInFinder(path string) { _ = exec.Command("xdg-open", filepath.Dir(path)).Start() }
