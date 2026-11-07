package main

import "golang.org/x/sys/unix"

// processZombie reports whether pid has exited but its parent has not reaped
// it yet. A zombie still answers kill(pid, 0).
func processZombie(pid int) bool {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return false
	}
	// SZOMB in <sys/proc.h>.
	return info.Proc.P_stat == 5
}
