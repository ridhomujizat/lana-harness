//go:build !darwin && !linux

package workbench

// selfTTYDev and statRdev are unavailable off macOS and Linux, so InPane is
// always false there (REQ-WA-002).
var (
	selfTTYDev = func() (uint64, bool) { return 0, false }
	statRdev   = func(string) (uint64, bool) { return 0, false }
)
