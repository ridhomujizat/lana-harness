package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/spill"
)

// maxToolOutput is the largest tool output read into memory before the spill
// policy in the tool runner trims it. It is large enough that the full text
// still reaches spill.
const maxToolOutput = 4 << 20

// ErrOutsideWorkspace is returned when a path is outside the workspace.
var ErrOutsideWorkspace = errors.New("outside workspace")

// Guard restricts path access to a single workspace.
type Guard struct {
	Workspace string
}

// Resolve resolves a path using the guard's workspace.
func (g *Guard) Resolve(path string) (string, error) {
	ws := g.Workspace
	if ws == "" {
		ws = "."
	}
	return ResolveInWorkspace(ws, path)
}

// ResolveInWorkspace returns the absolute path with symlinks evaluated.
func ResolveInWorkspace(workspace, path string) (string, error) {
	ws, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(ws); err == nil {
		ws = resolved
	}
	target := path
	if !filepath.IsAbs(target) {
		target = filepath.Join(ws, target)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	} else {
		parent := filepath.Dir(target)
		if resolvedParent, err := filepath.EvalSymlinks(parent); err == nil {
			target = filepath.Join(resolvedParent, filepath.Base(target))
		}
	}
	rel, err := filepath.Rel(ws, target)
	if err != nil {
		return "", ErrOutsideWorkspace
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", ErrOutsideWorkspace
	}
	return target, nil
}

// checkWorkspace checks that ws exists and is a directory. It returns a
// message that names the workspace path, so bash, glob, and grep report a
// clear error instead of the underlying operating system error.
func checkWorkspace(ws string) error {
	abs, err := filepath.Abs(ws)
	if err != nil {
		abs = ws
	}
	info, err := os.Stat(abs)
	if err != nil {
		switch {
		case os.IsNotExist(err):
			return fmt.Errorf("workspace does not exist: %s", abs)
		case errors.Is(err, syscall.ENOTDIR):
			// A parent path component is a file, so the workspace cannot be
			// reached as a directory.
			return fmt.Errorf("workspace is not a directory: %s", abs)
		default:
			return fmt.Errorf("workspace is not accessible: %s: %v", abs, err)
		}
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace is not a directory: %s", abs)
	}
	return nil
}

func effectiveWorkspace(g *Guard, tcWorkspace string) string {
	if tcWorkspace != "" {
		return tcWorkspace
	}
	if g != nil {
		return g.Workspace
	}
	return "."
}

func resolveToolPath(g *Guard, tcWorkspace, path string) (string, *core.ToolOutput) {
	resolved, err := ResolveInWorkspace(effectiveWorkspace(g, tcWorkspace), path)
	if err == nil {
		return resolved, nil
	}
	if errors.Is(err, ErrOutsideWorkspace) {
		out := core.ToolOutput{Status: core.ToolStatusRejected, Output: "outside workspace"}
		return "", &out
	}
	out := core.ToolOutput{Status: core.ToolStatusError, Output: err.Error()}
	return "", &out
}

// resolveReadToolPath resolves a path for the read-only tools. A path inside
// the spill directory of the session in tc is accepted even when it is outside
// the workspace (REQ-LTC-021); every other path resolves against the workspace.
func resolveReadToolPath(g *Guard, tc core.ToolCtx, workspace, path string) (string, *core.ToolOutput) {
	dir, ok := spillSessionDir(tc)
	if !ok {
		return resolveToolPath(g, workspace, path)
	}
	if target, inside := pathWithin(dir, path); inside {
		return target, nil
	}
	return resolveToolPath(g, workspace, path)
}

// spillSessionDir returns the canonical spill directory of the session in tc.
// ok is false when tc carries no home or session.
func spillSessionDir(tc core.ToolCtx) (string, bool) {
	if tc.Home == "" || tc.SessionID == "" {
		return "", false
	}
	return canonicalPath(spill.SessionDir(filepath.Join(tc.Home, "spill"), tc.SessionID)), true
}

// pathWithin returns the canonical target when it lies inside dir.
func pathWithin(dir, path string) (string, bool) {
	if path == "" {
		return "", false
	}
	target := canonicalPath(path)
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return target, true
}

// canonicalPath returns the absolute path with symlinks resolved as far as the
// path exists.
func canonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	if resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		return filepath.Join(resolvedParent, filepath.Base(abs))
	}
	return abs
}

func errorOutput(msg string) (core.ToolOutput, error) {
	return core.ToolOutput{Status: core.ToolStatusError, Output: msg}, nil
}

func successOutput(msg string) (core.ToolOutput, error) {
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: msg}, nil
}

func truncateText(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + fmt.Sprintf("\n...[truncated from %d characters]", len(runes))
}
