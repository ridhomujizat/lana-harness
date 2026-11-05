package toolbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// versionRE matches a dotted numeric version inside a version line.
var versionRE = regexp.MustCompile(`\d+(?:\.\d+)*`)

// Detection status states (design "Status and detection").
const (
	StateMissing    = "missing"
	StateInstalling = "installing"
	StateInstalled  = "installed"
	StateTooOld     = "too_old"
	StateBroken     = "broken"
	StateFailed     = "failed"
)

// versionTimeout bounds a version command (REQ-TB-002).
const versionTimeout = 10 * time.Second

// Status is the detection result of one package (REQ-TB-002).
type Status struct {
	State      string `json:"state"`
	Version    string `json:"version"`
	BinaryPath string `json:"binary_path"`
	Error      string `json:"error"`
	CheckedAt  int64  `json:"checked_at"`
}

// Resolve finds the package binary: the package bin directory first, then
// PATH (REQ-TB-002).
func (s *Store) Resolve(m Manifest) (string, bool) {
	if m.Detect.Binary == "" {
		return "", false
	}
	candidate := filepath.Join(s.BinDir(m.ID), m.Detect.Binary)
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate, true
	}
	if path, err := exec.LookPath(m.Detect.Binary); err == nil {
		return path, true
	}
	return "", false
}

// Check resolves the binary, runs version_args with a 10 s timeout and compares
// the parsed version to MinVersion. The result is one of the design states
// (REQ-TB-002).
func (s *Store) Check(ctx context.Context, m Manifest) Status {
	status := Status{State: StateMissing, CheckedAt: time.Now().UnixMilli()}
	path, ok := s.Resolve(m)
	if !ok {
		return status
	}
	status.BinaryPath = path
	if len(m.Detect.VersionArgs) == 0 {
		status.State = StateInstalled
		return status
	}

	runCtx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	out, err := exec.CommandContext(runCtx, path, m.Detect.VersionArgs...).Output()
	if err != nil {
		status.State = StateBroken
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			status.Error = "version command timed out"
		} else {
			status.Error = commandError(err)
		}
		return status
	}

	line := firstLine(string(out))
	version := versionFromLine(m.Detect.Binary, line)
	status.Version = version

	if m.Detect.MinVersion == "" {
		status.State = StateInstalled
		return status
	}
	if parseVersion(version) == "" {
		status.State = StateBroken
		status.Error = "could not parse a version from " + strconv.Quote(line)
		return status
	}
	if compareVersions(version, m.Detect.MinVersion) < 0 {
		status.State = StateTooOld
		return status
	}
	status.State = StateInstalled
	return status
}

// firstLine returns the first line of s, trimmed.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// parseVersion returns the first dotted numeric run of line, or "".
func parseVersion(line string) string {
	return versionRE.FindString(line)
}

// versionFromLine reads the version of a version command's first line: the
// first dotted number, with a leading binary name stripped when present
// ("obscura 0.2.2" -> "0.2.2") (REQ-TB-002).
func versionFromLine(binary, line string) string {
	line = strings.TrimSpace(line)
	if binary != "" {
		if rest, ok := strings.CutPrefix(line, binary); ok {
			if rest = strings.TrimSpace(rest); rest != "" {
				line = rest
			}
		}
	}
	if version := parseVersion(line); version != "" {
		return version
	}
	return line
}

// compareVersions compares two dotted numeric versions: -1, 0 or 1. Non-numeric
// parts of a segment are ignored and a missing segment counts as zero
// (REQ-TB-002).
func compareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// versionParts splits a version on dots and keeps the leading digits of each
// segment.
func versionParts(v string) []int {
	fields := strings.Split(v, ".")
	parts := make([]int, 0, len(fields))
	for _, field := range fields {
		n := 0
		if digits := leadingDigits(field); digits != "" {
			n, _ = strconv.Atoi(digits)
		}
		parts = append(parts, n)
	}
	return parts
}

// leadingDigits returns the first run of ASCII digits in s.
func leadingDigits(s string) string {
	i := 0
	for i < len(s) && (s[i] < '0' || s[i] > '9') {
		i++
	}
	j := i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	return s[i:j]
}

// commandError renders a failed command, preferring the captured stderr.
func commandError(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if msg := strings.TrimSpace(string(exitErr.Stderr)); msg != "" {
			return msg
		}
		return "exit code " + strconv.Itoa(exitErr.ExitCode())
	}
	return err.Error()
}
