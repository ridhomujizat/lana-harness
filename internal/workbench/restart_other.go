//go:build !darwin && !linux

package workbench

// processAlive is unknown off macOS and Linux; the socket and core.pid checks
// decide there.
func processAlive(pid int) bool { return false }
