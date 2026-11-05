package toolbox

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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
)

// Job kinds (design "Jobs").
const (
	JobKindInstall   = "install"
	JobKindUninstall = "uninstall"
)

// Job statuses (design "Jobs").
const (
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"
	JobCancelled = "cancelled"
)

// Job tuning constants (design "Jobs").
const (
	jobTimeout        = 15 * time.Minute
	maxJobsPerPackage = 10
	maxDownloadBytes  = 500 << 20
	maxLogBytes       = 64 * 1024
	jobLogsDirMode    = 0o700
	jobLogFileMode    = 0o600
	installedFileMode = 0o755
	installedDirMode  = 0o700
)

// Job is one install or uninstall job (design "Jobs").
type Job struct {
	ID        string `json:"id"`
	Package   string `json:"package"`
	Kind      string `json:"kind"`
	Method    int    `json:"method"`
	Label     string `json:"label"`
	Status    string `json:"status"`
	StartedAt int64  `json:"started_at"`
	EndedAt   int64  `json:"ended_at"`
	Error     string `json:"error"`
}

// jobManager runs at most one job per package, keeps the last jobs in memory
// and streams each job to a log file (REQ-TB-003, REQ-TB-008).
type jobManager struct {
	store    *Store
	logger   *slog.Logger
	onChange func(Job)

	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	jobs   map[string][]Job
	active map[string]string
	wg     sync.WaitGroup
	closed bool
}

// newJobManager builds a job manager over a store. onChange, when set, is
// called after a job finishes with its final snapshot.
func newJobManager(store *Store, logger *slog.Logger, onChange func(Job)) *jobManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &jobManager{
		store:    store,
		logger:   logger,
		onChange: onChange,
		ctx:      ctx,
		cancel:   cancel,
		jobs:     map[string][]Job{},
		active:   map[string]string{},
	}
}

// start begins an install or uninstall job. It returns ErrBusy when a job is
// already running for the package (REQ-TB-003).
func (jm *jobManager) start(m Manifest, kind string, method int) (Job, error) {
	jm.mu.Lock()
	if jm.closed {
		jm.mu.Unlock()
		return Job{}, errors.New("toolbox: service is closed")
	}
	if _, busy := jm.active[m.ID]; busy {
		jm.mu.Unlock()
		return Job{}, ErrBusy
	}
	if err := os.MkdirAll(jm.store.LogDir(m.ID), jobLogsDirMode); err != nil {
		jm.mu.Unlock()
		return Job{}, err
	}
	id := newJobID()
	logPath := filepath.Join(jm.store.LogDir(m.ID), id+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, jobLogFileMode)
	if err != nil {
		jm.mu.Unlock()
		return Job{}, err
	}

	label := "uninstall"
	if kind == JobKindInstall && method >= 0 && method < len(m.Install) {
		label = m.Install[method].Label
	}
	job := Job{
		ID: id, Package: m.ID, Kind: kind, Method: method, Label: label,
		Status: JobRunning, StartedAt: time.Now().UnixMilli(),
	}
	jm.jobs[m.ID] = append([]Job{job}, jm.jobs[m.ID]...)
	jm.active[m.ID] = id
	jm.wg.Add(1)
	jm.mu.Unlock()

	go jm.run(job, m, logFile)
	return job, nil
}

// run executes one job and records its final state.
func (jm *jobManager) run(job Job, m Manifest, logFile *os.File) {
	defer jm.wg.Done()
	defer func() { _ = logFile.Close() }()

	ctx, cancel := context.WithTimeout(jm.ctx, jobTimeout)
	defer cancel()

	var err error
	switch job.Kind {
	case JobKindInstall:
		err = jm.install(ctx, m, job.Method, logFile)
	case JobKindUninstall:
		err = jm.uninstall(ctx, m, logFile)
	default:
		err = fmt.Errorf("toolbox: unknown job kind %q", job.Kind)
	}

	status := JobSucceeded
	errText := ""
	if err != nil {
		errText = err.Error()
		switch {
		case jm.ctx.Err() != nil:
			status = JobCancelled
			errText = "cancelled"
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			status = JobFailed
			errText = "timed out"
		default:
			status = JobFailed
		}
		_, _ = fmt.Fprintf(logFile, "\n[error] %s\n", errText)
	} else {
		_, _ = fmt.Fprintln(logFile, "\n[done]")
	}
	jm.finish(job.ID, m.ID, status, errText)
}

