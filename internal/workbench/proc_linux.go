//go:build linux

package workbench

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ridhomujizat/lana-harness/internal/workbench/agentdetect"
	"golang.org/x/sys/unix"
)

// foreground returns the name and working directory of the pty's foreground
// process group (REQ-WB-013). The shell pid is unused: /proc holds the group
// leader's working directory directly.
func foreground(fd uintptr, shellPID int) (program, cwd string, ok bool) {
	if fd == 0 {
		return "", "", false
	}
	pgid, err := unix.IoctlGetInt(int(fd), unix.TIOCGPGRP)
	if err != nil || pgid <= 0 {
		return "", "", false
	}
	dir := "/proc/" + strconv.Itoa(pgid)
	comm, err := os.ReadFile(dir + "/comm")
	if err != nil {
		return "", "", false
	}
	name := strings.TrimSpace(string(comm))
	if name == "" {
		return "", "", false
	}
	if genericProgram(name) {
		if args, err := os.ReadFile(dir + "/cmdline"); err == nil {
			if a := agentArg(args); a != "" {
				name = a
			}
		}
	}
	if target, err := os.Readlink(dir + "/cwd"); err == nil {
		cwd = target
	}
	return name, cwd, true
}

// foregroundJob returns the pty's foreground process group: its id and every
// process in it, with the name, argv0 and argv agent detection needs
// (spec 55 A3).
func foregroundJob(fd uintptr) (agentdetect.Job, bool) {
	if fd == 0 {
		return agentdetect.Job{}, false
	}
	pgid, err := unix.IoctlGetInt(int(fd), unix.TIOCGPGRP)
	if err != nil || pgid <= 0 {
		return agentdetect.Job{}, false
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return agentdetect.Job{}, false
	}
	var processes []agentdetect.Process
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			continue
		}
		pgrp, comm, ok := processGroupAndComm(string(stat))
		if !ok || pgrp != pgid || comm == "" {
			continue
		}
		argv := linuxArgv(pid)
		argv0 := ""
		if len(argv) > 0 {
			argv0 = strings.TrimPrefix(filepath.Base(argv[0]), "-")
		}
		processes = append(processes, agentdetect.Process{PID: pid, Name: comm, Argv0: argv0, Argv: argv})
	}
	if len(processes) == 0 {
		return agentdetect.Job{}, false
	}
	sort.Slice(processes, func(i, j int) bool { return processes[i].PID < processes[j].PID })
	return agentdetect.Job{PGID: pgid, Processes: processes}, true
}

// processGroupAndComm reads the process group and comm from /proc/<pid>/stat.
// The (comm) field can hold spaces and parentheses, so the last ')' is found
// first.
func processGroupAndComm(stat string) (pgrp int, comm string, ok bool) {
	close := strings.LastIndexByte(stat, ')')
	open := strings.IndexByte(stat, '(')
	if close < 0 || open < 0 || open > close {
		return 0, "", false
	}
	fields := strings.Fields(stat[close+1:])
	// After (comm): state(0) ppid(1) pgrp(2)
	if len(fields) < 3 {
		return 0, "", false
	}
	pgrp, err := strconv.Atoi(fields[2])
	if err != nil {
		return 0, "", false
	}
	return pgrp, stat[open+1 : close], true
}

// linuxArgv returns a process's argv from /proc/<pid>/cmdline.
func linuxArgv(pid int) []string {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil || len(raw) == 0 {
		return nil
	}
	parts := bytes.Split(raw, []byte{0})
	argv := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		argv = append(argv, string(part))
	}
	return argv
}
