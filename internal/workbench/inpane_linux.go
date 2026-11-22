//go:build linux

package workbench

import (
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// selfTTYDev and statRdev are the linux halves of InPane; variables so tests
// can inject a controlling terminal and a path's device.
var (
	selfTTYDev = linuxSelfTTYDev
	statRdev   = linuxStatRdev
)

// linuxSelfTTYDev returns the device number of the process's controlling
// terminal, field 7 (tty_nr) of /proc/self/stat. The (comm) field can hold
// spaces and parentheses, so the last ')' is found first.
func linuxSelfTTYDev() (uint64, bool) {
	raw, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0, false
	}
	stat := string(raw)
	close := strings.LastIndexByte(stat, ')')
	if close < 0 || close+1 >= len(stat) {
		return 0, false
	}
	fields := strings.Fields(stat[close+1:])
	// After (comm): state(0) ppid(1) pgrp(2) session(3) tty_nr(4).
	if len(fields) < 5 {
		return 0, false
	}
	n, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil {
		return 0, false
	}
	return uint64(uint32(n)), true
}

// linuxStatRdev returns the device number of the device node at path.
func linuxStatRdev(path string) (uint64, bool) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return 0, false
	}
	return st.Rdev, true
}
