//go:build darwin

package workbench

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unsafe"

	"github.com/ridhomujizat/lana-harness/internal/workbench/agentdetect"
	"golang.org/x/sys/unix"
)

// Darwin proc_info(2) arguments used to read a process's working directory.
//
// proc_info(PROC_INFO_CALL_PIDINFO, pid, PROC_PIDVNODEPATHINFO, 0, buf, size)
// fills a struct proc_vnodepathinfo (sys/proc_info.h):
//
//	struct vnode_info_path { struct vnode_info vip_vi; char vip_path[MAXPATHLEN]; };
//	struct proc_vnodepathinfo { struct vnode_info_path pvi_cdir; struct vnode_info_path pvi_rdir; };
//
// struct vnode_info is 152 bytes (vinfo_stat 136, two ints 8, fsid_t 8) and
// MAXPATHLEN is 1024, so pvi_cdir.vip_path starts at offset 152 and the whole
// struct is 2*(152+1024) = 2352. herdr reads the same struct for the same
// field.
const (
	procInfoCallPidInfo   = 2
	procPidVnodePathInfo  = 9
	vnodePathPathOffset   = 152
	vnodePathPathSize     = 1024
	procVnodePathInfoSize = 2 * (vnodePathPathOffset + vnodePathPathSize)
)

// darwinCwd returns pid's working directory, read with
// proc_info(PROC_INFO_CALL_PIDINFO, pid, PROC_PIDVNODEPATHINFO). It returns ""
// when the pid is gone or the read is denied (EPERM), which is not an error for
// the session (spec 55 A4).
func darwinCwd(pid int) string {
	if pid <= 0 {
		return ""
	}
	var buf [procVnodePathInfoSize]byte
	_, _, errno := syscall.Syscall6(unix.SYS_PROC_INFO, //nolint:staticcheck // proc_info has no libSystem wrapper; darwinCwd uses it too.
		procInfoCallPidInfo, uintptr(pid), procPidVnodePathInfo, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if errno != 0 {
		return ""
	}
	return cString(buf[vnodePathPathOffset : vnodePathPathOffset+vnodePathPathSize])
}

// darwinCwdOrShell reads the working directory of the foreground group leader,
// falling back to the pty's shell pid (the pty child) when the leader is gone
// or the read fails.
func darwinCwdOrShell(pgid, shellPID int) string {
	if dir := darwinCwd(pgid); dir != "" {
		return dir
	}
	if shellPID > 0 && shellPID != pgid {
		return darwinCwd(shellPID)
	}
	return ""
}

// foreground returns the name and working directory of the pty's foreground
// process group. macOS has no /proc, so the working directory comes from the
// proc_info syscall (REQ-WB-013).
func foreground(fd uintptr, shellPID int) (program, cwd string, ok bool) {
	if fd == 0 {
		return "", "", false
	}
	pgid, err := unix.IoctlGetInt(int(fd), unix.TIOCGPGRP)
	if err != nil || pgid <= 0 {
		return "", "", false
	}
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", pgid)
	if err != nil {
		return "", "", false
	}
	name := cString(proc.Proc.P_comm[:])
	if name == "" {
		return "", "", false
	}
	if genericProgram(name) {
		if a := agentArg(darwinArgs(pgid)); a != "" {
			name = a
		}
	}
	return name, darwinCwdOrShell(pgid, shellPID), true
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
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return agentdetect.Job{}, false
	}
	var processes []agentdetect.Process
	for i := range procs {
		if int(procs[i].Eproc.Pgid) != pgid {
			continue
		}
		pid := int(procs[i].Proc.P_pid)
		name := cString(procs[i].Proc.P_comm[:])
		if name == "" {
			continue
		}
		argv := darwinArgv(pid)
		processes = append(processes, agentdetect.Process{
			PID: pid, Name: name, Argv0: darwinArgv0(argv), Argv: argv,
		})
	}
	if len(processes) == 0 {
		return agentdetect.Job{}, false
	}
	sort.Slice(processes, func(i, j int) bool { return processes[i].PID < processes[j].PID })
	return agentdetect.Job{PGID: pgid, Processes: processes}, true
}

// darwinArgs returns the NUL-separated command-line arguments of a process,
// from the KERN_PROCARGS2 sysctl: an int32 argc, the exec path, NUL padding,
// then the arguments.
func darwinArgs(pid int) []byte {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(raw) < 4 {
		return nil
	}
	raw = raw[4:]
	i := bytes.IndexByte(raw, 0)
	if i < 0 {
		return nil
	}
	// Skip the exec path and the NUL padding that follows it.
	return bytes.TrimLeft(raw[i:], "\x00")
}

// darwinProcArgs2 reads a process's KERN_PROCARGS2 buffer and returns its argc
// and the buffer past the exec path and the NUL padding that follows it: the
// argc argv strings, then the environment strings. It returns false when the
// pid is gone or the read is denied.
func darwinProcArgs2(pid int) (argc int, rest []byte, ok bool) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(raw) < 4 {
		return 0, nil, false
	}
	argc = int(int32(binary.NativeEndian.Uint32(raw[:4])))
	if argc < 1 {
		return 0, nil, false
	}
	rest = raw[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return 0, nil, false
	}
	pos := end
	for pos < len(rest) && rest[pos] == 0 {
		pos++
	}
	return argc, rest[pos:], true
}

