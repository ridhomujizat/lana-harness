package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
)

// ErrOutsideWorkspace dikembalikan saat path keluar dari workspace.
var ErrOutsideWorkspace = errors.New("di luar workspace")

// Guard membatasi akses path ke satu workspace.
type Guard struct {
	Workspace string
}

// Resolve memakai workspace guard.
func (g *Guard) Resolve(path string) (string, error) {
	ws := g.Workspace
	if ws == "" {
		ws = "."
	}
	return ResolveInWorkspace(ws, path)
}

// ResolveInWorkspace mengembalikan path absolut yang sudah dievaluasi symlink.
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
		out := core.ToolOutput{Status: core.ToolStatusRejected, Output: "di luar workspace"}
		return "", &out
	}
	out := core.ToolOutput{Status: core.ToolStatusError, Output: err.Error()}
	return "", &out
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
	return string(runes[:limit]) + fmt.Sprintf("\n...[dipotong dari %d karakter]", len(runes))
}
