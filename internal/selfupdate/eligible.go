package selfupdate

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ManualCommand is the install one-liner the dashboard shows when the daemon
// cannot replace its own binary (REQ-UPD-005, API-UPD-002).
const ManualCommand = "curl -fsSL https://raw.githubusercontent.com/ridhomujizat/lana-harness/main/install.sh | bash"

// Eligibility says whether this process can update itself (design.md,
// Eligibility).
type Eligibility struct {
	// Supported is false on a platform without restart in place.
	Supported bool
	// ExePath is the resolved executable path; empty when it cannot resolve.
	ExePath string
	// CanApply is false when the binary cannot be replaced; Reason says why.
	CanApply bool
	Reason   string
}

// SupportedOS reports whether self-update runs on goos.
func SupportedOS(goos string) bool { return goos == "darwin" || goos == "linux" }

// CheckEligibility resolves the executable (os.Executable, then
// filepath.EvalSymlinks) and decides can_apply: not under /nix/store, not in a
// Homebrew Cellar, and its directory accepts a new file (REQ-UPD-005).
// executable nil means os.Executable.
func CheckEligibility(goos string, executable func() (string, error)) Eligibility {
	if !SupportedOS(goos) {
		return Eligibility{Reason: "self-update is not supported on " + goos}
	}
	if executable == nil {
		executable = os.Executable
	}
	path, err := executable()
	if err != nil {
		return Eligibility{Supported: true, Reason: "cannot resolve the lana executable: " + err.Error()}
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	result := Eligibility{Supported: true, ExePath: path}
	slashed := filepath.ToSlash(path)
	switch {
	case strings.HasPrefix(slashed, "/nix/store/"):
		result.Reason = "lana runs from /nix/store, which is read-only"
		return result
	case strings.Contains(slashed, "/Cellar/"):
		result.Reason = "lana runs from a Homebrew Cellar (" + filepath.Dir(path) + "), which Homebrew manages"
		return result
	}
	dir := filepath.Dir(path)
	probe := filepath.Join(dir, ".lana-update-write-test-"+strconv.Itoa(os.Getpid()))
	file, err := os.OpenFile(probe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		result.Reason = fmt.Sprintf("lana cannot write to %s, the folder of its binary", dir)
		return result
	}
	_ = file.Close()
	_ = os.Remove(probe)
	result.CanApply = true
	return result
}
