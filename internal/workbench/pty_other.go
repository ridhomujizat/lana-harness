//go:build !darwin && !linux

package workbench

import (
	"errors"
	"syscall"
)

// errUnsupported is returned by the terminal pieces on a platform the core
// does not run on. The package still compiles, so the client side may be used
// from anywhere.
var errUnsupported = errors.New("workbench: the terminal core runs on macOS and Linux")

// pty is a stub off macOS and Linux.
type pty struct{}

func startPTY(path string, argv []string, dir string, env []string, cols, rows int) (*pty, error) {
	return nil, errUnsupported
}

func (p *pty) read(b []byte) (int, error)   { return 0, errUnsupported }
func (p *pty) write(b []byte) (int, error)  { return 0, errUnsupported }
func (p *pty) close() error                 { return nil }
func (p *pty) fd() uintptr                  { return 0 }
func (p *pty) foregroundGroup() (int, bool) { return 0, false }
func (p *pty) resize(cols, rows int) error  { return errUnsupported }
func (p *pty) pid() int                     { return 0 }
func (p *pty) wait() int                    { return -1 }
func (p *pty) hangup()                      {}
func (p *pty) kill()                        {}

func shellCommand(cmd []string) (string, []string, string, error) {
	return "", nil, "", errUnsupported
}

// detached starts a process in a session of its own; not on this platform.
func detached() *syscall.SysProcAttr { return nil }
