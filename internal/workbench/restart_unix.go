//go:build darwin || linux

package workbench

import (
	"errors"
	"syscall"
)

// processAlive reports whether pid still exists; EPERM means it exists under
// another user.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
