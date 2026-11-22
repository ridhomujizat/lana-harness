//go:build darwin

package claudechat

import "golang.org/x/sys/unix"

// parentPID returns pid's parent process id, read with sysctl(kern.proc.pid). It
// returns false when the pid is gone or the read is denied (REQ-WC-001).
func parentPID(pid int) (int, bool) {
	if pid <= 0 {
		return 0, false
	}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || info == nil {
		return 0, false
	}
	ppid := int(info.Eproc.Ppid)
	if ppid <= 0 {
		return 0, false
	}
	return ppid, true
}
