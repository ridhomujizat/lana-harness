package antigravity

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
	"sync"
	"syscall"
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

// DefaultSignInTimeout bounds `agy acp --login` (REQ-AGY-010).
const DefaultSignInTimeout = 15 * time.Minute

const (
	// modelTTL caches the `agy models` list of one account (REQ-AGY-009).
	modelTTL = 10 * time.Minute
	// modelsTimeout bounds one `agy models` run (API-AGY-012).
	modelsTimeout = 30 * time.Second
	// modelsHeader is the first line of `agy models` output; the model lines
	// follow it.
	modelsHeader = "Fetching available models..."
	// signInMarker is agy's answer for an account that is not signed in.
	signInMarker = "Please sign in"
	// probeMaxBytes caps the output a one-shot probe reads.
	probeMaxBytes = 1 << 20
)

// agyState caches the per-account probes of the runtime (the `agy models`
// status for StatusTTL and the model list for modelTTL) and holds the running
// sign-in and usage runs.
type agyState struct {
	mu        sync.Mutex
	accounts  map[string]*agyAccount
	authRuns  map[string]context.CancelFunc
	usageRuns map[uint64]context.CancelFunc
	usageSeq  uint64
}

// agyAccount is the cached probe of one account.
type agyAccount struct {
	status    core.RuntimeAccountStatus
	statusAt  time.Time
	hasStatus bool

	models    []core.RuntimeModel
	signedIn  bool
	modelsErr error
	modelsAt  time.Time
	hasModels bool
}

func newAgyState() *agyState {
	return &agyState{
		accounts:  map[string]*agyAccount{},
		authRuns:  map[string]context.CancelFunc{},
		usageRuns: map[uint64]context.CancelFunc{},
	}
}

// agyAccountName is the account of a probe: "" is the built-in one.
func agyAccountName(account core.RuntimeAccount) string {
	if account.Name == "" {
		return core.AntigravityProvider
	}
	return account.Name
}

// AccountStatus reports one Antigravity account (API-AGY-012): its binary and
// version, and, from `agy models` run with the account's HOME, whether the
// account is signed in and the models it offers. The probe is cached for
// StatusTTL per account and never reads a credential; the model list is cached
// for modelTTL and reported through RuntimeDeps.OnModels.
func (r *Runtime) AccountStatus(ctx context.Context, account core.RuntimeAccount) core.RuntimeAccountStatus {
	name := agyAccountName(account)
	r.agy.mu.Lock()
	a := r.agy.accounts[name]
	if a != nil && a.hasStatus && time.Since(a.statusAt) < r.opts.StatusTTL {
		st := a.status
		r.agy.mu.Unlock()
		return st
	}
	r.agy.mu.Unlock()
	st := r.probeAccount(ctx, account)
	if ctx.Err() == nil {
		r.agy.mu.Lock()
		a := r.agy.account(name)
		a.status, a.statusAt, a.hasStatus = st, time.Now(), true
		r.agy.mu.Unlock()
	}
	return st
}

var _ core.RuntimeAccountStatuser = (*Runtime)(nil)

// ForgetAccountStatus drops the cached status and model probe of one account
// (a sign-in just finished, or the account was removed), so the next
// AccountStatus runs `agy models` again.
func (r *Runtime) ForgetAccountStatus(account string) {
	if account == "" {
		account = core.AntigravityProvider
	}
	r.agy.mu.Lock()
	delete(r.agy.accounts, account)
	r.agy.mu.Unlock()
}

// account is the cached probe of key. The caller holds s.mu.
func (s *agyState) account(key string) *agyAccount {
	a := s.accounts[key]
	if a == nil {
		a = &agyAccount{}
		s.accounts[key] = a
	}
	return a
}

// probeAccount runs the account's version check and `agy models`.
func (r *Runtime) probeAccount(ctx context.Context, account core.RuntimeAccount) core.RuntimeAccountStatus {
	var st core.RuntimeAccountStatus
	bin, err := r.resolveBinary(account.Binary)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Binary = bin
	version, verr := r.checkVersion(ctx, bin)
	st.Version = version
	if verr != nil {
		st.Error = verr.Error()
		return st
	}
	st.VersionOK = true
	_, signedIn, merr := r.accountModels(ctx, account, bin)
	if merr != nil {
		st.Error = merr.Error()
		return st
	}
	st.LoggedIn = signedIn
	return st
}

// Models returns the cached `agy models` list of one account (API-AGY-026):
// the models the last AccountStatus probe read, at most modelTTL old. ok is
// false when no list was captured yet.
func (r *Runtime) Models(account string) ([]core.RuntimeModel, bool) {
	if account == "" {
		account = core.AntigravityProvider
	}
	r.agy.mu.Lock()
	defer r.agy.mu.Unlock()
	a := r.agy.accounts[account]
	if a == nil || !a.hasModels || len(a.models) == 0 {
		return nil, false
	}
	return append([]core.RuntimeModel(nil), a.models...), true
}