// install runs one install method (REQ-TB-003).
func (jm *jobManager) install(ctx context.Context, m Manifest, method int, log io.Writer) error {
	if method < 0 || method >= len(m.Install) {
		return fmt.Errorf("toolbox: install method %d does not exist", method)
	}
	md := m.Install[method]
	if !methodMatches(md) {
		return ErrMethodMismatch
	}
	switch {
	case strings.TrimSpace(md.Command) != "":
		return jm.runShell(ctx, m, md.Command, log)
	case strings.TrimSpace(md.URL) != "":
		return jm.download(ctx, m, md, log)
	default:
		return errors.New("toolbox: install method has neither command nor url")
	}
}

// uninstall runs the optional uninstall command and removes the bin directory
// contents, keeping data (REQ-TB-008).
func (jm *jobManager) uninstall(ctx context.Context, m Manifest, log io.Writer) error {
	if strings.TrimSpace(m.Uninstall.Command) != "" {
		if err := jm.runShell(ctx, m, m.Uninstall.Command, log); err != nil {
			return err
		}
	}
	return clearDir(jm.store.BinDir(m.ID))
}

// runShell runs a shell command with bash -c in the package home, with the
// package directories in the environment and bin/ first on PATH (REQ-TB-003).
func (jm *jobManager) runShell(ctx context.Context, m Manifest, command string, log io.Writer) error {
	_, _ = fmt.Fprintf(log, "$ %s\n", command)
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = jm.store.Dir(m.ID)
	cmd.Env = shellEnv(jm.store, m)
	cmd.Stdout = log
	cmd.Stderr = log
	return cmd.Run()
}

// download fetches a URL to a temp file, verifies its sha256 when set and
// extracts the binary into bin/ (REQ-TB-003).
func (jm *jobManager) download(ctx context.Context, m Manifest, md InstallMethod, log io.Writer) error {
	_, _ = fmt.Fprintf(log, "downloading %s\n", md.URL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, md.URL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("toolbox: download failed: %s", resp.Status)
	}

	tmp, err := os.CreateTemp("", "toolbox-download-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	defer func() { _ = tmp.Close() }()

	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, hasher), io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		return err
	}
	if written > maxDownloadBytes {
		return fmt.Errorf("toolbox: download exceeds %d bytes", int64(maxDownloadBytes))
	}
	if md.SHA256 != "" {
		got := hex.EncodeToString(hasher.Sum(nil))
		if !strings.EqualFold(got, md.SHA256) {
			return fmt.Errorf("toolbox: sha256 mismatch: got %s want %s", got, md.SHA256)
		}
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(log, "downloaded %d bytes\n", written)
	if err := extract(tmpName, md, jm.store.BinDir(m.ID), m.Detect.Binary); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(log, "installed %s\n", m.Detect.Binary)
	return nil
}

// finish records the final state of a job, prunes the in-memory list and calls
// the change callback. The job stays "running" in the list until the callback
// returns, so a caller that waits for the job also waits for the detection pass
// the callback runs (REQ-TB-002).
func (jm *jobManager) finish(id, pkg, status, errText string) {
	ended := time.Now().UnixMilli()

	jm.mu.Lock()
	updated := Job{ID: id, Package: pkg}
	for _, job := range jm.jobs[pkg] {
		if job.ID == id {
			updated = job
			break
		}
	}
	jm.mu.Unlock()
	updated.Status, updated.EndedAt, updated.Error = status, ended, errText

	if jm.onChange != nil {
		jm.onChange(updated)
	}

	jm.mu.Lock()
	list := jm.jobs[pkg]
	for i := range list {
		if list[i].ID == id {
			list[i].Status, list[i].EndedAt, list[i].Error = status, ended, errText
			break
		}
	}
	if len(list) > maxJobsPerPackage {
		list = list[:maxJobsPerPackage]
	}
	jm.jobs[pkg] = list
	if jm.active[pkg] == id {
		delete(jm.active, pkg)
	}
	jm.mu.Unlock()
}

// list returns a package's jobs, newest first.
func (jm *jobManager) list(pkg string) []Job {
	jm.mu.Lock()
	defer jm.mu.Unlock()
	out := make([]Job, len(jm.jobs[pkg]))
	copy(out, jm.jobs[pkg])
	return out
}

// active reports whether a job is running for the package.
func (jm *jobManager) isActive(pkg string) bool {
	jm.mu.Lock()
	defer jm.mu.Unlock()
	_, ok := jm.active[pkg]
	return ok
}

// log returns the last 64 KiB of a job log and whether the job has finished.
func (jm *jobManager) log(pkg, jobID string) (string, bool, error) {
	jm.mu.Lock()
	list := jm.jobs[pkg]
	running := false
	found := false
	for _, job := range list {
		if job.ID == jobID {
			found = true
			running = job.Status == JobRunning
			break
		}
	}
	jm.mu.Unlock()
	if !found {
		return "", true, ErrNotFound
	}
	text, err := tailFile(filepath.Join(jm.store.LogDir(pkg), jobID+".log"), maxLogBytes)
	if err != nil {
		return "", !running, err
	}
	return text, !running, nil
}

