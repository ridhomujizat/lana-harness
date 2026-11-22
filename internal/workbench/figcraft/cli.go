package figcraft

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// defaultAppPlist is the FigCraft app Info.plist (REQ-FV-002).
const defaultAppPlist = "/Applications/FigCraft.app/Contents/Info.plist"

// cliTimeout is how long one figcraft-cli run may take (REQ-FV-003). It is a
// variable so a test can shorten it.
var cliTimeout = 30 * time.Second

// cli resolves the figcraft-cli path and version, cached for 60 s keyed by the
// CLIPath setting, so a settings change takes effect (REQ-FV-001). None found is
// a *CLIMissingError.
func (s *Service) cli(ctx context.Context) (string, string, error) {
	setting := s.settings().CLIPath

	s.mu.Lock()
	if s.cliKey == setting && !s.cliAt.IsZero() && s.clock().Sub(s.cliAt) < cliTTL {
		path, ver, missing := s.cliPath, s.cliVer, s.cliMissing
		s.mu.Unlock()
		if missing != nil {
			return "", "", missing
		}
		return path, ver, nil
	}
	s.mu.Unlock()

	path, ver, err := s.findCLI(ctx, setting)

	s.mu.Lock()
	s.cliKey = setting
	s.cliAt = s.clock()
	s.cliPath, s.cliVer = path, ver
	s.cliMissing = nil
	var missing *CLIMissingError
	if errors.As(err, &missing) {
		s.cliMissing = missing
	}
	s.mu.Unlock()
	return path, ver, err
}

// findCLI looks for the CLI in the order of REQ-FV-001: the cli_path setting,
// the per-user install, then PATH. It returns a *CLIMissingError with every path
// it tried when none is an existing executable regular file.
func (s *Service) findCLI(ctx context.Context, setting string) (string, string, error) {
	home := filepath.Join(s.opts.HomeDir, ".local", "share", "figcraft-cli", "figcraft-cli")
	var tried []string

	if setting != "" {
		tried = append(tried, setting)
		if executableRegular(setting) {
			return setting, s.cliVersion(ctx, setting), nil
		}
	}
	tried = append(tried, home)
	if executableRegular(home) {
		return home, s.cliVersion(ctx, home), nil
	}
	if found, err := s.opts.LookPath("figcraft-cli"); err == nil && found != "" {
		tried = append(tried, found)
		if executableRegular(found) {
			return found, s.cliVersion(ctx, found), nil
		}
	} else {
		tried = append(tried, "figcraft-cli")
	}
	return "", "", &CLIMissingError{Tried: tried}
}

// cliVersion runs "<cli> --version" and returns the last whitespace-separated
// token, so "figcraft-cli X.Y.Z" yields "X.Y.Z" (REQ-FV-001). A failure logs a
// warning and yields "".
func (s *Service) cliVersion(ctx context.Context, path string) string {
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(runCtx, path, "--version").Output()
	if err != nil {
		s.log.Warn("figcraft cli version failed", "path", path, "error", err)
		return ""
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

// appVersion is the FigCraft app version, cached for 60 s (REQ-FV-002).
func (s *Service) appVersion() string {
	s.mu.Lock()
	if !s.appAt.IsZero() && s.clock().Sub(s.appAt) < cliTTL {
		v := s.appVer
		s.mu.Unlock()
		return v
	}
	s.mu.Unlock()

	v := s.appVerFn()

	s.mu.Lock()
	s.appAt = s.clock()
	s.appVer = v
	s.mu.Unlock()
	return v
}

// defaultAppVersion reads CFBundleShortVersionString from the FigCraft app
// Info.plist on macOS with plutil; elsewhere, or on any error, it is ""
// (REQ-FV-002).
func defaultAppVersion() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "plutil", "-extract", "CFBundleShortVersionString", "raw", defaultAppPlist).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// run runs the CLI for one request (REQ-FV-003): no shell, the automation roots
// for the file folder and the run temp folder, at most maxConcurrent processes
// and the run timeout. It returns the standard output and how long the process
// took. A run the timeout killed is a *FailedError with code "figcraft_timeout";
// a non-zero exit is one with "figcraft_failed" and the first stderr line.
func (s *Service) run(ctx context.Context, abs, tmp string, args []string) ([]byte, time.Duration, error) {
	path, _, err := s.cli(ctx)
	if err != nil {
		return nil, 0, err
	}

	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	}
	defer func() { <-s.sem }()

	runCtx, cancel := context.WithTimeout(ctx, cliTimeout)
	defer cancel()

	full := make([]string, 0, len(args)+6)
	full = append(full, args...)
	full = append(full,
		"--automation-read-root", filepath.Dir(abs),
		"--automation-read-root", tmp,
		"--automation-write-root", tmp,
	)

	cmd := exec.CommandContext(runCtx, path, full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	action := "run"
	if len(args) > 0 {
		action = args[0]
	}
	start := s.clock()
	runErr := cmd.Run()
	took := s.clock().Sub(start)

	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		s.log.Warn("figcraft cli timeout", "file", abs, "action", action, "duration", took)
		return nil, took, &FailedError{Code: "figcraft_timeout", Message: firstLine(stderr.String())}
	case runErr != nil:
		s.log.Warn("figcraft cli failed", "file", abs, "action", action, "duration", took, "error", runErr)
		return nil, took, &FailedError{Code: "figcraft_failed", Message: firstLine(stderr.String())}
	case runCtx.Err() != nil:
		return nil, took, runCtx.Err()
	}
	s.log.Debug("figcraft cli run", "file", abs, "action", action, "duration", took)
	return stdout.Bytes(), took, nil
}

// newRunDir makes a private run temp folder under TempDir with prefix
// "lana-figcraft-" and returns a cleanup that removes it (REQ-FV-003).
func (s *Service) newRunDir() (string, func(), error) {
	root := s.opts.TempDir
	if root == "" {
		root = os.TempDir()
	}
	dir, err := os.MkdirTemp(root, "lana-figcraft-")
	if err != nil {
		return "", nil, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

// executableRegular reports whether path is an existing executable regular file
// (REQ-FV-001).
func executableRegular(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !info.Mode().IsRegular() {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}

// firstLine is the first non-empty line of s, trimmed, or "" when there is none.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
