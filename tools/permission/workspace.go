package permission

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/ridhomujizat/lana-harness/internal/pattern"
)

// ResolvePath menyelesaikan path terhadap workspace dan symlink.
func ResolvePath(workspace, input string) (pattern.Argument, error) {
	if strings.HasPrefix(input, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return pattern.Argument{}, err
		}
		input = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(input, "~"), "/"))
	}
	if !filepath.IsAbs(input) {
		input = filepath.Join(workspace, input)
	}
	value := filepath.Clean(input)
	var alt string
	if resolved, err := filepath.EvalSymlinks(value); err == nil {
		alt = resolved
	} else if errors.Is(err, os.ErrNotExist) {
		alt = resolveExistingAncestor(value)
	} else {
		return pattern.Argument{}, err
	}
	return pattern.Argument{Kind: pattern.ArgPath, Value: value, Alt: alt}, nil
}

func resolveExistingAncestor(path string) string {
	cur := path
	var tail []string
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return path
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

// WithinWorkspace memeriksa path hasil resolusi.
func WithinWorkspace(workspace, resolved string) bool {
	ws, err := filepath.Abs(workspace)
	if err != nil {
		return false
	}
	if eval, err := filepath.EvalSymlinks(ws); err == nil {
		ws = eval
	}
	var candidate string
	if eval, err := filepath.EvalSymlinks(resolved); err == nil {
		candidate = eval
	} else {
		candidate = resolveExistingAncestor(resolved)
	}
	rel, err := filepath.Rel(ws, candidate)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
