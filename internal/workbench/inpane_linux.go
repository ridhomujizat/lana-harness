//go:build linux

package workbench

import (
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// procTTY and statRdev are the linux halves of InPane; variables so tests can
// inject a process's controlling terminal and a path's device.
var (
	procTTY  = linuxProcTTY
	statRdev = linuxStatRdev
)

// linuxProcTTY returns the device number of pid's controlling terminal, its
// parent pid and whether the lookup succeeded, read from /proc/<pid>/stat. The
// (comm) field can hold spaces and parentheses, so the last ')' is found
// first. A tty_nr of 0 means the process has no controlling terminal.
func linuxProcTTY(pid int) (dev uint64, hasTTY bool, ppid int, ok bool) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false, 0, false
	}
	stat := string(raw)
	close := strings.LastIndexByte(stat, ')')
	if close < 0 || close+1 >= len(stat) {
		return 0, false, 0, false
	}
	fields := strings.Fields(stat[close+1:])
	// After (comm): state(0) ppid(1) pgrp(2) session(3) tty_nr(4).
	if len(fields) < 5 {
		return 0, false, 0, false
	}
	ppid, err = strconv.Atoi(fields[1])
	if err != nil {
		return 0, false, 0, false
	}
	tty, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil {
		return 0, false, 0, false
	}
	if tty == 0 {
		return 0, false, ppid, true
	}
	return uint64(uint32(tty)), true, ppid, true
}

// linuxStatRdev returns the device number of the device node at path.
func linuxStatRdev(path string) (uint64, bool) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return 0, false
	}
	return st.Rdev, true
}
