package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// newTestRegistry returns a registry rooted in a temporary directory. Its
// cleanup kills every job the test started.
func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	reg := New(t.TempDir())
	t.Cleanup(reg.Shutdown)
	return reg
}

func waitForTail(t *testing.T, reg *Registry, sessionID, id, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if tail, err := reg.Tail(sessionID, id, 4096); err == nil && strings.Contains(tail, want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	tail, _ := reg.Tail(sessionID, id, 4096)
	t.Fatalf("job %s did not write %q; tail=%q", id, want, tail)
}

func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func TestStartRunsAndTails(t *testing.T) {
	reg := newTestRegistry(t)
	ws := t.TempDir()
	job, err := reg.Start("s1", ws, "echo ready; sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(job.ID, "bash-") || len(job.ID) != len("bash-")+16 {
		t.Fatalf("id=%q want bash- plus 16 hex", job.ID)
	}
	if job.Status != StatusRunning || job.PID <= 0 {
		t.Fatalf("job=%+v", job)
	}
	if !strings.HasPrefix(job.LogPath, reg.Root) {
		t.Fatalf("log path %q is not under %q", job.LogPath, reg.Root)
	}
	waitForTail(t, reg, "s1", job.ID, "ready", 2*time.Second)
	if !processAlive(job.PID) {
		t.Fatal("job process is not alive")
	}
}

func TestReadNewAdvancesCursor(t *testing.T) {
	reg := newTestRegistry(t)
	job, err := reg.Start("s1", t.TempDir(), "printf one; sleep 0.3; printf two; sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	waitForTail(t, reg, "s1", job.ID, "one", 2*time.Second)
	first, dropped, _, err := reg.ReadNew("s1", job.ID, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if dropped != 0 || first != "one" {
		t.Fatalf("first read=%q dropped=%d", first, dropped)
	}
	waitForTail(t, reg, "s1", job.ID, "two", 2*time.Second)
	second, dropped, _, err := reg.ReadNew("s1", job.ID, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if dropped != 0 || second != "two" {
		t.Fatalf("second read=%q dropped=%d want %q", second, dropped, "two")
	}
}

func TestReadNewDropsOldOutput(t *testing.T) {
	reg := newTestRegistry(t)
	job, err := reg.Start("s1", t.TempDir(), "printf 0123456789; sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	waitForTail(t, reg, "s1", job.ID, "0123456789", 2*time.Second)
	text, dropped, _, err := reg.ReadNew("s1", job.ID, 4)
	if err != nil {
		t.Fatal(err)
	}
	if text != "6789" || dropped != 6 {
		t.Fatalf("read=%q dropped=%d want %q and 6", text, dropped, "6789")
	}
}

func TestTailDoesNotMoveCursor(t *testing.T) {
	reg := newTestRegistry(t)
	job, err := reg.Start("s1", t.TempDir(), "printf 0123456789; sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	waitForTail(t, reg, "s1", job.ID, "0123456789", 2*time.Second)
	tail, err := reg.Tail("s1", job.ID, 4)
	if err != nil {
		t.Fatal(err)
	}
	if tail != "6789" {
		t.Fatalf("tail=%q want %q", tail, "6789")
	}
	text, _, _, err := reg.ReadNew("s1", job.ID, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if text != "0123456789" {
		t.Fatalf("read=%q; Tail moved the cursor", text)
	}
}

func TestKillStopsProcess(t *testing.T) {
	reg := newTestRegistry(t)
	job, err := reg.Start("s1", t.TempDir(), "sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	killed, err := reg.Kill("s1", job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if killed.Status != StatusKilled {
		t.Fatalf("status=%q want killed", killed.Status)
	}
	if killed.EndedAt.IsZero() {
		t.Fatal("EndedAt is zero after kill")
	}
	if processAlive(job.PID) {
		t.Fatalf("process %d is still alive after kill", job.PID)
	}
}

func TestRunningCap(t *testing.T) {
	reg := newTestRegistry(t)
	ws := t.TempDir()
	for i := 0; i < defaultMaxPerSession; i++ {
		if _, err := reg.Start("s1", ws, "sleep 30"); err != nil {
			t.Fatalf("start %d: %v", i+1, err)
		}
	}
	_, err := reg.Start("s1", ws, "sleep 30")
	if err == nil {
		t.Fatal("11th job was accepted")
	}
	if !strings.Contains(err.Error(), "too many running background jobs (10)") || !strings.Contains(err.Error(), "job_kill") {
		t.Fatalf("error=%q", err.Error())
	}
	// Another session has its own cap.
	if _, err := reg.Start("s2", ws, "sleep 30"); err != nil {
		t.Fatalf("other session start: %v", err)
	}
}

func TestOwnershipIsolation(t *testing.T) {
	reg := newTestRegistry(t)
	job, err := reg.Start("s1", t.TempDir(), "sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("s2", job.ID); ok {
		t.Fatal("Get returned another session's job")
	}
	if _, _, _, err := reg.ReadNew("s2", job.ID, 4096); !errors.Is(err, ErrUnknownJob) {
		t.Fatalf("ReadNew err=%v want ErrUnknownJob", err)
	}
	if _, err := reg.Tail("s2", job.ID, 4096); !errors.Is(err, ErrUnknownJob) {
		t.Fatalf("Tail err=%v want ErrUnknownJob", err)
	}
	if _, err := reg.Kill("s2", job.ID); !errors.Is(err, ErrUnknownJob) {
		t.Fatalf("Kill err=%v want ErrUnknownJob", err)
	}
	if got := reg.Wait(context.Background(), "s2", job.ID, time.Second); got.ID != "" {
		t.Fatalf("Wait returned another session's job: %+v", got)
	}
	if ErrUnknownJob.Error() == "" || strings.Contains(ErrUnknownJob.Error(), "s1") {
		t.Fatalf("unknown-job error reveals the other session: %q", ErrUnknownJob.Error())
	}
}

func TestWaitReturnsOnExit(t *testing.T) {
	reg := newTestRegistry(t)
	job, err := reg.Start("s1", t.TempDir(), "sleep 0.2; exit 3")
	if err != nil {
		t.Fatal(err)
	}
	got := reg.Wait(context.Background(), "s1", job.ID, 5*time.Second)
	if got.Status != StatusExited || got.ExitCode != 3 {
		t.Fatalf("wait=%+v want exited 3", got)
	}
}

func TestWaitTimesOutWhileRunning(t *testing.T) {
	reg := newTestRegistry(t)
	job, err := reg.Start("s1", t.TempDir(), "sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	got := reg.Wait(context.Background(), "s1", job.ID, 100*time.Millisecond)
	if got.Status != StatusRunning {
		t.Fatalf("wait=%+v want running", got)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Wait took %v", elapsed)
	}
}

func TestContextLineRunningAndEndedOnce(t *testing.T) {
	reg := newTestRegistry(t)
	running, err := reg.Start("s1", t.TempDir(), "sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	line := reg.ContextLine("s1")
	if !strings.HasPrefix(line, "background jobs: ") || !strings.Contains(line, running.ID+" running (") {
		t.Fatalf("context=%q", line)
	}
	// Running jobs are listed every turn.
	if again := reg.ContextLine("s1"); !strings.Contains(again, running.ID+" running (") {
		t.Fatalf("running job dropped from the next context: %q", again)
	}

	done, err := reg.Start("s1", t.TempDir(), "exit 0")
	if err != nil {
		t.Fatal(err)
	}
	reg.Wait(context.Background(), "s1", done.ID, 5*time.Second)
	first := reg.ContextLine("s1")
	if !strings.Contains(first, done.ID+" exited 0 (") {
		t.Fatalf("ended job missing from context: %q", first)
	}
	second := reg.ContextLine("s1")
	if strings.Contains(second, done.ID) {
		t.Fatalf("ended job repeated in context: %q", second)
	}
	if !strings.Contains(second, running.ID+" running (") {
		t.Fatalf("running job dropped from context: %q", second)
	}
}

func TestKillSessionOnlyTouchesSession(t *testing.T) {
	reg := newTestRegistry(t)
	one, err := reg.Start("s1", t.TempDir(), "sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	other, err := reg.Start("s2", t.TempDir(), "sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	reg.KillSession("s1")
	if processAlive(one.PID) {
		t.Fatalf("s1 job %d is still alive", one.PID)
	}
	if !processAlive(other.PID) {
		t.Fatalf("s2 job %d was killed", other.PID)
	}
	if other, ok := reg.Get("s2", other.ID); !ok || other.Status != StatusRunning {
		t.Fatalf("s2 job=%+v ok=%v", other, ok)
	}
}

func TestShutdownKillsAll(t *testing.T) {
	reg := New(t.TempDir())
	one, err := reg.Start("s1", t.TempDir(), "sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	two, err := reg.Start("s2", t.TempDir(), "sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	reg.Shutdown()
	for _, job := range []*Job{one, two} {
		if processAlive(job.PID) {
			t.Fatalf("job %s (%d) is still alive after Shutdown", job.ID, job.PID)
		}
		got, ok := reg.Get(job.SessionID, job.ID)
		if !ok || got.Status != StatusKilled {
			t.Fatalf("job %s=%+v ok=%v want killed", job.ID, got, ok)
		}
	}
}

// A background job sees the registry's extra PATH dirs (Tools page installs).
func TestStartExtraPath(t *testing.T) {
	reg := newTestRegistry(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "hello-tool"), []byte("#!/bin/sh\necho hello from tool\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	reg.ExtraPath = func() []string { return []string{bin} }
	job, err := reg.Start("s1", t.TempDir(), "hello-tool")
	if err != nil {
		t.Fatal(err)
	}
	waitForTail(t, reg, "s1", job.ID, "hello from tool", 5*time.Second)
}
