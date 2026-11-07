package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/selfupdate"
	"github.com/ridhomujizat/lana-harness/web"
)

// Update timings (REQ-UPD-001, design.md). Tests shorten the variables.
var (
	// updateFirstCheckDelay is the wait before the first periodic check.
	updateFirstCheckDelay = 60 * time.Second
	// updateCheckInterval is the time between periodic checks.
	updateCheckInterval = 6 * time.Hour
)

const (
	// updateVersionTimeout limits `<new> --version` (design.md, verifying).
	updateVersionTimeout = 10 * time.Second
	// updateProbeTimeout limits `<new> update-probe` (design.md, probing).
	updateProbeTimeout = 60 * time.Second
	// updateProbeStderrLines is how much probe stderr a failure reports.
	updateProbeStderrLines = 20
)

// updateRunner runs a downloaded binary. The default runs a real process;
// tests use a fake.
type updateRunner interface {
	run(ctx context.Context, name string, args, env []string) (stdout, stderr []byte, err error)
}

// execUpdateRunner runs the command with os/exec.
type execUpdateRunner struct{}

func (execUpdateRunner) run(ctx context.Context, name string, args, env []string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if env != nil {
		cmd.Env = env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// updateRestarter is the restart path an update job uses for its waiting step:
// the webBackend Restart and CancelRestart of spec 16 (API-TFB-002).
type updateRestarter interface {
	Restart(ctx context.Context, force bool) (web.DaemonRestartOutcome, error)
	CancelRestart(ctx context.Context) (bool, error)
}

// releaseLister lists GitHub releases (selfupdate.Client in production).
type releaseLister interface {
	Releases(ctx context.Context) ([]selfupdate.Release, error)
}

// updateStateError is an ErrUpdateInvalidState or ErrUpdateUnavailable with
// its own message.
type updateStateError struct {
	base error
	msg  string
}

func (e updateStateError) Error() string { return e.msg }
func (e updateStateError) Unwrap() error { return e.base }

func updateInvalidState(format string, args ...any) error {
	return updateStateError{base: web.ErrUpdateInvalidState, msg: fmt.Sprintf(format, args...)}
}

func updateUnavailable(reason string) error {
	return updateStateError{base: web.ErrUpdateUnavailable, msg: reason}
}

// updateJob is one run of the update pipeline (design.md, Job state machine).
type updateJob struct {
	state    string
	version  string
	release  selfupdate.Release
	force    bool
	done     int64
	total    int64
	err      string
	download string
	cancel   context.CancelFunc
	// drained is true when the waiting step began a restart drain.
	drained bool
}

// active reports whether the job is between downloading and restarting.
func (j *updateJob) active() bool {
	return j != nil && j.state != web.UpdateStateFailed && j.state != web.UpdateStateCancelled
}

// updateServiceOptions builds an updateService.
type updateServiceOptions struct {
	Home    string
	Current string
	GOOS    string
	GOARCH  string
	Config  core.UpdateConfig
	// Releases lists GitHub releases; nil means the client from the
	// environment (API-UPD-001).
	Releases releaseLister
	// HTTP downloads the assets; nil means a client without a total timeout.
	HTTP *http.Client
	// Eligibility decides can_apply; nil means selfupdate.CheckEligibility.
	Eligibility func() selfupdate.Eligibility
	Runner      updateRunner
	Restarter   updateRestarter
	RestartCtl  *restartController
	Drain       func() drainStatus
	// WriteConfig writes [update] in config.toml (API-UPD-008).
	WriteConfig func(ctx context.Context, channel *string, check *bool) error
	// Root is the daemon root context; jobs and periodic checks stop with it.
	Root context.Context
	Now  func() time.Time
}

// updateService runs the self-update checks and jobs and implements
// web.UpdateStore (spec 36).
type updateService struct {
	home        string
	current     string
	goos        string
	goarch      string
	paths       selfupdate.Paths
	releases    releaseLister
	http        *http.Client
	eligibility func() selfupdate.Eligibility
	runner      updateRunner
	restarter   updateRestarter
	restartCtl  *restartController
	drain       func() drainStatus
	writeConfig func(ctx context.Context, channel *string, check *bool) error
	root        context.Context
	now         func() time.Time

	eligOnce sync.Once
	elig     selfupdate.Eligibility

	// checkMu serializes GitHub checks.
	checkMu sync.Mutex

	mu         sync.Mutex
	channel    string
	check      bool
	checkedAt  time.Time
	checkError string
	available  *selfupdate.Release
	job        *updateJob
}

var _ web.UpdateStore = (*updateService)(nil)

func newUpdateService(opts updateServiceOptions) *updateService {
	s := &updateService{
		home:        opts.Home,
		current:     opts.Current,
		goos:        opts.GOOS,
		goarch:      opts.GOARCH,
		paths:       selfupdate.Paths{Home: opts.Home},
		releases:    opts.Releases,
		http:        opts.HTTP,
		eligibility: opts.Eligibility,
		runner:      opts.Runner,
		restarter:   opts.Restarter,
		restartCtl:  opts.RestartCtl,
		drain:       opts.Drain,
		writeConfig: opts.WriteConfig,
		root:        opts.Root,
		now:         opts.Now,
		channel:     opts.Config.Channel,
		check:       opts.Config.Check,
	}
	if s.goos == "" {
		s.goos = runtime.GOOS
	}
	if s.goarch == "" {
		s.goarch = runtime.GOARCH
	}
	if !selfupdate.ValidChannel(s.channel) {
		s.channel = selfupdate.ChannelStable
	}
	if s.releases == nil {
		s.releases = selfupdate.NewClientFromEnv(nil, nil, "lana/"+s.current)
	}
	if s.http == nil {
		s.http = &http.Client{}
	}
	if s.eligibility == nil {
		goos := s.goos
		s.eligibility = func() selfupdate.Eligibility { return selfupdate.CheckEligibility(goos, nil) }
	}
	if s.runner == nil {
		s.runner = execUpdateRunner{}
	}
	if s.root == nil {
		s.root = context.Background()
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

// assetName is the release asset for this platform.
func (s *updateService) assetName() string { return selfupdate.AssetName(s.goos, s.goarch) }

// eligible caches the eligibility check: it creates a file next to the binary.
func (s *updateService) eligible() selfupdate.Eligibility {
	s.eligOnce.Do(func() { s.elig = s.eligibility() })
	return s.elig
}

// runningVersion parses the running version; false for a dev build.
func (s *updateService) runningVersion() (selfupdate.Version, bool) {
	return selfupdate.ParseVersion(s.current)
}

// supported is false for a dev build or an unsupported OS (API-UPD-002).
func (s *updateService) supported() bool {
	if _, ok := s.runningVersion(); !ok {
		return false
	}
	return selfupdate.SupportedOS(s.goos) && s.eligible().Supported
}

// canApply says whether the binary can be replaced, with the reason when not
// (REQ-UPD-005).
func (s *updateService) canApply() (bool, string) {
	if _, ok := s.runningVersion(); !ok {
		return false, "this is a development build (lana " + s.current + "); install a release to update from the dashboard"
	}
	elig := s.eligible()
	if !elig.Supported || !selfupdate.SupportedOS(s.goos) {
		if elig.Reason != "" {
			return false, elig.Reason
		}
		return false, "self-update is not supported on " + s.goos
	}
	if !elig.CanApply {
		return false, elig.Reason
	}
	if s.restartCtl == nil || !s.restartCtl.supported() || s.restarter == nil {
		return false, "restart in place is not available"
	}
	return true, ""
}

// UpdateStatus implements web.UpdateStore (API-UPD-003). It never calls GitHub.
func (s *updateService) UpdateStatus(context.Context) web.UpdateStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

func (s *updateService) statusLocked() web.UpdateStatus {
	canApply, reason := s.canApply()
	status := web.UpdateStatus{
		Current:       s.current,
		Channel:       s.channel,
		Check:         s.check,
		Supported:     s.supported(),
		CanApply:      canApply,
		ManualReason:  reason,
		ManualCommand: selfupdate.ManualCommand,
		CheckError:    s.checkError,
	}
	if !s.checkedAt.IsZero() {
		at := s.checkedAt.UnixMilli()
		status.CheckedAt = &at
	}
	if state, err := selfupdate.ReadState(s.paths); err == nil {
		status.Skipped = state.Skipped
		if state.LastResult != nil {
			result := web.UpdateResult(*state.LastResult)
			status.LastResult = &result
		}
	}
	if !status.Supported {
		return status
	}
	if s.available != nil {
		status.Available = webRelease(*s.available, s.assetName())
	}
	if s.job != nil {
		s.syncWaitingLocked()
		status.Job = s.webJobLocked()
	}
	return status
}

// syncWaitingLocked notices a restart drain that was cancelled elsewhere (the
// daemon card or a stop signal turning it into a stop) and cancels the job.
func (s *updateService) syncWaitingLocked() {
	job := s.job
	if job == nil || job.state != web.UpdateStateWaiting || !job.drained || s.drain == nil {
		return
	}
	if status := s.drain(); status.State == drainIdle || status.Reason != drainRestart {
		s.restartCtl.setUpdateHook(nil)
		job.state = web.UpdateStateCancelled
		s.cleanupJob(job)
	}
}

func (s *updateService) webJobLocked() *web.UpdateJob {
	job := s.job
	out := &web.UpdateJob{State: job.state, Version: job.version}
	switch job.state {
	case web.UpdateStateDownloading:
		out.Progress = &web.UpdateProgress{Done: job.done, Total: job.total}
	case web.UpdateStateWaiting:
		if s.drain != nil {
			status := s.drain()
			out.RunningTurns = status.Running
			if !status.Deadline.IsZero() {
				deadline := status.Deadline.UnixMilli()
				out.Deadline = &deadline
			}
		}
	case web.UpdateStateFailed:
		out.Error = job.err
	}
	return out
}

func webRelease(release selfupdate.Release, asset string) *web.UpdateRelease {
	out := &web.UpdateRelease{
		Version:      release.Version.String(),
		Tag:          release.Tag,
		Name:         release.Name,
		Notes:        release.Notes,
		URL:          release.URL,
		Prerelease:   release.Prerelease,
		UpgradeNotes: release.UpgradeNotes(),
	}
	if !release.PublishedAt.IsZero() {
		out.PublishedAt = release.PublishedAt.UnixMilli()
	}
	if item, ok := release.Asset(asset); ok {
		out.Size = item.Size
	}
	return out
}

// CheckUpdate implements web.UpdateStore (API-UPD-004). A GitHub failure keeps
// the last result and sets check_error (AC-UPD-004).
func (s *updateService) CheckUpdate(ctx context.Context) (web.UpdateStatus, error) {
	s.mu.Lock()
	if s.job.active() {
		s.mu.Unlock()
		return web.UpdateStatus{}, updateInvalidState("an update to %s is running", s.job.version)
	}
	s.mu.Unlock()
	s.runCheck(ctx)
	return s.UpdateStatus(ctx), nil
}

// runCheck asks GitHub for the newest release on the channel (REQ-UPD-001).
// A dev build never checks.
func (s *updateService) runCheck(ctx context.Context) {
	current, ok := s.runningVersion()
	if !ok || !s.supported() {
		return
	}
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	s.mu.Lock()
	channel := s.channel
	s.mu.Unlock()

	checkCtx, cancel := context.WithTimeout(ctx, selfupdate.CheckTimeout)
	defer cancel()
	releases, err := s.releases.Releases(checkCtx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if channel != s.channel {
		// The channel changed during the request; the caller checks again.
		return
	}
	s.checkedAt = s.now()
	if err != nil {
		s.checkError = err.Error()
		slog.Warn("update: check failed", "error", err)
		return
	}
	s.checkError = ""
	if release, ok := selfupdate.Select(releases, current, channel, s.assetName()); ok {
		s.available = &release
	} else {
		s.available = nil
	}
}

// runPeriodic checks 60 seconds after start and then every 6 hours while
// update.check is true (REQ-UPD-001). A dev build never checks.
func (s *updateService) runPeriodic(ctx context.Context) {
	if !s.supported() {
		return
	}
	timer := time.NewTimer(updateFirstCheckDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		s.mu.Lock()
		enabled := s.check && !s.job.active()
		s.mu.Unlock()
		if enabled {
			s.runCheck(ctx)
		}
		timer.Reset(updateCheckInterval)
	}
}

// ApplyUpdate implements web.UpdateStore (API-UPD-005). It starts the job and
// returns with job.state downloading.
func (s *updateService) ApplyUpdate(ctx context.Context, version string, force bool) (web.UpdateStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.supported() {
		return web.UpdateStatus{}, updateUnavailable("this lana build cannot update itself")
	}
	if ok, reason := s.canApply(); !ok {
		return web.UpdateStatus{}, updateUnavailable(reason)
	}
	if s.job.active() {
		// API-UPD-005: "Interrupt and update" repeats the apply with force
		// while the job waits for running turns. The turns are interrupted
		// with the restart cause and the completed drain runs installing.
		job := s.job
		if force && job.state == web.UpdateStateWaiting && job.drained && selfupdate.SameVersion(job.version, version) {
			if _, err := s.restarter.Restart(ctx, true); err != nil {
				if errors.Is(err, web.ErrRestartInvalidState) {
					return web.UpdateStatus{}, updateInvalidState("%s", err.Error())
				}
				return web.UpdateStatus{}, err
			}
			return s.statusLocked(), nil
		}
		return web.UpdateStatus{}, updateInvalidState("an update to %s is running", s.job.version)
	}
	if s.available == nil || !selfupdate.SameVersion(s.available.Version.String(), version) {
		return web.UpdateStatus{}, updateInvalidState("version %s is not the available update", version)
	}
	jobCtx, cancel := context.WithCancel(s.root)
	job := &updateJob{
		state:   web.UpdateStateDownloading,
		version: s.available.Version.String(),
		release: *s.available,
		force:   force,
		cancel:  cancel,
	}
	if asset, ok := job.release.Asset(s.assetName()); ok {
		job.total = asset.Size
	}
	s.job = job
	go s.runJob(jobCtx, job)
	return s.statusLocked(), nil
}

// CancelUpdate implements web.UpdateStore (API-UPD-006). It cancels a job in
// downloading, verifying, probing, or waiting; waiting also cancels the
// restart drain.
func (s *updateService) CancelUpdate(ctx context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.job
	if job == nil {
		return false, nil
	}
	switch job.state {
	case web.UpdateStateDownloading, web.UpdateStateVerifying, web.UpdateStateProbing:
		job.cancel()
		job.state = web.UpdateStateCancelled
		return true, nil
	case web.UpdateStateWaiting:
		if !job.drained {
			return false, nil
		}
		cancelled, err := s.restarter.CancelRestart(ctx)
		if err != nil || !cancelled {
			return false, err
		}
		s.restartCtl.setUpdateHook(nil)
		job.state = web.UpdateStateCancelled
		s.cleanupJob(job)
		return true, nil
	}
	return false, nil
}

// SkipUpdate implements web.UpdateStore (API-UPD-007).
func (s *updateService) SkipUpdate(ctx context.Context, version string) (web.UpdateStatus, error) {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if _, err := selfupdate.UpdateState(s.paths, func(state *selfupdate.State) { state.Skipped = version }); err != nil {
		return web.UpdateStatus{}, err
	}
	return s.UpdateStatus(ctx), nil
}

// UpdateSettings implements web.UpdateStore (API-UPD-008): it writes [update]
// in config.toml, applies it live, and checks again when the channel changed.
func (s *updateService) UpdateSettings(ctx context.Context, channel *string, check *bool) (web.UpdateStatus, error) {
	if channel != nil && !selfupdate.ValidChannel(*channel) {
		return web.UpdateStatus{}, fmt.Errorf("unknown channel %q", *channel)
	}
	if channel == nil && check == nil {
		return s.UpdateStatus(ctx), nil
	}
	if s.writeConfig != nil {
		if err := s.writeConfig(ctx, channel, check); err != nil {
			return web.UpdateStatus{}, err
		}
	}
	s.mu.Lock()
	cfg := core.UpdateConfig{Channel: s.channel, Check: s.check}
	s.mu.Unlock()
	if channel != nil {
		cfg.Channel = *channel
	}
	if check != nil {
		cfg.Check = *check
	}
	if s.applyConfig(cfg) {
		s.mu.Lock()
		busy := s.job.active()
		s.mu.Unlock()
		if !busy {
			s.runCheck(ctx)
		}
	}
	return s.UpdateStatus(ctx), nil
}

// applyConfig applies a new [update] table and reports whether the channel
// changed. A changed channel drops the release found on the old one.
func (s *updateService) applyConfig(cfg core.UpdateConfig) bool {
	if !selfupdate.ValidChannel(cfg.Channel) {
		cfg.Channel = selfupdate.ChannelStable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := cfg.Channel != s.channel
	s.channel = cfg.Channel
	s.check = cfg.Check
	if changed {
		s.available = nil
		s.checkedAt = time.Time{}
		s.checkError = ""
	}
	return changed
}

// applyConfigAsync applies a config file written outside the update settings
// endpoint, for example a raw config.toml save, and re-checks in the
// background when the channel changed.
func (s *updateService) applyConfigAsync(cfg core.UpdateConfig) {
	if s.applyConfig(cfg) {
		go s.runCheck(s.root)
	}
}

// ClearUpdateResult implements web.UpdateStore (API-UPD-009).
func (s *updateService) ClearUpdateResult(context.Context) error {
	_, err := selfupdate.UpdateState(s.paths, func(state *selfupdate.State) { state.LastResult = nil })
	return err
}

// setJobState moves the job to state unless it already ended. It reports
// false when the job was cancelled meanwhile.
func (s *updateService) setJobState(job *updateJob, state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !job.active() {
		return false
	}
	job.state = state
	return true
}

// failJob ends the job: cancelled when its context ended, failed otherwise.
// The download and the probe home are removed; the daemon keeps running the
// old binary (REQ-UPD-003).
func (s *updateService) failJob(ctx context.Context, job *updateJob, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job.active() {
		if ctx.Err() != nil {
			job.state = web.UpdateStateCancelled
		} else {
			job.state = web.UpdateStateFailed
			job.err = err.Error()
			slog.Warn("update: job failed", "version", job.version, "error", err)
		}
	}
	s.cleanupJob(job)
}

// cleanupJob removes the job's download and probe home.
func (s *updateService) cleanupJob(job *updateJob) {
	if job.download != "" {
		_ = os.Remove(job.download)
	}
	_ = os.RemoveAll(s.paths.ProbeHome(job.version))
}

// runJob runs downloading, verifying, probing, and waiting (design.md, Job
// state machine). Installing and restarting run in the restart hook.
func (s *updateService) runJob(ctx context.Context, job *updateJob) {
	defer job.cancel()
	asset, ok := job.release.Asset(s.assetName())
	sums, sumsOK := job.release.Asset(selfupdate.SumsAsset)
	if !ok || !sumsOK {
		s.failJob(ctx, job, fmt.Errorf("release %s has no %s or %s", job.release.Tag, s.assetName(), selfupdate.SumsAsset))
		return
	}

	// downloading
	if err := os.MkdirAll(s.paths.Dir(), 0o700); err != nil {
		s.failJob(ctx, job, err)
		return
	}
	dest := s.paths.Download(job.version)
	s.mu.Lock()
	job.download = dest
	s.mu.Unlock()
	_, err := selfupdate.Download(ctx, s.http, asset.URL, dest, selfupdate.MaxDownloadSize, func(done, total int64) {
		if total == 0 {
			total = asset.Size
		}
		s.mu.Lock()
		job.done, job.total = done, total
		s.mu.Unlock()
	})
	if err != nil {
		s.failJob(ctx, job, fmt.Errorf("download: %w", err))
		return
	}
	sumsData, err := selfupdate.FetchSums(ctx, s.http, sums.URL)
	if err != nil {
		s.failJob(ctx, job, fmt.Errorf("download %s: %w", selfupdate.SumsAsset, err))
		return
	}

	// verifying
	if !s.setJobState(job, web.UpdateStateVerifying) {
		s.failJob(ctx, job, context.Canceled)
		return
	}
	if err := selfupdate.VerifyFile(dest, s.assetName(), sumsData); err != nil {
		s.failJob(ctx, job, err)
		return
	}
	if err := os.Chmod(dest, 0o755); err != nil {
		s.failJob(ctx, job, err)
		return
	}
	if s.goos == "darwin" {
		// Best effort: a file written by this process has no quarantine
		// attribute, but a copied one may.
		_, _, _ = s.runner.run(ctx, "xattr", []string{"-d", "com.apple.quarantine", dest}, nil)
	}
	versionCtx, cancelVersion := context.WithTimeout(ctx, updateVersionTimeout)
	stdout, stderr, err := s.runner.run(versionCtx, dest, []string{"--version"}, nil)
	cancelVersion()
	if err != nil {
		s.failJob(ctx, job, fmt.Errorf("run %s --version: %v%s", s.assetName(), err, stderrTail(stderr, 5)))
		return
	}
	if !versionOutputMatches(string(stdout), job.version) {
		s.failJob(ctx, job, fmt.Errorf("the new binary reports %q, want version %s", strings.TrimSpace(string(stdout)), job.version))
		return
	}

	// probing
	if !s.setJobState(job, web.UpdateStateProbing) {
		s.failJob(ctx, job, context.Canceled)
		return
	}
	if err := s.probe(ctx, dest, job.version); err != nil {
		s.failJob(ctx, job, err)
		return
	}

	// waiting
	s.mu.Lock()
	defer s.mu.Unlock()
	if !job.active() || ctx.Err() != nil {
		job.state = web.UpdateStateCancelled
		s.cleanupJob(job)
		return
	}
	job.state = web.UpdateStateWaiting
	s.restartCtl.setUpdateHook(func(lock *daemonLock, stderr io.Writer) int {
		return s.install(job, lock, stderr)
	})
	outcome, err := s.restarter.Restart(context.Background(), job.force)
	if err != nil {
		s.restartCtl.setUpdateHook(nil)
		job.state = web.UpdateStateFailed
		job.err = "restart: " + err.Error()
		s.cleanupJob(job)
		return
	}
	if outcome.State == web.DaemonRestartDraining {
		job.drained = true
		return
	}
	// The restart is scheduled; it can no longer be cancelled.
	job.state = web.UpdateStateInstalling
}

// probe runs the new binary in probe mode against a copy of the data
// (REQ-UPD-003 step 3, AC-UPD-008). The live data is not touched.
func (s *updateService) probe(ctx context.Context, binary, version string) error {
	probeHome := s.paths.ProbeHome(version)
	_ = os.RemoveAll(probeHome)
	defer func() { _ = os.RemoveAll(probeHome) }()
	if err := prepareProbeHome(ctx, s.home, probeHome); err != nil {
		return fmt.Errorf("prepare the probe: %w", err)
	}
	probeCtx, cancel := context.WithTimeout(ctx, updateProbeTimeout)
	defer cancel()
	env := append(withoutEnv(os.Environ(), "LANA_HOME", restartLockFDEnv), "LANA_HOME="+probeHome)
	_, stderr, err := s.runner.run(probeCtx, binary, []string{"update-probe", "--home", probeHome}, env)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("the new version failed its check: %v%s", err, stderrTail(stderr, updateProbeStderrLines))
	}
	return nil
}

// prepareProbeHome builds the temp home of the probe: config.toml and
// secrets.env copied, the other folders linked (the probe only reads them),
// and VACUUM INTO copies of every *.db (design.md, probing).
func prepareProbeHome(ctx context.Context, home, probeHome string) error {
	if err := os.MkdirAll(probeHome, 0o700); err != nil {
		return err
	}
	for _, name := range []string{"config.toml", "secrets.env"} {
		src := filepath.Join(home, name)
		if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := selfupdate.CopyFile(src, filepath.Join(probeHome, name), 0o600); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "updates" {
			continue
		}
		if err := os.Symlink(filepath.Join(home, entry.Name()), filepath.Join(probeHome, entry.Name())); err != nil {
			return err
		}
	}
	_, err = selfupdate.CopyDatabases(ctx, home, probeHome)
	return err
}

// install is the update restart hook: back up, write pending.json, replace the
// executable, prune, and exec the new binary (design.md, installing). A
// failure records last_result failed and execs the old binary.
func (s *updateService) install(job *updateJob, lock *daemonLock, stderr io.Writer) int {
	s.mu.Lock()
	job.state = web.UpdateStateInstalling
	s.mu.Unlock()
	exe := s.eligible().ExePath
	to := job.version
	failOld := func(reason string) int {
		_, _ = fmt.Fprintf(stderr, "update: %s; restarting %s\n", reason, s.current)
		s.recordResult(selfupdate.OutcomeFailed, to, reason)
		s.mu.Lock()
		job.state = web.UpdateStateFailed
		job.err = reason
		s.mu.Unlock()
		return execRestart(lock, stderr, exe)
	}
	backupDir, _, err := selfupdate.Backup(context.Background(), selfupdate.BackupOptions{
		Home: s.home, ExePath: exe, From: s.current, To: to, Now: s.now(),
	})
	if err != nil {
		return failOld("backup failed: " + err.Error())
	}
	pending := selfupdate.Pending{
		From: s.current, To: to, ExePath: exe, BackupDir: backupDir, Boots: 0, CreatedAt: s.now().UnixMilli(),
	}
	if err := selfupdate.WritePending(s.paths, pending); err != nil {
		return failOld("write pending.json: " + err.Error())
	}
	if err := selfupdate.ReplaceExecutable(job.download, exe); err != nil {
		_ = selfupdate.RemovePending(s.paths)
		return failOld("replace the binary: " + err.Error())
	}
	if err := selfupdate.Prune(s.paths, selfupdate.KeepBackups); err != nil {
		slog.Warn("update: prune backups", "error", err)
	}
	s.mu.Lock()
	job.state = web.UpdateStateRestarting
	s.mu.Unlock()
	// The watchdog runs from the backup copy of this binary, since exe is the
	// new one now. It rolls back when the new binary dies or hangs before its
	// trial boot reports healthy (design.md, Trial boot and rollback).
	if err := spawnUpdateWatch(s.home, backupDir, os.Getpid(), os.Args[1:]); err != nil {
		_, _ = fmt.Fprintf(stderr, "warning: update: start the update watchdog: %v\n", err)
	}
	_, _ = fmt.Fprintf(stderr, "update: installed %s; restarting\n", to)
	code := execRestart(lock, stderr, exe)
	// The exec failed: put the old binary back and run it.
	if err := selfupdate.CopyFile(filepath.Join(backupDir, "lana"), exe, 0o755); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: update: restore the old binary: %v\n", err)
		_ = selfupdate.RemovePending(s.paths)
		s.recordResult(selfupdate.OutcomeFailed, to, "the new binary did not start and the old one could not be restored: "+err.Error())
		return code
	}
	_ = selfupdate.RemovePending(s.paths)
	return failOld("the new binary could not be started")
}

// recordResult writes last_result for an update from the running version.
func (s *updateService) recordResult(outcome, to, reason string) {
	writeUpdateResult(s.paths, selfupdate.Result{Outcome: outcome, From: s.current, To: to, Reason: reason, At: s.now().UnixMilli()})
}

func writeUpdateResult(paths selfupdate.Paths, result selfupdate.Result) {
	if _, err := selfupdate.UpdateState(paths, func(state *selfupdate.State) { state.LastResult = &result }); err != nil {
		slog.Warn("update: write state.json", "error", err)
	}
}

// versionOutputMatches reports whether `lana --version` output names version
// as one of its words ("lana 0.1.0 (abc, date)").
func versionOutputMatches(output, version string) bool {
	for _, field := range strings.Fields(output) {
		field = strings.Trim(field, "(),")
		if selfupdate.SameVersion(field, version) {
			return true
		}
	}
	return false
}

// stderrTail renders the last n non-empty lines of stderr after a colon and a
// newline, or "" when stderr is empty.
func stderrTail(stderr []byte, n int) string {
	lines := strings.Split(strings.TrimRight(string(stderr), "\n"), "\n")
	var kept []string
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			kept = append(kept, line)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	if len(kept) > n {
		kept = kept[len(kept)-n:]
	}
	return ":\n" + strings.Join(kept, "\n")
}

// withoutEnv drops the named variables from env.
func withoutEnv(env []string, names ...string) []string {
	out := make([]string, 0, len(env))
	for _, item := range env {
		drop := false
		for _, name := range names {
			if strings.HasPrefix(item, name+"=") {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, item)
		}
	}
	return out
}
