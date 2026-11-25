package antigravity

import (
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

// MinVersion is the oldest Antigravity CLI whose ACP extensions and /usage JSON
// lana relies on (ADR 0022).
const MinVersion = "1.5.17"

const probeTimeout = 15 * time.Second

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// versionInfo is one cached `agy --version` result.
type versionInfo struct {
	version string
	err     error
	modTime time.Time
	at      time.Time
}

// parseVersion reads the first x.y.z in `agy --version` output, for example
// "1.5.17".
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
// result is cached per binary until the binary changes or the TTL passes.
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
	cmd := exec.CommandContext(pctx, bin, "--version")
	cmd.Env = buildEnv(r.opts.Environ(), core.RuntimeConfig{})
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s --version failed: %v", bin, err)
	}
	version, v, ok := parseVersion(string(out))
	if !ok {
		return "", fmt.Errorf("cannot read the Antigravity version from %q", truncate(strings.TrimSpace(string(out)), 100))
	}
	_, min, _ := parseVersion(MinVersion)
	if !versionAtLeast(v, min) {
		return version, userError("Antigravity CLI %s is required (found %s)", MinVersion, version)
	}
	return version, nil
}

// Status reports the runtime's health (API-CCR-003): the binary and its
// version, plus the running and queued turn counts. Later tasks add the login
// state and the accounts.
func (r *Runtime) Status(ctx context.Context) core.RuntimeStatus {
	var st core.RuntimeStatus
	bin, err := r.resolveBinary("")
	if err != nil {
		st.Error = errNotInstalled
	} else {
		st.Binary = bin
		version, verr := r.checkVersion(ctx, bin)
		st.Version = version
		if verr != nil {
			st.Error = verr.Error()
		} else {
			st.VersionOK = true
		}
	}
	st.Running, st.Queued = r.sem.Counts()
	st.MaxConcurrent = r.opts.MaxConcurrent
	return st
}

// agentVersion reads the agent version an initialize result reports.
func agentVersion(resp json.RawMessage) string {
	var r struct {
		AgentInfo struct {
			Version string `json:"version"`
		} `json:"agentInfo"`
	}
	if json.Unmarshal(resp, &r) == nil {
		return r.AgentInfo.Version
	}
	return ""
}
