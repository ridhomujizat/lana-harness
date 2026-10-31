// Package jobs runs background shell jobs for sessions and tracks their log
// files. It is the in-process registry of REQ-JOB-010..018: jobs live and die
// with the daemon, belong to the session that started them, and write their
// output to a private log file.
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/spill"
)

// Status is the lifecycle state of a job.
type Status string

const (
	StatusRunning Status = "running"
	StatusExited  Status = "exited"
	StatusKilled  Status = "killed"
)

// defaultMaxPerSession is the running-job cap per session (REQ-JOB-011).
const defaultMaxPerSession = 10

// killGrace is how long a kill waits for SIGTERM before SIGKILL (REQ-JOB-014).
const killGrace = 5 * time.Second

// ErrUnknownJob is returned for an id that is unknown or owned by another
// session. The message never reveals the other session (REQ-JOB-011).
var ErrUnknownJob = errors.New("unknown job")

// Job is one background job.
type Job struct {
	ID        string
	SessionID string
	Command   string
	LogPath   string
	PID       int
	StartedAt time.Time
	EndedAt   time.Time
	Status    Status
	ExitCode  int

	// readOff is the job_output cursor; announced marks an ended job already
	// reported in a turn <context> block.
	readOff   int64
	announced bool
	// killed records that the registry sent the kill signal, so the wait
	// goroutine records StatusKilled instead of StatusExited.
	killed bool
}

// Registry tracks background jobs by id.
type Registry struct {
	Root          string
	MaxPerSession int

	mu   sync.Mutex
	jobs map[string]*Job
	// done holds the wait channel per running job, closed when the process
	// exits. It is read and closed under mu.
	done map[string]chan struct{}
}

// New creates a registry whose log files live under root/jobs.
func New(root string) *Registry {
	return &Registry{
		Root:          root,
		MaxPerSession: defaultMaxPerSession,
		jobs:          map[string]*Job{},
		done:          map[string]chan struct{}{},
	}
}

func (r *Registry) maxPerSession() int {
	if r.MaxPerSession > 0 {
		return r.MaxPerSession
	}
	return defaultMaxPerSession
}

func (r *Registry) ensureLocked() {
	if r.jobs == nil {
		r.jobs = map[string]*Job{}
	}
	if r.done == nil {
		r.done = map[string]chan struct{}{}
	}
}

