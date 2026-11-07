package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/selfupdate"
	"github.com/ridhomujizat/lana-harness/store"
	"github.com/ridhomujizat/lana-harness/web"
)

// fakeReleases is a releaseLister with a fixed answer.
type fakeReleases struct {
	mu       sync.Mutex
	releases []selfupdate.Release
	err      error
	calls    int
}

func (f *fakeReleases) Releases(context.Context) ([]selfupdate.Release, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.releases, f.err
}

// fakeRunner answers `--version` and `update-probe` without a process.
type fakeRunner struct {
	mu           sync.Mutex
	version      string
	probeErr     error
	probeStderr  string
	probeEnv     []string
	probeHome    string
	probeEntries []string
	calls        []string
}

func (f *fakeRunner) run(_ context.Context, name string, args, env []string) ([]byte, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.Join(append([]string{filepath.Base(name)}, args...), " "))
	switch {
	case len(args) == 1 && args[0] == "--version":
		return []byte("lana " + f.version + " (abc1234, 2026-11-08T00:00:00Z)\n"), nil, nil
	case len(args) > 0 && args[0] == "update-probe":
		f.probeEnv = env
		f.probeHome = args[2]
		entries, _ := os.ReadDir(args[2])
		for _, entry := range entries {
			f.probeEntries = append(f.probeEntries, entry.Name())
		}
		return nil, []byte(f.probeStderr), f.probeErr
	}
	return nil, nil, nil
}

// fakeRestarter records the restart calls of the waiting step.
type fakeRestarter struct {
	mu       sync.Mutex
	outcome  web.DaemonRestartOutcome
	err      error
	forces   []bool
	cancel   bool
	cancels  int
	draining bool
}

func (f *fakeRestarter) Restart(_ context.Context, force bool) (web.DaemonRestartOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forces = append(f.forces, force)
	if f.err != nil {
		return web.DaemonRestartOutcome{}, f.err
	}
	if f.outcome.State == web.DaemonRestartDraining {
		f.draining = true
	}
	return f.outcome, nil
}

func (f *fakeRestarter) CancelRestart(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancels++
	if f.cancel {
		f.draining = false
	}
	return f.cancel, nil
}

func (f *fakeRestarter) drainStatus() drainStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.draining {
		return drainStatus{}
	}
	return drainStatus{State: drainDraining, Reason: drainRestart, Running: 2, Deadline: time.UnixMilli(1762600060000)}
}