// accountModels returns the account's model list, running `agy models` when
// the cache is older than modelTTL. signedIn is false when agy answered
// "Please sign in". For the built-in account a changed list is reported
// through RuntimeDeps.OnModels.
func (r *Runtime) accountModels(ctx context.Context, account core.RuntimeAccount, bin string) ([]core.RuntimeModel, bool, error) {
	name := agyAccountName(account)
	r.agy.mu.Lock()
	a := r.agy.accounts[name]
	if a != nil && a.hasModels && time.Since(a.modelsAt) < modelTTL {
		models, signedIn, err := append([]core.RuntimeModel(nil), a.models...), a.signedIn, a.modelsErr
		r.agy.mu.Unlock()
		return models, signedIn, err
	}
	r.agy.mu.Unlock()

	env := buildEnv(r.opts.Environ(), core.RuntimeConfig{Home: account.Home})
	models, signedIn, err := r.probeModels(ctx, bin, env)
	if ctx.Err() == nil {
		r.agy.mu.Lock()
		a := r.agy.account(name)
		changed := !sameModels(a.models, models)
		a.models, a.signedIn, a.modelsErr = append([]core.RuntimeModel(nil), models...), signedIn, err
		a.modelsAt, a.hasModels = time.Now(), true
		r.agy.mu.Unlock()
		if err == nil && signedIn && changed && name == core.AntigravityProvider && r.deps.OnModels != nil {
			r.deps.OnModels(append([]core.RuntimeModel(nil), models...))
		}
	}
	return models, signedIn, err
}

// probeModels runs `<bin> models` with the account's env and reads its answer:
// the model list when it succeeded, signed out when it answered "Please sign
// in", and the last output line as the error otherwise.
func (r *Runtime) probeModels(ctx context.Context, bin string, env []string) ([]core.RuntimeModel, bool, error) {
	out, exitErr, err := r.runProbe(ctx, bin, []string{"models"}, env, "", modelsTimeout)
	if err != nil {
		return nil, false, err
	}
	if strings.Contains(out, signInMarker) {
		return nil, false, nil
	}
	if exitErr == nil {
		return parseModelLines(out), true, nil
	}
	if detail := lastLineDetail(out); detail != "" {
		return nil, false, &TurnError{Message: detail}
	}
	return nil, false, userError("Antigravity could not list its models")
}

// parseModelLines reads the `<modelId>\t<name>` lines that follow the
// "Fetching available models..." line.
func parseModelLines(out string) []core.RuntimeModel {
	var models []core.RuntimeModel
	started := false
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		line = strings.TrimRight(line, "\r")
		if !started {
			if strings.Contains(line, modelsHeader) {
				started = true
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		id, name, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		id = strings.TrimSpace(id)
		if id == "" || strings.HasPrefix(id, "-") {
			continue
		}
		models = append(models, core.RuntimeModel{Value: id, DisplayName: truncate(strings.TrimSpace(name), 80)})
	}
	return models
}

// sameModels reports whether two model lists are equal.
func sameModels(a, b []core.RuntimeModel) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// runProbe runs a one-shot `<bin> args...` with env and dir, stdin closed, its
// own process group, and a deadline. It returns the combined stdout and stderr
// and the exit error. A start failure, a deadline, or a cancel is returned as
// err; a non-zero exit is returned as exitErr with the output.
func (r *Runtime) runProbe(ctx context.Context, bin string, args, env []string, dir string, timeout time.Duration) (string, error, error) {
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Dir = dir
	// Stdin stays nil: the child reads /dev/null (API-AGY-012).
	buf := &probeBuffer{}
	cmd.Stdout = buf
	cmd.Stderr = buf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return "", nil, userError("Could not start Antigravity: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case exitErr := <-done:
		if pctx.Err() != nil {
			return buf.String(), nil, fmt.Errorf("antigravity probe cancelled: %w", ctx.Err())
		}
		return buf.String(), exitErr, nil
	case <-pctx.Done():
		r.stopProbe(cmd, done)
		if ctx.Err() != nil {
			return "", nil, fmt.Errorf("antigravity probe cancelled: %w", ctx.Err())
		}
		return "", nil, userError("Antigravity timed out after %s", timeout)
	}
}

// stopProbe ends a probe's process group: SIGTERM, then SIGKILL after
// KillGrace, and waits for the command.
func (r *Runtime) stopProbe(cmd *exec.Cmd, done <-chan error) {
	if cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-done:
		return
	case <-time.After(r.opts.KillGrace):
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
	select {
	case <-done:
	case <-time.After(r.opts.KillGrace):
	}
}

// cancelProbes cancels every running sign-in and usage probe (Shutdown).
func (r *Runtime) cancelProbes() {
	r.agy.mu.Lock()
	defer r.agy.mu.Unlock()
	for _, cancel := range r.agy.authRuns {
		cancel()
	}
	for _, cancel := range r.agy.usageRuns {
		cancel()
	}
}

// probeBuffer collects a probe's combined output, capped at probeMaxBytes.
type probeBuffer struct {
	buf bytes.Buffer
}

func (b *probeBuffer) Write(p []byte) (int, error) {
	if room := probeMaxBytes - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *probeBuffer) String() string { return b.buf.String() }
