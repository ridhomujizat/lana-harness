package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// MinVersion is the oldest Claude Code with the stream-json control protocol
// and --permission-prompts (spec 38 Q-001).
const MinVersion = "2.1.0"

const probeTimeout = 15 * time.Second

type versionInfo struct {
	version string
	err     error
	modTime time.Time
	at      time.Time
}

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// parseVersion reads the first x.y.z in `claude --version` output, for example
// "2.1.319 (Claude Code)".
func parseVersion(out string) (string, [3]int, bool) {
	m := versionRe.FindStringSubmatch(out)
	if m == nil {
		return "", [3]int{}, false
	}
	var v [3]int
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return m[0], v, true
}

// versionAtLeast reports whether v >= min.
func versionAtLeast(v, min [3]int) bool {
	for i := range v {
		if v[i] != min[i] {
			return v[i] > min[i]
		}
	}
	return true
}

// checkVersion runs `<bin> --version` and checks it against MinVersion. The
// result is cached per binary until the binary changes or StatusTTL passes.
func (r *Runtime) checkVersion(ctx context.Context, bin string) (string, error) {
	var modTime time.Time
	if info, err := os.Stat(bin); err == nil {
		modTime = info.ModTime()
	}
	r.versionMu.Lock()
	cached, ok := r.versions[bin]
	r.versionMu.Unlock()
	if ok && cached.modTime.Equal(modTime) && time.Since(cached.at) < r.opts.StatusTTL {
		return cached.version, cached.err
	}
	version, err := r.probeVersion(ctx, bin)
	if ctx.Err() != nil {
		return version, ctx.Err()
	}
	r.versionMu.Lock()
	r.versions[bin] = versionInfo{version: version, err: err, modTime: modTime, at: time.Now()}
	r.versionMu.Unlock()
	return version, err
}

func (r *Runtime) probeVersion(ctx context.Context, bin string) (string, error) {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	env, _, _ := buildEnv(r.opts.Environ(), core.RuntimeConfig{}, nil)
	cmd := exec.CommandContext(pctx, bin, "--version")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s --version failed: %v", bin, err)
	}
	version, v, ok := parseVersion(string(out))
	if !ok {
		return "", fmt.Errorf("cannot read the Claude Code version from %q", truncate(strings.TrimSpace(string(out)), 100))
	}
	_, min, _ := parseVersion(MinVersion)
	if !versionAtLeast(v, min) {
		return version, userError("Claude Code %s is older than the minimum %s; update it with `claude update`", version, MinVersion)
	}
	return version, nil
}

// authStatus is the part of `claude auth status --json` lana reads. The output
// also holds the account e-mail and organization, which lana ignores.
type authStatus struct {
	LoggedIn   bool   `json:"loggedIn"`
	AuthMethod string `json:"authMethod"`
}

// parseAuthStatus reads `claude auth status --json` output. The command exits
// 1 when not logged in but still prints the JSON.
func parseAuthStatus(out []byte) (authStatus, error) {
	var st authStatus
	trimmed := bytes.TrimSpace(out)
	if start := bytes.IndexByte(trimmed, '{'); start > 0 {
		trimmed = trimmed[start:]
	}
	if err := json.Unmarshal(trimmed, &st); err != nil {
		return authStatus{}, fmt.Errorf("cannot read `claude auth status`: %s", truncate(redact(strings.TrimSpace(string(out)), ""), 200))
	}
	if st.AuthMethod == "none" {
		st.AuthMethod = ""
	}
	return st, nil
}

// Status reports the binary, version, login state, and queue (API-CCR-003).
// The probe runs `claude --version` and `claude auth status`, never reads a
// credential file, and is cached for StatusTTL.
func (r *Runtime) Status(ctx context.Context) core.RuntimeStatus {
	r.statusMu.Lock()
	st, at, bin := r.status, r.statusAt, r.lastBinary
	r.statusMu.Unlock()
	if at.IsZero() || time.Since(at) >= r.opts.StatusTTL {
		st = r.probeStatus(ctx, bin)
		if ctx.Err() == nil {
			r.statusMu.Lock()
			r.status, r.statusAt = st, time.Now()
			r.statusMu.Unlock()
		}
	}
	st.Running, st.Queued = r.sem.Counts()
	st.MaxConcurrent = r.opts.MaxConcurrent
	return st
}

func (r *Runtime) probeStatus(ctx context.Context, bin string) core.RuntimeStatus {
	var st core.RuntimeStatus
	if bin == "" {
		path, err := r.opts.LookPath("claude")
		if err != nil {
			st.Error = errNotInstalled
			return st
		}
		bin = path
	}
	st.Binary = bin
	version, err := r.checkVersion(ctx, bin)
	st.Version = version
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.VersionOK = true
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	env, _, _ := buildEnv(r.opts.Environ(), core.RuntimeConfig{}, nil)
	cmd := exec.CommandContext(pctx, bin, "auth", "status", "--json")
	cmd.Env = env
	out, runErr := cmd.Output()
	auth, err := parseAuthStatus(out)
	if err != nil {
		if runErr != nil {
			st.Error = "`claude auth status` failed: " + runErr.Error()
		} else {
			st.Error = err.Error()
		}
		return st
	}
	st.LoggedIn, st.AuthMethod = auth.LoggedIn, auth.AuthMethod
	return st
}
