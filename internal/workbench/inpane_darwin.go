//go:build darwin

package workbench

import (
	"os"

	"golang.org/x/sys/unix"
)

// selfTTYDev and statRdev are the darwin halves of InPane; variables so tests
// can inject a controlling terminal and a path's device.
var (
	selfTTYDev = darwinSelfTTYDev
	statRdev   = darwinStatRdev
)

// darwinSelfTTYDev returns the device number of the process's controlling
// terminal, read from its kinfo_proc (Eproc.Tdev).
func darwinSelfTTYDev() (uint64, bool) {
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", os.Getpid())
	if err != nil {
		return 0, false
	}
	return uint64(uint32(proc.Eproc.Tdev)), true
}

// darwinStatRdev returns the device number of the device node at path.
func darwinStatRdev(path string) (uint64, bool) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return 0, false
	}
	return uint64(uint32(st.Rdev)), true
}