// darwinArgv returns a process's argv as KERN_PROCARGS2 reports it: exactly
// argc NUL-terminated arguments, so environment entries are not mistaken for
// arguments.
func darwinArgv(pid int) []string {
	argc, rest, ok := darwinProcArgs2(pid)
	if !ok {
		return nil
	}
	argv := make([]string, 0, argc)
	pos := 0
	for n := 0; n < argc && pos < len(rest); n++ {
		next := bytes.IndexByte(rest[pos:], 0)
		if next < 0 {
			next = len(rest) - pos
		}
		if next == 0 {
			return nil
		}
		argv = append(argv, string(rest[pos:pos+next]))
		pos += next + 1
	}
	return argv
}

// processEnv returns pid's environment as KEY=VALUE strings, from the
// KERN_PROCARGS2 buffer: the body that follows the argc arguments. It returns
// nil when the pid is gone or the read is denied (spec 60 addendum: the agent's
// own CLAUDE_CONFIG_DIR, which the session environment does not carry).
func processEnv(pid int) []string {
	argc, rest, ok := darwinProcArgs2(pid)
	if !ok {
		return nil
	}
	pos := 0
	for n := 0; n < argc; n++ {
		if pos >= len(rest) {
			return nil
		}
		next := bytes.IndexByte(rest[pos:], 0)
		if next < 0 {
			return nil
		}
		pos += next + 1
	}
	if pos >= len(rest) {
		return nil
	}
	return splitNul(rest[pos:])
}

// darwinArgv0 returns the basename of argv[0] with a leading dash removed, or
// "" when argv is empty.
func darwinArgv0(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	return strings.TrimPrefix(filepath.Base(argv[0]), "-")
}

// cString returns b up to its first NUL.
func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// Darwin proc_info(2) arguments used to read a process's open file descriptors
// and the paths behind them.
//
// proc_info(PROC_INFO_CALL_PIDINFO, pid, PROC_PIDLISTFDS, 0, buf, size) fills an
// array of struct proc_fdinfo (sys/proc_info.h):
//
//	struct proc_fdinfo { int32_t proc_fd; uint32_t proc_fdtype; };
//
// proc_info(PROC_INFO_CALL_PIDFDINFO, pid, PROC_PIDFDVNODEPATHINFO, fd, buf, size)
// fills a struct vnode_fdinfowithpath:
//
//	struct vnode_fdinfowithpath { struct proc_fileinfo pfi; struct vnode_info_path pvip; };
//
// struct proc_fileinfo is 24 bytes; struct vnode_info_path is a 152-byte
// vnode_info plus char vip_path[MAXPATHLEN], so the path starts at offset 176
// and the whole struct is 1200 bytes. herdr reads the same fds for the same
// purpose (spec 60 REQ-WR-010).
const (
	procInfoCallPidFdInfo   = 3
	procPidListFDs          = 1
	procPidFDVnodePathInfo  = 2
	proxFdTypeVnode         = 1
	procFdInfoSize          = 8
	procFdInfoMax           = 4096
	vnodeFdPathOffset       = 24 + 152
	vnodeFdInfoPathSize     = 1024
	vnodeFdInfoWithPathSize = vnodeFdPathOffset + vnodeFdInfoPathSize
)

// openFiles returns the absolute paths of the regular files pid has open, read
// with proc_info(PROC_PIDLISTFDS) and proc_info(PROC_PIDFDVNODEPATHINFO). It
// returns nil when the pid is gone or the read is denied (spec 60 REQ-WR-010).
func openFiles(pid int) []string {
	if pid <= 0 {
		return nil
	}
	var fds [procFdInfoMax * procFdInfoSize]byte
	n, _, errno := syscall.Syscall6(unix.SYS_PROC_INFO, //nolint:staticcheck // proc_info has no libSystem wrapper; darwinCwd uses it too.
		procInfoCallPidInfo, uintptr(pid), procPidListFDs, 0,
		uintptr(unsafe.Pointer(&fds[0])), uintptr(len(fds)))
	if errno != 0 || n == 0 {
		return nil
	}
	var paths []string
	seen := map[string]bool{}
	for i := 0; i < int(n)/procFdInfoSize; i++ {
		entry := fds[i*procFdInfoSize : (i+1)*procFdInfoSize]
		fd := int(int32(binary.NativeEndian.Uint32(entry[:4])))
		if binary.NativeEndian.Uint32(entry[4:8]) != proxFdTypeVnode {
			continue
		}
		var info [vnodeFdInfoWithPathSize]byte
		m, _, errno := syscall.Syscall6(unix.SYS_PROC_INFO, //nolint:staticcheck // proc_info has no libSystem wrapper; darwinCwd uses it too.
			procInfoCallPidFdInfo, uintptr(pid), procPidFDVnodePathInfo, uintptr(fd),
			uintptr(unsafe.Pointer(&info[0])), uintptr(len(info)))
		if errno != 0 || m == 0 {
			continue
		}
		path := cString(info[vnodeFdPathOffset : vnodeFdPathOffset+vnodeFdInfoPathSize])
		if !strings.HasPrefix(path, "/") || seen[path] {
			continue
		}
		st, err := os.Stat(path)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	return paths
}