// updateFixture is a home, a binary, and a fake GitHub serving one release.
type updateFixture struct {
	home     string
	exe      string
	server   *httptest.Server
	payload  []byte
	releases *fakeReleases
	runner   *fakeRunner
	restart  *fakeRestarter
	ctl      *restartController
	svc      *updateService
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func newUpdateFixture(t *testing.T, current string, sumsFor []byte) *updateFixture {
	t.Helper()
	f := &updateFixture{home: t.TempDir(), payload: []byte("new-binary-0.1.0")}
	if sumsFor == nil {
		sumsFor = f.payload
	}
	asset := selfupdate.AssetName("linux", "amd64")
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bin":
			_, _ = w.Write(f.payload)
		case "/sums":
			_, _ = fmt.Fprintf(w, "%s  %s\n", sha256Hex(sumsFor), asset)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	if err := os.WriteFile(filepath.Join(f.home, "config.toml"), []byte("# config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), filepath.Join(f.home, "lana.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	if err := os.MkdirAll(filepath.Join(f.home, "agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	f.exe = filepath.Join(t.TempDir(), "lana")
	if err := os.WriteFile(f.exe, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.releases = &fakeReleases{releases: []selfupdate.Release{
		{
			Version: mustUpdateVersion(t, "0.1.0"), Tag: "v0.1.0", Name: "lana 0.1.0", Notes: "## Upgrade notes\n- none",
			Assets: []selfupdate.Asset{{Name: asset, URL: f.server.URL + "/bin", Size: int64(len(f.payload))}, {Name: selfupdate.SumsAsset, URL: f.server.URL + "/sums"}},
		},
		{
			Version: mustUpdateVersion(t, "0.2.0-beta.1"), Tag: "v0.2.0-beta.1", Prerelease: true,
			Assets: []selfupdate.Asset{{Name: asset, URL: f.server.URL + "/bin"}, {Name: selfupdate.SumsAsset, URL: f.server.URL + "/sums"}},
		},
	}}
	f.runner = &fakeRunner{version: "0.1.0"}
	f.restart = &fakeRestarter{outcome: web.DaemonRestartOutcome{State: web.DaemonRestartScheduled}}
	f.ctl = newRestartController(nil)
	exe := f.exe
	f.svc = newUpdateService(updateServiceOptions{
		Home:     f.home,
		Current:  current,
		GOOS:     "linux",
		GOARCH:   "amd64",
		Config:   core.UpdateConfig{Channel: "stable", Check: true},
		Releases: f.releases,
		HTTP:     f.server.Client(),
		Eligibility: func() selfupdate.Eligibility {
			return selfupdate.Eligibility{Supported: true, ExePath: exe, CanApply: true}
		},
		Runner:     f.runner,
		Restarter:  f.restart,
		RestartCtl: f.ctl,
		Drain:      f.restart.drainStatus,
		Root:       context.Background(),
	})
	return f
}

func mustUpdateVersion(t *testing.T, s string) selfupdate.Version {
	t.Helper()
	v, ok := selfupdate.ParseVersion(s)
	if !ok {
		t.Fatalf("bad version %q", s)
	}
	return v
}

func waitJob(t *testing.T, svc *updateService, states ...string) web.UpdateStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status := svc.UpdateStatus(context.Background())
		if status.Job != nil {
			for _, state := range states {
				if status.Job.State == state {
					return status
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	status := svc.UpdateStatus(context.Background())
	t.Fatalf("job = %+v, want one of %v", status.Job, states)
	return status
}

// errExecStub stands for an exec that replaced the process image.
var errExecStub = errors.New("exec stub")

// stubExec replaces execRestart for the test and records its paths.
func stubExec(t *testing.T, codes ...int) *[]string {
	t.Helper()
	var calls []string
	old := execRestart
	execRestart = func(_ *daemonLock, _ io.Writer, path string) int {
		calls = append(calls, path)
		if len(codes) > 0 {
			code := codes[0]
			codes = codes[1:]
			return code
		}
		return 0
	}
	t.Cleanup(func() { execRestart = old })
	return &calls
}

func homeListing(t *testing.T, home string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(home, path)
		// The home folder itself changes when updates/ is created.
		if rel == "." || rel == "updates" || strings.HasPrefix(rel, "updates"+string(filepath.Separator)) {
			return nil
		}
		out = append(out, fmt.Sprintf("%s %d %d", rel, info.Size(), info.ModTime().UnixNano()))
		return nil
	})
	sort.Strings(out)
	return out
}

// TestUpdateCheck covers AC-UPD-001, AC-UPD-002, and AC-UPD-004 through the
// service, and a channel change through UpdateSettings (API-UPD-008).
func TestUpdateCheck(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0-beta.1", nil)
	status := f.svc.UpdateStatus(context.Background())
	if status.CheckedAt != nil || status.Available != nil || !status.Supported || !status.CanApply || status.ManualCommand != selfupdate.ManualCommand {
		t.Fatalf("initial status = %+v", status)
	}
	if f.releases.calls != 0 {
		t.Fatal("GET /v1/update called GitHub")
	}
	status, err := f.svc.CheckUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Available == nil || status.Available.Version != "0.1.0" || !status.Available.UpgradeNotes || status.CheckedAt == nil {
		t.Fatalf("stable available = %+v", status.Available)
	}
	if status.Available.Size != int64(len(f.payload)) {
		t.Fatalf("size = %d", status.Available.Size)
	}

	// AC-UPD-004: GitHub down keeps the last result and reports the error.
	f.releases.err = errors.New("dial tcp: connection refused")
	status, err = f.svc.CheckUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Available == nil || !strings.Contains(status.CheckError, "connection refused") {
		t.Fatalf("after failure = %+v", status)
	}
	f.releases.err = nil

	// Switching to beta re-checks and offers the prerelease.
	var wrote []string
	f.svc.writeConfig = func(_ context.Context, channel *string, check *bool) error {
		wrote = append(wrote, *channel)
		return nil
	}
	beta := "beta"
	status, err = f.svc.UpdateSettings(context.Background(), &beta, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status.Channel != "beta" || status.Available == nil || status.Available.Version != "0.2.0-beta.1" || status.CheckError != "" {
		t.Fatalf("beta status = %+v / %+v", status, status.Available)
	}
	if len(wrote) != 1 {
		t.Fatalf("config writes = %v", wrote)
	}
}

// TestUpdateDevBuild: a dev build never checks and is not supported
// (REQ-UPD-001, API-UPD-002).
func TestUpdateDevBuild(t *testing.T) {
	f := newUpdateFixture(t, "dev", nil)
	status, err := f.svc.CheckUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Supported || status.CanApply || status.ManualReason == "" || status.Available != nil || status.Job != nil {
		t.Fatalf("dev status = %+v", status)
	}
	if f.releases.calls != 0 {
		t.Fatal("dev build called GitHub")
	}
	if _, err := f.svc.ApplyUpdate(context.Background(), "0.1.0", false); !errors.Is(err, web.ErrUpdateUnavailable) {
		t.Fatalf("apply err = %v", err)
	}
	// The periodic loop returns at once.
	done := make(chan struct{})
	go func() { f.svc.runPeriodic(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runPeriodic did not return for a dev build")
	}
}

// TestUpdatePeriodic covers the periodic check of REQ-UPD-001 with short
// timings, and that check=false skips it.
func TestUpdatePeriodic(t *testing.T) {
	oldFirst, oldEvery := updateFirstCheckDelay, updateCheckInterval
	updateFirstCheckDelay, updateCheckInterval = 5*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { updateFirstCheckDelay, updateCheckInterval = oldFirst, oldEvery })
	f := newUpdateFixture(t, "0.1.0-beta.1", nil)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { f.svc.runPeriodic(ctx); close(stopped) }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.releases.mu.Lock()
		calls := f.releases.calls
		f.releases.mu.Unlock()
		if calls >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-stopped
	if f.releases.calls < 2 {
		t.Fatalf("periodic checks = %d", f.releases.calls)
	}

	g := newUpdateFixture(t, "0.1.0-beta.1", nil)
	g.svc.applyConfig(core.UpdateConfig{Channel: "stable", Check: false})
	ctx, cancel = context.WithCancel(context.Background())
	stopped = make(chan struct{})
	go func() { g.svc.runPeriodic(ctx); close(stopped) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-stopped
	g.releases.mu.Lock()
	defer g.releases.mu.Unlock()
	if g.releases.calls != 0 {
		t.Fatalf("checks with check=false = %d", g.releases.calls)
	}
}

// TestUpdateJobInstalls covers the whole job: download, verify, probe, waiting
// (scheduled), and the restart hook that backs up, writes pending.json,
// replaces the binary, and execs it (REQ-UPD-003).
func TestUpdateJobInstalls(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0-beta.1", nil)
	if _, err := f.svc.CheckUpdate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ApplyUpdate(context.Background(), "0.2.0", false); !errors.Is(err, web.ErrUpdateInvalidState) {
		t.Fatalf("wrong version err = %v", err)
	}
	status, err := f.svc.ApplyUpdate(context.Background(), "0.1.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if status.Job == nil || status.Job.State != web.UpdateStateDownloading || status.Job.Progress == nil {
		t.Fatalf("job = %+v", status.Job)
	}
	if _, err := f.svc.CheckUpdate(context.Background()); err != nil && !errors.Is(err, web.ErrUpdateInvalidState) {
		t.Fatalf("check during job err = %v", err)
	}
	waitJob(t, f.svc, web.UpdateStateInstalling)
	if _, err := f.svc.CheckUpdate(context.Background()); !errors.Is(err, web.ErrUpdateInvalidState) {
		t.Fatalf("check while installing err = %v", err)
	}
	if _, err := f.svc.ApplyUpdate(context.Background(), "0.1.0", true); !errors.Is(err, web.ErrUpdateInvalidState) {
		t.Fatalf("second apply err = %v", err)
	}
	if cancelled, _ := f.svc.CancelUpdate(context.Background()); cancelled {
		t.Fatal("a scheduled restart was cancelled")
	}
	if len(f.restart.forces) != 1 || f.restart.forces[0] {
		t.Fatalf("restart calls = %v", f.restart.forces)
	}
	// The probe ran with LANA_HOME set to a copy holding config and the db.
	f.runner.mu.Lock()
	probeHome, entries, env := f.runner.probeHome, strings.Join(f.runner.probeEntries, ","), f.runner.probeEnv
	f.runner.mu.Unlock()
	if !strings.Contains(entries, "config.toml") || !strings.Contains(entries, "lana.db") || !strings.Contains(entries, "agents") {
		t.Fatalf("probe home entries = %s", entries)
	}
	foundEnv := false
	for _, item := range env {
		if item == "LANA_HOME="+probeHome {
			foundEnv = true
		}
	}
	if !foundEnv {
		t.Fatal("probe ran without LANA_HOME of the probe home")
	}
	if _, err := os.Stat(probeHome); !os.IsNotExist(err) {
		t.Fatalf("probe home left behind: %v", err)
	}

	// A real exec never returns; the stub panics to stop the hook there.
	var calls []string
	oldExec := execRestart
	execRestart = func(_ *daemonLock, _ io.Writer, path string) int {
		calls = append(calls, path)
		panic(errExecStub)
	}
	t.Cleanup(func() { execRestart = oldExec })
	hook := f.ctl.takeUpdateHook()
	if hook == nil {
		t.Fatal("no update hook set")
	}
	var stderr bytes.Buffer
	func() {
		defer func() {
			if r := recover(); r != errExecStub {
				t.Fatalf("hook did not exec: %v, stderr = %s", r, stderr.String())
			}
		}()
		hook(nil, &stderr)
	}()
	if len(calls) != 1 || calls[0] != f.exe {
		t.Fatalf("exec calls = %v, want [%s]", calls, f.exe)
	}
	if data, _ := os.ReadFile(f.exe); string(data) != string(f.payload) {
		t.Fatalf("exe = %q", data)
	}
	pending, err := selfupdate.ReadPending(selfupdate.Paths{Home: f.home})
	if err != nil || pending == nil || pending.To != "0.1.0" || pending.From != "0.1.0-beta.1" || pending.Boots != 0 || pending.ExePath != f.exe {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	manifest, err := selfupdate.ReadManifest(pending.BackupDir)
	if err != nil || strings.Join(manifest.Files, ",") != "lana.db,config.toml,lana" {
		t.Fatalf("manifest = %+v, %v", manifest, err)
	}
	if data, _ := os.ReadFile(filepath.Join(pending.BackupDir, "lana")); string(data) != "old-binary" {
		t.Fatalf("backed-up binary = %q", data)
	}
	if got := f.svc.UpdateStatus(context.Background()).Job.State; got != web.UpdateStateRestarting {
		t.Fatalf("state = %s", got)
	}
}

// TestUpdateJobCorruptDownload covers AC-UPD-007.
func TestUpdateJobCorruptDownload(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0-beta.1", []byte("something else"))
	if _, err := f.svc.CheckUpdate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ApplyUpdate(context.Background(), "0.1.0", false); err != nil {
		t.Fatal(err)
	}
	status := waitJob(t, f.svc, web.UpdateStateFailed)
	if !strings.Contains(status.Job.Error, "checksum mismatch") {
		t.Fatalf("error = %q", status.Job.Error)
	}
	if f.ctl.takeUpdateHook() != nil || len(f.restart.forces) != 0 {
		t.Fatal("a failed job asked for a restart")
	}
	if data, _ := os.ReadFile(f.exe); string(data) != "old-binary" {
		t.Fatalf("exe = %q", data)
	}
	if _, err := os.Stat(selfupdate.Paths{Home: f.home}.Download("0.1.0")); !os.IsNotExist(err) {
		t.Fatalf("download kept: %v", err)
	}
	// A failed job does not block the next attempt.
	f.payload = []byte("something else")
	if _, err := f.svc.ApplyUpdate(context.Background(), "0.1.0", false); err != nil {
		t.Fatalf("retry: %v", err)
	}
	waitJob(t, f.svc, web.UpdateStateInstalling, web.UpdateStateFailed)
}

// TestUpdateJobWrongVersion: the new binary must report the target version.
func TestUpdateJobWrongVersion(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0-beta.1", nil)
	f.runner.version = "0.1.0-beta.1"
	if _, err := f.svc.CheckUpdate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ApplyUpdate(context.Background(), "0.1.0", false); err != nil {
		t.Fatal(err)
	}
	status := waitJob(t, f.svc, web.UpdateStateFailed)
	if !strings.Contains(status.Job.Error, "want version 0.1.0") {
		t.Fatalf("error = %q", status.Job.Error)
	}
}

// TestUpdateJobProbeFails covers AC-UPD-008: the job fails at checking with
// the last stderr lines, and nothing in $LANA_HOME except updates/ changed.
func TestUpdateJobProbeFails(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0-beta.1", nil)
	var lines []string
	for i := 1; i <= 30; i++ {
		lines = append(lines, fmt.Sprintf("probe line %02d", i))
	}
	f.runner.probeErr = errors.New("exit status 2")
	f.runner.probeStderr = strings.Join(lines, "\n") + "\n"
	if _, err := f.svc.CheckUpdate(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := homeListing(t, f.home)
	if _, err := f.svc.ApplyUpdate(context.Background(), "0.1.0", false); err != nil {
		t.Fatal(err)
	}
	status := waitJob(t, f.svc, web.UpdateStateFailed)
	if !strings.Contains(status.Job.Error, "probe line 30") || !strings.Contains(status.Job.Error, "probe line 11") || strings.Contains(status.Job.Error, "probe line 10") {
		t.Fatalf("error = %q", status.Job.Error)
	}
	if after := homeListing(t, f.home); strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("home changed:\nbefore %v\nafter  %v", before, after)
	}
	if f.ctl.takeUpdateHook() != nil {
		t.Fatal("hook set after a failed probe")
	}
}

// TestUpdateJobWaiting covers AC-UPD-009 and the API-UPD-005 force rule: the
// job waits for 2 turns, "Interrupt and update" repeats the apply with force,
// and cancel cancels the drain (API-UPD-006).
func TestUpdateJobWaiting(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0-beta.1", nil)
	f.restart.outcome = web.DaemonRestartOutcome{State: web.DaemonRestartDraining, RunningTurns: 2, Deadline: 1762600060000}
	f.restart.cancel = true
	if _, err := f.svc.CheckUpdate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ApplyUpdate(context.Background(), "0.1.0", false); err != nil {
		t.Fatal(err)
	}
	status := waitJob(t, f.svc, web.UpdateStateWaiting)
	if status.Job.RunningTurns != 2 || status.Job.Deadline == nil || *status.Job.Deadline != 1762600060000 || status.Job.Progress != nil {
		t.Fatalf("waiting job = %+v", status.Job)
	}

	// A plain apply while waiting is invalid_state; a different version too.
	if _, err := f.svc.ApplyUpdate(context.Background(), "0.1.0", false); !errors.Is(err, web.ErrUpdateInvalidState) {
		t.Fatalf("apply without force err = %v", err)
	}
	if _, err := f.svc.ApplyUpdate(context.Background(), "0.2.0", true); !errors.Is(err, web.ErrUpdateInvalidState) {
		t.Fatalf("apply other version err = %v", err)
	}
	// Interrupt and update: force=true reaches the restart path.
	status, err := f.svc.ApplyUpdate(context.Background(), "0.1.0", true)
	if err != nil {
		t.Fatal(err)
	}
	if status.Job == nil || status.Job.State != web.UpdateStateWaiting {
		t.Fatalf("force status job = %+v", status.Job)
	}
	if len(f.restart.forces) != 2 || f.restart.forces[0] || !f.restart.forces[1] {
		t.Fatalf("restart forces = %v", f.restart.forces)
	}

	cancelled, err := f.svc.CancelUpdate(context.Background())
	if err != nil || !cancelled || f.restart.cancels != 1 {
		t.Fatalf("cancel = %v, %v, cancels = %d", cancelled, err, f.restart.cancels)
	}
	if got := f.svc.UpdateStatus(context.Background()).Job.State; got != web.UpdateStateCancelled {
		t.Fatalf("state = %s", got)
	}
	if f.ctl.takeUpdateHook() != nil {
		t.Fatal("hook kept after cancel")
	}
	if cancelled, _ := f.svc.CancelUpdate(context.Background()); cancelled {
		t.Fatal("second cancel reported true")
	}
}

// TestUpdateJobWaitingCancelledElsewhere: a restart drain cancelled from the
// daemon card cancels the waiting job.
func TestUpdateJobWaitingCancelledElsewhere(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0-beta.1", nil)
	f.restart.outcome = web.DaemonRestartOutcome{State: web.DaemonRestartDraining, RunningTurns: 1}
	if _, err := f.svc.CheckUpdate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ApplyUpdate(context.Background(), "0.1.0", false); err != nil {
		t.Fatal(err)
	}
	waitJob(t, f.svc, web.UpdateStateWaiting)
	f.restart.mu.Lock()
	f.restart.draining = false
	f.restart.mu.Unlock()
	if got := f.svc.UpdateStatus(context.Background()).Job.State; got != web.UpdateStateCancelled {
		t.Fatalf("state = %s", got)
	}
	if f.ctl.takeUpdateHook() != nil {
		t.Fatal("hook kept")
	}
}

// TestUpdateCancelDownloading: cancel during downloading ends the job and
// keeps the old binary (API-UPD-006).
func TestUpdateCancelDownloading(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0-beta.1", nil)
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer close(release)
	f.releases.releases[0].Assets[0].URL = slow.URL + "/bin"
	if _, err := f.svc.CheckUpdate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ApplyUpdate(context.Background(), "0.1.0", false); err != nil {
		t.Fatal(err)
	}
	cancelled, err := f.svc.CancelUpdate(context.Background())
	if err != nil || !cancelled {
		t.Fatalf("cancel = %v, %v", cancelled, err)
	}
	if got := f.svc.UpdateStatus(context.Background()).Job.State; got != web.UpdateStateCancelled {
		t.Fatalf("state = %s", got)
	}
	time.Sleep(50 * time.Millisecond)
	if got := f.svc.UpdateStatus(context.Background()).Job.State; got != web.UpdateStateCancelled {
		t.Fatalf("state after the goroutine = %s", got)
	}
	if len(f.restart.forces) != 0 {
		t.Fatal("cancelled job restarted")
	}
}

// TestUpdateInstallFailureRunsOldBinary: a failed backup records last_result
// failed and execs the old binary (design.md, installing).
func TestUpdateInstallFailureRunsOldBinary(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0-beta.1", nil)
	if _, err := f.svc.CheckUpdate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ApplyUpdate(context.Background(), "0.1.0", false); err != nil {
		t.Fatal(err)
	}
	waitJob(t, f.svc, web.UpdateStateInstalling)
	// The binary disappears, so the backup cannot copy it.
	if err := os.Remove(f.exe); err != nil {
		t.Fatal(err)
	}
	calls := stubExec(t)
	var stderr bytes.Buffer
	f.ctl.takeUpdateHook()(nil, &stderr)
	if len(*calls) != 1 || (*calls)[0] != f.exe {
		t.Fatalf("exec calls = %v", *calls)
	}
	paths := selfupdate.Paths{Home: f.home}
	if pending, _ := selfupdate.ReadPending(paths); pending != nil {
		t.Fatalf("pending left: %+v", pending)
	}
	state, _ := selfupdate.ReadState(paths)
	if state.LastResult == nil || state.LastResult.Outcome != selfupdate.OutcomeFailed || !strings.Contains(state.LastResult.Reason, "backup") {
		t.Fatalf("last_result = %+v", state.LastResult)
	}
	backups, _ := selfupdate.ListBackups(paths)
	if len(backups) != 0 {
		t.Fatalf("partial backup left: %+v", backups)
	}
}

// TestUpdateInstallExecFailure: when the new binary cannot be exec'd, the old
// binary is put back and started.
func TestUpdateInstallExecFailure(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0-beta.1", nil)
	if _, err := f.svc.CheckUpdate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ApplyUpdate(context.Background(), "0.1.0", false); err != nil {
		t.Fatal(err)
	}
	waitJob(t, f.svc, web.UpdateStateInstalling)
	calls := stubExec(t, 1, 0)
	var stderr bytes.Buffer
	if code := f.ctl.takeUpdateHook()(nil, &stderr); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if len(*calls) != 2 {
		t.Fatalf("exec calls = %v", *calls)
	}
	if data, _ := os.ReadFile(f.exe); string(data) != "old-binary" {
		t.Fatalf("exe = %q", data)
	}
	paths := selfupdate.Paths{Home: f.home}
	if pending, _ := selfupdate.ReadPending(paths); pending != nil {
		t.Fatalf("pending left: %+v", pending)
	}
	if state, _ := selfupdate.ReadState(paths); state.LastResult == nil || state.LastResult.Outcome != selfupdate.OutcomeFailed {
		t.Fatalf("last_result = %+v", state.LastResult)
	}
}

// TestRestartExecHookOnlyForUpdate: a plain restart execs the same binary; the
// install hook runs only when an update job set it (design.md).
func TestRestartExecHookOnlyForUpdate(t *testing.T) {
	calls := stubExec(t)
	ctl := newRestartController(nil)
	if code := restartExec(ctl, nil, io.Discard); code != 0 || len(*calls) != 1 || (*calls)[0] != "" {
		t.Fatalf("plain restart: code=%d calls=%v", code, *calls)
	}
	hooked := 0
	ctl.setUpdateHook(func(*daemonLock, io.Writer) int { hooked++; return 7 })
	if code := restartExec(ctl, nil, io.Discard); code != 7 || hooked != 1 || len(*calls) != 1 {
		t.Fatalf("update restart: code=%d hooked=%d calls=%v", code, hooked, *calls)
	}
	// The hook is used once.
	if code := restartExec(ctl, nil, io.Discard); code != 0 || hooked != 1 || len(*calls) != 2 {
		t.Fatalf("after update restart: hooked=%d calls=%v", hooked, *calls)
	}
}

// TestTrialDecision covers design.md, Trial boot steps 1-3.
func TestTrialDecision(t *testing.T) {
	cases := []struct {
		name    string
		pending *selfupdate.Pending
		running string
		want    trialAction
	}{
		{"absent", nil, "0.1.0", trialNone},
		{"old binary", &selfupdate.Pending{From: "0.1.0-beta.1", To: "0.1.0"}, "0.1.0-beta.1", trialStale},
		{"first boot", &selfupdate.Pending{To: "0.1.0"}, "0.1.0", trialStart},
		{"first boot v prefix", &selfupdate.Pending{To: "v0.1.0"}, "0.1.0", trialStart},
		{"never healthy", &selfupdate.Pending{To: "0.1.0", Boots: 1}, "0.1.0", trialRollback},
	}
	for _, tc := range cases {
		if got := trialDecision(tc.pending, tc.running); got != tc.want {
			t.Fatalf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestTrialBootHealthy: a trial start writes boots=1; healthy deletes
// pending.json and records updated (design.md step 5).
func TestTrialBootHealthy(t *testing.T) {
	paths := selfupdate.Paths{Home: t.TempDir()}
	if err := selfupdate.WritePending(paths, selfupdate.Pending{From: "0.1.0-beta.1", To: "0.1.0"}); err != nil {
		t.Fatal(err)
	}
	trial, _, action := beginTrialBoot(paths, "0.1.0", time.Now)
	if action != trialStart || !trial.active() {
		t.Fatalf("action = %d", action)
	}
	pending, _ := selfupdate.ReadPending(paths)
	if pending == nil || pending.Boots != 1 {
		t.Fatalf("pending = %+v", pending)
	}
	trial.markHealthy()
	trial.markHealthy()
	if trial.active() {
		t.Fatal("still active")
	}
	if pending, _ := selfupdate.ReadPending(paths); pending != nil {
		t.Fatalf("pending left: %+v", pending)
	}
	state, _ := selfupdate.ReadState(paths)
	if state.LastResult == nil || state.LastResult.Outcome != selfupdate.OutcomeUpdated || state.LastResult.To != "0.1.0" {
		t.Fatalf("last_result = %+v", state.LastResult)
	}

	// The old binary after a failed install drops a stale pending.json.
	_ = selfupdate.WritePending(paths, selfupdate.Pending{From: "0.1.0-beta.1", To: "0.1.0"})
	if trial, _, action := beginTrialBoot(paths, "0.1.0-beta.1", time.Now); trial != nil || action != trialStale {
		t.Fatalf("stale = %v %d", trial, action)
	}
	if pending, _ := selfupdate.ReadPending(paths); pending != nil {
		t.Fatal("stale pending.json kept")
	}
	var nilTrial *trialBoot
	nilTrial.markHealthy()
	if nilTrial.active() {
		t.Fatal("nil trial active")
	}
}

// trialHome prepares a home after an install: a backup of the old state and
// pending.json for "0.1.0", with the live data changed by the new version.
func trialHome(t *testing.T, boots int, liveConfig string) (string, string, selfupdate.Pending) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[web]\nenabled = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), filepath.Join(home, "lana.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	exe := filepath.Join(t.TempDir(), "lana")
	if err := os.WriteFile(exe, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir, _, err := selfupdate.Backup(context.Background(), selfupdate.BackupOptions{Home: home, ExePath: exe, From: "0.1.0-beta.1", To: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	pending := selfupdate.Pending{From: "0.1.0-beta.1", To: "0.1.0", ExePath: exe, BackupDir: dir, Boots: boots}
	if err := selfupdate.WritePending(selfupdate.Paths{Home: home}, pending); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("new-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(liveConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, exe, pending
}

func setVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

// TestTrialBootRollbackOnStartError covers AC-UPD-011: the new binary exits
// with a config error, and the daemon restores the backup and execs the old
// binary.
func TestTrialBootRollbackOnStartError(t *testing.T) {
	setVersion(t, "0.1.0")
	home, exe, _ := trialHome(t, 0, "[update]\nchannel = \"nightly\"\n")
	calls := stubExec(t)
	var stderr bytes.Buffer
	code := runDaemon(context.Background(), &stderr)
	if code != 0 || len(*calls) != 1 || (*calls)[0] != exe {
		t.Fatalf("code = %d, exec calls = %v, stderr = %s", code, *calls, stderr.String())
	}
	if data, _ := os.ReadFile(exe); string(data) != "old-binary" {
		t.Fatalf("exe = %q", data)
	}
	if data, _ := os.ReadFile(filepath.Join(home, "config.toml")); string(data) != "[web]\nenabled = false\n" {
		t.Fatalf("config = %q", data)
	}
	paths := selfupdate.Paths{Home: home}
	if pending, _ := selfupdate.ReadPending(paths); pending != nil {
		t.Fatalf("pending left: %+v", pending)
	}
	state, _ := selfupdate.ReadState(paths)
	if state.LastResult == nil || state.LastResult.Outcome != selfupdate.OutcomeRolledBack || !strings.Contains(state.LastResult.Reason, "update.channel") {
		t.Fatalf("last_result = %+v", state.LastResult)
	}
}

// TestTrialBootRollbackAfterKill covers AC-UPD-012: a trial boot that never
// reported healthy is rolled back on the next start, before the config loads.
func TestTrialBootRollbackAfterKill(t *testing.T) {
	setVersion(t, "0.1.0")
	home, exe, _ := trialHome(t, 1, "[web]\nenabled = false\n# new\n")
	calls := stubExec(t)
	var stderr bytes.Buffer
	if code := runDaemon(context.Background(), &stderr); code != 0 || len(*calls) != 1 || (*calls)[0] != exe {
		t.Fatalf("code = %d, calls = %v, stderr = %s", code, *calls, stderr.String())
	}
	if data, _ := os.ReadFile(exe); string(data) != "old-binary" {
		t.Fatalf("exe = %q", data)
	}
	state, _ := selfupdate.ReadState(selfupdate.Paths{Home: home})
	if state.LastResult == nil || state.LastResult.Outcome != selfupdate.OutcomeRolledBack || !strings.Contains(state.LastResult.Reason, "never reported healthy") {
		t.Fatalf("last_result = %+v", state.LastResult)
	}
}

// TestUpdateRollbackCommand covers `lana update rollback` (API-UPD-010).
func TestUpdateRollbackCommand(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := runUpdateRollback(home, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "no update backup") {
		t.Fatalf("no backup: code=%d stderr=%s", code, stderr.String())
	}
	_, exe, _ := trialHome(t, 1, "# new\n")
	home = os.Getenv("LANA_HOME")

	lock, err := acquireDaemonLock(home)
	if err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if code := runUpdateRollback(home, &stdout, &stderr); code != 3 {
		t.Fatalf("daemon running: code=%d stderr=%s", code, stderr.String())
	}
	lock.release()

	stderr.Reset()
	if code := runUpdateRollback(home, &stdout, &stderr); code != 0 {
		t.Fatalf("rollback: code=%d stderr=%s", code, stderr.String())
	}
	if data, _ := os.ReadFile(exe); string(data) != "old-binary" {
		t.Fatalf("exe = %q", data)
	}
	if data, _ := os.ReadFile(filepath.Join(home, "config.toml")); string(data) != "[web]\nenabled = false\n" {
		t.Fatalf("config = %q", data)
	}
	if pending, _ := selfupdate.ReadPending(selfupdate.Paths{Home: home}); pending != nil {
		t.Fatal("pending left")
	}
}

// TestUpdateProbe covers `lana update-probe --home <dir>` (API-UPD-010).
func TestUpdateProbe(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[web]\nenabled = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := runUpdateProbe(context.Background(), home, &stderr); code != 0 {
		t.Fatalf("empty home: code=%d stderr=%s", code, stderr.String())
	}
	// A lana.db is opened and migrated; an unknown db is checked.
	if err := os.WriteFile(filepath.Join(home, "lana.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "knowledge.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runUpdateProbe(context.Background(), home, &stderr); code != 0 {
		t.Fatalf("with dbs: code=%d stderr=%s", code, stderr.String())
	}
	if info, err := os.Stat(filepath.Join(home, "lana.db")); err != nil || info.Size() == 0 {
		t.Fatalf("lana.db not migrated: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "other.db"), []byte("this is not a sqlite database file at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if code := runUpdateProbe(context.Background(), home, &stderr); code == 0 || !strings.Contains(stderr.String(), "other.db") {
		t.Fatalf("broken db: code=%d stderr=%s", code, stderr.String())
	}
	_ = os.Remove(filepath.Join(home, "other.db"))
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[update]\nchannel = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if code := runUpdateProbe(context.Background(), home, &stderr); code == 0 || !strings.Contains(stderr.String(), `update.channel: must be "stable" or "beta"`) {
		t.Fatalf("bad config: code=%d stderr=%s", code, stderr.String())
	}
}

// TestWriteUpdateConfig covers the [update] writer of API-UPD-008: it appends
// the table, replaces keys in place, keeps other bytes, and moves the loaded
// version so the restart banner stays off.
func TestWriteUpdateConfig(t *testing.T) {
	home := t.TempDir()
	original := "# my config\n[web]\nenabled = false\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := &webBackend{home: home, loadedSHA256: configFileSHA(home)}
	beta, off := "beta", false
	if err := backend.writeUpdateConfig(context.Background(), &beta, &off); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(home, "config.toml"))
	if !strings.HasPrefix(string(data), original) || !strings.Contains(string(data), "[update]\nchannel = \"beta\"\ncheck = false\n") {
		t.Fatalf("config = %q", data)
	}
	if backend.ConfigLoadedVersion() != configFileSHA(home) {
		t.Fatal("loaded version did not move")
	}
	stable := "stable"
	if err := backend.writeUpdateConfig(context.Background(), &stable, nil); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(home, "config.toml"))
	if !strings.Contains(string(data), "channel = \"stable\"\ncheck = false\n") || strings.Count(string(data), "[update]") != 1 {
		t.Fatalf("config = %q", data)
	}
	bad := "nightly"
	if err := backend.writeUpdateConfig(context.Background(), &bad, nil); err == nil {
		t.Fatal("invalid channel written")
	}

	// A pending restart-only change keeps the banner: the loaded version
	// does not move.
	backend.loadedSHA256 = "something-else"
	if err := backend.writeUpdateConfig(context.Background(), &beta, nil); err != nil {
		t.Fatal(err)
	}
	if backend.ConfigLoadedVersion() != "something-else" {
		t.Fatal("loaded version moved over a pending change")
	}
}
