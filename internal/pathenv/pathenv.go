// Package pathenv builds a child-process environment whose PATH starts with
// extra directories, such as the bin dirs of programs installed on the Tools
// page, so shell commands can call those programs by plain name.
package pathenv

import (
	"os"
	"path/filepath"
	"strings"
)

// defaultPath is used when the process has no PATH at all.
const defaultPath = "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

// Env returns os.Environ() with dirs prepended to PATH, for exec.Cmd.Env.
// Blank dirs are skipped. With no dirs it returns nil, so the child inherits
// the parent environment unchanged. Every other variable is kept as is.
func Env(dirs []string) []string {
	kept := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if strings.TrimSpace(dir) != "" {
			kept = append(kept, dir)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	current, ok := os.LookupEnv("PATH")
	if !ok || current == "" {
		current = defaultPath
	}
	path := strings.Join(kept, string(filepath.ListSeparator)) + string(filepath.ListSeparator) + current
	environ := os.Environ()
	out := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		if strings.HasPrefix(kv, "PATH=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "PATH="+path)
}

// FromFunc calls fn and returns Env of its result. A nil fn returns nil.
func FromFunc(fn func() []string) []string {
	if fn == nil {
		return nil
	}
	return Env(fn())
}
