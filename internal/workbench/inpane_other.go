//go:build !darwin && !linux

package workbench

// procTTY and statRdev are unavailable off macOS and Linux, so InPane is
// always false there (REQ-WA-002).
var (
	procTTY  = func(pid int) (uint64, bool, int, bool) { return 0, false, 0, false }
	statRdev = func(path string) (uint64, bool) { return 0, false }
)
