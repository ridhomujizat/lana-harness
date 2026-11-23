//go:build linux

package claudechat

import (
	"os"
	"strconv"
	"strings"
)

// parentPID returns pid's parent process id, read from /proc/<pid>/stat. It
// returns false when the pid is gone or the read is denied (REQ-WC-001).
func parentPID(pid int) (int, bool) {
	if pid <= 0 {
		return 0, false
	}
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	return statParentPID(string(raw))
}

// statParentPID reads the ppid field of a /proc/<pid>/stat line. The (comm)
// field can hold spaces and parentheses, so the last ')' is found first.
func statParentPID(stat string) (int, bool) {
	close := strings.LastIndexByte(stat, ')')
	if close < 0 || close+1 >= len(stat) {
		return 0, false
	}
	fields := strings.Fields(stat[close+1:])
	// After (comm): state(0) ppid(1)
	if len(fields) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil || ppid <= 0 {
		return 0, false
	}
	return ppid, true
}
