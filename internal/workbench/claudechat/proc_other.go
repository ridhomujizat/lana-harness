//go:build !darwin && !linux

package claudechat

// parentPID is unavailable off macOS and Linux, so no ancestor can be found
// (REQ-WC-001).
func parentPID(pid int) (int, bool) { return 0, false }
