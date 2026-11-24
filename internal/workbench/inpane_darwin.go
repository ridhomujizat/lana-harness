//go:build darwin

package workbench

import "golang.org/x/sys/unix"

// procTTY and statRdev are the darwin halves of InPane; variables so tests can
// inject a process's controlling terminal and a path's device.
var (
	procTTY  = darwinProcTTY
	statRdev = darwinStatRdev
)

// darwinProcTTY returns the device number of pid's controlling terminal, its
// parent pid and whether the lookup succeeded, read from the process's
// kinfo_proc (Eproc.Ppid, Eproc.Tdev). A Tdev of NODEV (0xffffffff, that is
// -1) means the process has no controlling terminal.
func darwinProcTTY(pid int) (dev uint64, hasTTY bool, ppid int, ok bool) {
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, false, 0, false
	}
	ppid = int(proc.Eproc.Ppid)
	if uint32(proc.Eproc.Tdev) == 0xffffffff {
		return 0, false, ppid, true
	}
	return uint64(uint32(proc.Eproc.Tdev)), true, ppid, true
}

// darwinStatRdev returns the device number of the device node at path.
func darwinStatRdev(path string) (uint64, bool) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return 0, false
	}
	return uint64(uint32(st.Rdev)), true
}
