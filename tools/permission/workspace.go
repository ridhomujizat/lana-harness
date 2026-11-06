package permission

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ridhomujizat/lana-harness/internal/pathguard"
	"github.com/ridhomujizat/lana-harness/internal/pattern"
)

// ResolvePath resolves a path against the workspace and symlinks.
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
	alt, err := pathguard.Resolve(value)
	if err != nil {
		return pattern.Argument{}, err
	}
	return pattern.Argument{Kind: pattern.ArgPath, Value: value, Alt: alt}, nil
}

// WithinWorkspace checks a resolved path.
func WithinWorkspace(workspace, resolved string) bool {
	ws, err := filepath.Abs(workspace)
	if err != nil {
		return false
	}
	ws, err = pathguard.Resolve(ws)
	if err != nil {
		return false
	}
	candidate, err := pathguard.Resolve(resolved)
	if err != nil {
		return false
	}
	return pathguard.Inside(ws, candidate)
}
