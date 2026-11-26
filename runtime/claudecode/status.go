package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

// authStatus is the part of `claude auth status --json` lana reads: the login
// state, the auth method, and, for the account rows of the Router
// (API-CCT-011), the e-mail and plan. Nothing else of the output is decoded,
// and none of it is stored.
type authStatus struct {
	LoggedIn         bool   `json:"loggedIn"`
	AuthMethod       string `json:"authMethod"`
	Email            string `json:"email"`
	SubscriptionType string `json:"subscriptionType"`
	PlanTier         string `json:"planTier"`
}

// plan is planTier when it is more specific than subscriptionType ("max_20x"
// over "max"), else subscriptionType, else planTier.
func (a authStatus) plan() string {
	if a.PlanTier != "" && strings.HasPrefix(a.PlanTier, a.SubscriptionType) {
		return a.PlanTier
	}
	return a.SubscriptionType
}

// accountPlan is the plan name Claude Code itself shows: the server's
// display name cached in the global config file ("Ultra"), when the account
// is logged in and that file belongs to the same login, else auth.plan().
func accountPlan(auth authStatus, env []string) string {
	if !auth.LoggedIn {
		return auth.plan()
	}
	if name, email := planDisplayName(env); name != "" && (email == "" || strings.EqualFold(email, auth.Email)) {
		return name
	}
	return auth.plan()
}

// maxGlobalConfig caps how much of the global config file is read.
const maxGlobalConfig = 8 << 20

// planDisplayName reads oauthAccount.planDisplayName and emailAddress from
// Claude Code's global config file: $CLAUDE_CONFIG_DIR/.claude.json, else
// $HOME/.claude.json, from env. Only those two fields are decoded; any error
// gives empty values.
func planDisplayName(env []string) (name, email string) {
	path := globalConfigPath(env)
	if path == "" {
		return "", ""
	}
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer func() { _ = f.Close() }()
	var cfg struct {
		OAuthAccount struct {
			PlanDisplayName string `json:"planDisplayName"`
			EmailAddress    string `json:"emailAddress"`
		} `json:"oauthAccount"`
	}
	if err := json.NewDecoder(io.LimitReader(f, maxGlobalConfig)).Decode(&cfg); err != nil {
		return "", ""
	}
	return strings.TrimSpace(cfg.OAuthAccount.PlanDisplayName), strings.TrimSpace(cfg.OAuthAccount.EmailAddress)
}

// globalConfigPath is Claude Code's global config file for env.
func globalConfigPath(env []string) string {
	if dir := lastEnv(env, configDirEnv); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	home := lastEnv(env, "HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = h
	}
	return filepath.Join(home, ".claude.json")
}

// lastEnv is the last value of name in env, as exec uses it.
func lastEnv(env []string, name string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && k == name {
			return v
		}
	}
	return ""
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
	env, _, _ := buildEnv(r.opts.Environ(), core.RuntimeConfig{}, nil)
	auth, err := r.probeAuth(ctx, bin, env)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.LoggedIn, st.AuthMethod = auth.LoggedIn, auth.AuthMethod
	return st
}

// probeAuth runs `<bin> auth status --json` with env. The command prints its
// JSON on stdout even when it exits 1 (not logged in).
func (r *Runtime) probeAuth(ctx context.Context, bin string, env []string) (authStatus, error) {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(pctx, bin, "auth", "status", "--json")
	cmd.Env = env
	out, runErr := cmd.Output()
	auth, err := parseAuthStatus(out)
	if err != nil {
		if runErr != nil {
			return authStatus{}, fmt.Errorf("`claude auth status` failed: %s", runErr.Error())
		}
		return authStatus{}, err
	}
	return auth, nil
}

// cachedAccountStatus is one cached AccountStatus probe.
type cachedAccountStatus struct {
	status core.RuntimeAccountStatus
	at     time.Time
}

// AccountStatus reports one Claude Code account (API-CCT-011): its binary and
// version, and its login state, auth method, e-mail, and plan from
// `claude auth status` run with the account's CLAUDE_CONFIG_DIR. The probe is
// cached for StatusTTL per account; the e-mail and plan live only in that
// cache. Running and Queued count the account's turns.
func (r *Runtime) AccountStatus(ctx context.Context, account core.RuntimeAccount) core.RuntimeAccountStatus {
	name := account.Name
	if name == "" {
		name = core.ClaudeCodeProvider
	}
	key := strings.Join([]string{name, account.ConfigDir, account.Binary, account.Auth, account.APIKeyEnv}, "\x00")
	r.accountStatusMu.Lock()
	cached, ok := r.accountStatus[key]
	r.accountStatusMu.Unlock()
	st := cached.status
	if !ok || time.Since(cached.at) >= r.opts.StatusTTL {
		st = r.probeAccount(ctx, account)
		if ctx.Err() == nil {
			r.accountStatusMu.Lock()
			r.accountStatus[key] = cachedAccountStatus{status: st, at: time.Now()}
			r.accountStatusMu.Unlock()
		}
	}
	st.Running, st.Queued = r.accountTurns(name)
	return st
}

func (r *Runtime) probeAccount(ctx context.Context, account core.RuntimeAccount) core.RuntimeAccountStatus {
	var st core.RuntimeAccountStatus
	bin, err := r.accountBinary(account)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Binary = bin
	version, err := r.checkVersion(ctx, bin)
	st.Version = version
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.VersionOK = true
	env, _, err := buildEnv(r.opts.Environ(), core.RuntimeConfig{
		ConfigDir: account.ConfigDir, Auth: account.Auth, APIKeyEnv: account.APIKeyEnv,
	}, r.deps.Secrets)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	auth, err := r.probeAuth(ctx, bin, env)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.LoggedIn, st.AuthMethod = auth.LoggedIn, auth.AuthMethod
	st.Email, st.Plan = truncate(auth.Email, 200), truncate(accountPlan(auth, env), 80)
	return st
}

var _ core.RuntimeAccountStatuser = (*Runtime)(nil)
