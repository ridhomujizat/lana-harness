// Package pathguard resolves filesystem paths for the workspace guard. It
// follows symlinks through every component, including dangling ones, so a
// caller can decide whether a path stays inside a workspace.
package pathguard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxSymlinkHops bounds symlink resolution so a loop cannot spin forever.
const maxSymlinkHops = 40

// Resolve returns path with its symlinks followed component by component. A
// component that is a symlink is replaced by its target, including a dangling
// symlink whose target does not exist. A missing component is kept literally:
// the components after it cannot exist either. Resolve errors past
// maxSymlinkHops symlink hops.
func Resolve(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	out := rootOf(abs)
	pending := split(abs)
	hops := 0
	for len(pending) > 0 {
		comp := pending[0]
		pending = pending[1:]
		switch comp {
		case "", ".":
			continue
		case "..":
			out = filepath.Dir(out)
			continue
		}
		base := out
		candidate := filepath.Join(base, comp)
		info, err := os.Lstat(candidate)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			out = candidate
			continue
		}
		hops++
		if hops > maxSymlinkHops {
			return "", fmt.Errorf("too many levels of symbolic links: %s", path)
		}
		link, err := os.Readlink(candidate)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(link) {
			out = rootOf(link)
		} else {
			out = base
		}
		pending = append(split(link), pending...)
	}
	return filepath.Clean(out), nil
}

// Inside reports whether target is base itself or lies below it. base and
// target must be absolute and cleaned, as Resolve produces.
func Inside(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

// rootOf returns the volume root of an absolute path ("/" or "C:\\").
func rootOf(path string) string {
	if vol := filepath.VolumeName(path); vol != "" {
		return vol + string(os.PathSeparator)
	}
	return string(os.PathSeparator)
}

// split returns the volume-less components of a cleaned path.
func split(path string) []string {
	path = filepath.Clean(path)
	if vol := filepath.VolumeName(path); vol != "" {
		path = strings.TrimPrefix(path, vol)
	}
	path = strings.TrimPrefix(path, string(os.PathSeparator))
	if path == "" || path == "." {
		return nil
	}
	return strings.Split(path, string(os.PathSeparator))
}