// Start runs command in workspace as a new session (own process group, no
// controlling terminal) and returns the job. stdin is /dev/null; stdout and
// stderr append to a private log file (REQ-JOB-010).
func (r *Registry) Start(sessionID, workspace, command string) (*Job, error) {
	if strings.TrimSpace(command) == "" {
		return nil, errors.New("command is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureLocked()

	running := 0
	for _, job := range r.jobs {
		if job.SessionID == sessionID && job.Status == StatusRunning {
			running++
		}
	}
	if max := r.maxPerSession(); running >= max {
		return nil, fmt.Errorf("too many running background jobs (%d); stop one with job_kill first", max)
	}

	id, err := newID()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(r.Root, "jobs", spill.SessionHash(sessionID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// MkdirAll keeps an existing mode, so enforce the private mode.
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	logPath := filepath.Join(dir, id+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = workspace
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, err
	}

	job := &Job{
		ID:        id,
		SessionID: sessionID,
		Command:   command,
		LogPath:   logPath,
		PID:       cmd.Process.Pid,
		StartedAt: time.Now(),
		Status:    StatusRunning,
	}
	r.jobs[id] = job
	r.done[id] = make(chan struct{})
	go r.reap(id, cmd, logFile)

	snapshot := *job
	return &snapshot, nil
}

// reap waits for one job and records its final state.
func (r *Registry) reap(id string, cmd *exec.Cmd, logFile *os.File) {
	err := cmd.Wait()
	_ = logFile.Close()
	r.mu.Lock()
	defer r.mu.Unlock()
	job := r.jobs[id]
	if job != nil {
		job.EndedAt = time.Now()
		if job.killed {
			job.Status = StatusKilled
		} else {
			job.Status = StatusExited
			job.ExitCode = exitCode(err)
		}
	}
	if done := r.done[id]; done != nil {
		close(done)
	}
	delete(r.done, id)
}

// Get returns a snapshot of one job of the session.
func (r *Registry) Get(sessionID, id string) (*Job, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[id]
	if !ok || job.SessionID != sessionID {
		return nil, false
	}
	snapshot := *job
	return &snapshot, true
}

// List returns the session's jobs, oldest first.
func (r *Registry) List(sessionID string) []Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	sel := r.sessionJobsLocked(sessionID)
	out := make([]Job, 0, len(sel))
	for _, job := range sel {
		out = append(out, *job)
	}
	return out
}

// ReadNew returns the output written since the previous ReadNew of the job, at
// most max bytes. dropped is the number of bytes skipped because the cursor
// fell behind. It also returns the job snapshot (REQ-JOB-012).
func (r *Registry) ReadNew(sessionID, id string, max int) (string, int64, Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[id]
	if !ok || job.SessionID != sessionID {
		return "", 0, Job{}, ErrUnknownJob
	}
	text, dropped, err := readFrom(job.LogPath, job.readOff, max)
	if err != nil {
		return "", 0, *job, err
	}
	job.readOff += int64(len(text)) + dropped
	return text, dropped, *job, nil
}

// Tail returns the last max bytes of the log without moving the cursor.
func (r *Registry) Tail(sessionID, id string, max int) (string, error) {
	r.mu.Lock()
	job, ok := r.jobs[id]
	if !ok || job.SessionID != sessionID {
		r.mu.Unlock()
		return "", ErrUnknownJob
	}
	path := job.LogPath
	r.mu.Unlock()
	return tailOf(path, max)
}

// Wait waits until the job ends, d elapses, or ctx is done, and returns the job
// snapshot. An unknown id returns the zero value (REQ-JOB-012).
func (r *Registry) Wait(ctx context.Context, sessionID, id string, d time.Duration) Job {
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		r.mu.Lock()
		job, ok := r.jobs[id]
		if !ok || job.SessionID != sessionID {
			r.mu.Unlock()
			return Job{}
		}
		if job.Status != StatusRunning {
			snapshot := *job
			r.mu.Unlock()
			return snapshot
		}
		done := r.done[id]
		r.mu.Unlock()
		if done == nil {
			continue
		}
		select {
		case <-done:
		case <-timer.C:
			return r.snapshot(sessionID, id)
		case <-ctx.Done():
			return r.snapshot(sessionID, id)
		}
	}
}

// Kill sends SIGTERM to the job's process group, then SIGKILL after 5 s if it
// is still running, and returns the final job (REQ-JOB-014).
func (r *Registry) Kill(sessionID, id string) (Job, error) {
	r.mu.Lock()
	job, ok := r.jobs[id]
	if !ok || job.SessionID != sessionID {
		r.mu.Unlock()
		return Job{}, ErrUnknownJob
	}
	if job.Status != StatusRunning {
		snapshot := *job
		r.mu.Unlock()
		return snapshot, nil
	}
	job.killed = true
	pid := job.PID
	done := r.done[id]
	r.mu.Unlock()

	if pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGTERM)
	}
	if done == nil {
		return r.snapshot(sessionID, id), nil
	}
	select {
	case <-done:
	case <-time.After(killGrace):
		// Re-check before SIGKILL so a process that exited at the boundary is
		// not killed by a reused pid.
		if r.isRunning(sessionID, id) {
			killGroup(pid)
		}
		<-done
	}
	return r.snapshot(sessionID, id), nil
}

// isRunning reports whether the job is still running.
func (r *Registry) isRunning(sessionID, id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[id]
	return ok && job.SessionID == sessionID && job.Status == StatusRunning
}

// KillSession kills every running job of the session. It keeps the jobs in the
// map and their log files, which stay until the session is deleted
// (REQ-JOB-018).
func (r *Registry) KillSession(sessionID string) {
	for _, id := range r.runningIDs(sessionID) {
		_, _ = r.Kill(sessionID, id)
	}
}