// close cancels the running jobs and waits for them to stop (REQ-TB-003).
func (jm *jobManager) close() {
	jm.mu.Lock()
	if jm.closed {
		jm.mu.Unlock()
		return
	}
	jm.closed = true
	jm.mu.Unlock()
	jm.cancel()
	jm.wg.Wait()
}

// methodMatches reports whether an install method's OS/arch filter matches this
// machine (REQ-TB-003).
func methodMatches(md InstallMethod) bool {
	if md.OS != "" && md.OS != runtime.GOOS {
		return false
	}
	if md.Arch != "" && md.Arch != runtime.GOARCH {
		return false
	}
	return true
}

// shellEnv builds the environment of an install command: the daemon env, PATH
// with bin/ first, and BIN_DIR, PKG_DIR and DATA_DIR (REQ-TB-003).
func shellEnv(store *Store, m Manifest) []string {
	bin := store.BinDir(m.ID)
	base := os.Environ()
	env := make([]string, 0, len(base)+3)
	pathSet := false
	for _, kv := range base {
		if value, ok := strings.CutPrefix(kv, "PATH="); ok {
			env = append(env, "PATH="+bin+string(os.PathListSeparator)+value)
			pathSet = true
			continue
		}
		env = append(env, kv)
	}
	if !pathSet {
		env = append(env, "PATH="+bin)
	}
	env = append(env,
		"BIN_DIR="+bin,
		"PKG_DIR="+store.Dir(m.ID),
		"DATA_DIR="+store.DataDir(m.ID),
	)
	return env
}

// clearDir removes the children of dir, keeping dir itself.
func clearDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// extract writes the package binary from a downloaded file into binDir: a raw
// binary when extract is "", otherwise the named member of a tar.gz or zip
// archive (REQ-TB-003).
func extract(src string, md InstallMethod, binDir, binary string) error {
	if err := os.MkdirAll(binDir, installedDirMode); err != nil {
		return err
	}
	dest := filepath.Join(binDir, binary)
	if md.Extract == "" {
		return copyExecutable(src, dest)
	}
	lower := strings.ToLower(md.URL)
	switch {
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		return extractTarGz(src, md.Extract, dest)
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(src, md.Extract, dest)
	default:
		return fmt.Errorf("toolbox: cannot extract %q from an unknown archive type", md.Extract)
	}
}

// extractTarGz copies one member of a gzipped tar archive to dest.
func extractTarGz(src, want, dest string) error {
	file, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if !header.FileInfo().Mode().IsRegular() {
			continue
		}
		if !memberMatches(header.Name, want) {
			continue
		}
		return writeExecutable(dest, reader)
	}
	return fmt.Errorf("toolbox: archive does not contain %q", want)
}

// extractZip copies one member of a zip archive to dest.
func extractZip(src, want, dest string) error {
	reader, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	for _, file := range reader.File {
		if file.FileInfo().IsDir() || !memberMatches(file.Name, want) {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return err
		}
		err = writeExecutable(dest, rc)
		_ = rc.Close()
		return err
	}
	return fmt.Errorf("toolbox: archive does not contain %q", want)
}

// memberMatches reports whether an archive member is the wanted binary: an
// exact path, or the same base name (REQ-TB-003).
func memberMatches(name, want string) bool {
	if name == want {
		return true
	}
	base := filepath.Base(name)
	return base == want || base == filepath.Base(want)
}

// copyExecutable copies src to dest and makes it executable.
func copyExecutable(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	return writeExecutable(dest, in)
}

// writeExecutable writes r to dest and makes it executable.
func writeExecutable(dest string, r io.Reader) error {
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, installedFileMode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dest, installedFileMode)
}

// newJobID returns a job id: "tool-" plus 16 lowercase hex characters.
func newJobID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("tool-%d", time.Now().UnixNano())
	}
	return "tool-" + hex.EncodeToString(buf)
}

// tailFile returns the last max bytes of a file, or "" when it does not exist.
func tailFile(path string, max int) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	start := int64(0)
	if size := info.Size(); max > 0 && size > int64(max) {
		start = size - int64(max)
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return "", err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	// A byte cut can land inside a rune; drop the leading continuation bytes.
	skip := 0
	for skip < len(data) && data[skip]&0xC0 == 0x80 {
		skip++
	}
	return string(data[skip:]), nil
}