// Shutdown kills every running job's process group. It bounds the wait: SIGTERM
// to all groups, then SIGKILL to the survivors (REQ-JOB-018).
func (r *Registry) Shutdown() {
	type target struct {
		pid  int
		done chan struct{}
	}
	r.mu.Lock()
	targets := make([]target, 0, len(r.jobs))
	for id, job := range r.jobs {
		if job.Status != StatusRunning {
			continue
		}
		job.killed = true
		done := r.done[id]
		if done == nil {
			continue
		}
		targets = append(targets, target{pid: job.PID, done: done})
	}
	r.mu.Unlock()
	if len(targets) == 0 {
		return
	}
	for _, t := range targets {
		if t.pid > 0 {
			_ = syscall.Kill(-t.pid, syscall.SIGTERM)
		}
	}
	reaped := make([]bool, len(targets))
	grace := time.NewTimer(killGrace)
	defer grace.Stop()
	for i, t := range targets {
		select {
		case <-t.done:
			reaped[i] = true
		case <-grace.C:
		}
	}
	for i, t := range targets {
		if !reaped[i] && t.pid > 0 {
			killGroup(t.pid)
		}
	}
	// Give the reaped processes a bounded moment to record their final state.
	settle := time.NewTimer(time.Second)
	defer settle.Stop()
	for i, t := range targets {
		if reaped[i] {
			continue
		}
		select {
		case <-t.done:
			reaped[i] = true
		case <-settle.C:
		}
	}
}

// ContextLine renders the session's jobs for the turn <context> block. Running
// jobs are listed every time; an ended job is listed once, then marked
// announced (REQ-JOB-017).
func (r *Registry) ContextLine(sessionID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var parts []string
	for _, job := range r.sessionJobsLocked(sessionID) {
		switch {
		case job.Status == StatusRunning:
			parts = append(parts, fmt.Sprintf("%s running (%s, %s)", job.ID, cut(job.Command, 60), age(time.Since(job.StartedAt))))
		case !job.announced:
			job.announced = true
			command := cut(job.Command, 60)
			if job.Status == StatusKilled {
				parts = append(parts, fmt.Sprintf("%s killed (%s)", job.ID, command))
			} else {
				parts = append(parts, fmt.Sprintf("%s exited %d (%s)", job.ID, job.ExitCode, command))
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "background jobs: " + strings.Join(parts, "; ")
}

func (r *Registry) snapshot(sessionID, id string) Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[id]
	if !ok || job.SessionID != sessionID {
		return Job{}
	}
	return *job
}

func (r *Registry) runningIDs(sessionID string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ids []string
	for _, job := range r.sessionJobsLocked(sessionID) {
		if job.Status == StatusRunning {
			ids = append(ids, job.ID)
		}
	}
	return ids
}

// sessionJobsLocked returns the session's jobs sorted by start time, then id.
// The caller holds r.mu.
func (r *Registry) sessionJobsLocked(sessionID string) []*Job {
	out := make([]*Job, 0, len(r.jobs))
	for _, job := range r.jobs {
		if job.SessionID == sessionID {
			out = append(out, job)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.Before(out[j].StartedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// newID returns a job id: "bash-" plus 16 lowercase hex characters.
func newID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "bash-" + hex.EncodeToString(buf), nil
}

// readFrom reads from offset, at most max bytes. dropped is the number of bytes
// between offset and the returned text, including a byte a max cut skipped to
// land on a rune boundary.
func readFrom(path string, offset int64, max int) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", 0, nil
		}
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	size := info.Size()
	start := offset
	if start > size {
		start = size
	}
	if max > 0 && size-start > int64(max) {
		start = size - int64(max)
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return "", 0, err
	}
	buf := make([]byte, size-start)
	if _, err := io.ReadFull(f, buf); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", 0, err
	}
	// A max cut can land inside a rune; skip the leading continuation bytes so
	// the returned text is valid UTF-8.
	skip := leadingContinuation(buf)
	dropped := start + int64(skip) - offset
	if dropped < 0 {
		dropped = 0
	}
	return string(buf[skip:]), dropped, nil
}

// tailOf returns the last max bytes of path.
func tailOf(path string, max int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := info.Size()
	start := int64(0)
	if max > 0 && size > int64(max) {
		start = size - int64(max)
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return "", err
	}
	buf := make([]byte, size-start)
	if _, err := io.ReadFull(f, buf); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", err
	}
	return string(buf[leadingContinuation(buf):]), nil
}

// leadingContinuation counts the leading UTF-8 continuation bytes of buf, so a
// byte cut that landed inside a rune is dropped.
func leadingContinuation(buf []byte) int {
	i := 0
	for i < len(buf) && buf[i]&0xC0 == 0x80 {
		i++
	}
	return i
}

// killGroup kills the process group of pid and the process itself.
func killGroup(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// cut returns the first n runes of s.
func cut(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// age renders a duration as "12s", "4m", "3h", or "2d".
func age(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
