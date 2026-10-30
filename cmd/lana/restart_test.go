package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	restartHelperEnv  = "LANA_RESTART_TEST_HELPER"
	restartHelperTest = "TestDaemonRestartHelperProcess"
	restartSecondTest = "TestDaemonRestartSecondStart"
	restartTokenMark  = "; copy it now, it is shown only once: "
)

// TestDaemonRestartHelperProcess is the daemon side of the restart test. It
// runs runDaemon in a child process; an in-place restart re-executes this same
// test binary with the same arguments.
func TestDaemonRestartHelperProcess(t *testing.T) {
	if os.Getenv(restartHelperEnv) != "daemon" {
		return
	}
	os.Exit(runDaemon(context.Background(), os.Stderr))
}

// TestDaemonRestartSecondStart is a second `lana daemon` start. It prints the
// exit code and stderr so the parent can assert the lock was held.
func TestDaemonRestartSecondStart(t *testing.T) {
	if os.Getenv(restartHelperEnv) != "second" {
		return
	}
	var stderr bytes.Buffer
	code := runDaemon(context.Background(), &stderr)
	_, _ = fmt.Fprintf(os.Stdout, "exit %d\n%s", code, stderr.String())
	os.Exit(0)
}

// restartChild is a daemon child whose stderr is read line by line.
type restartChild struct {
	cmd   *exec.Cmd
	lines chan string

	mu  sync.Mutex
	buf bytes.Buffer
}

func startRestartChild(t *testing.T, home string) *restartChild {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+restartHelperTest+"$")
	cmd.Env = append(os.Environ(), restartHelperEnv+"=daemon", "LANA_HOME="+home)
	cmd.Stdout = io.Discard
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon child: %v", err)
	}
	child := &restartChild{cmd: cmd, lines: make(chan string, 64)}
	go func() {
		defer close(child.lines)
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			child.mu.Lock()
			child.buf.WriteString(line)
			child.buf.WriteByte('\n')
			child.mu.Unlock()
			child.lines <- line
		}
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return child
}

func (c *restartChild) stderr() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// waitLine returns the value after prefix from the next matching stderr line.
func (c *restartChild) waitLine(t *testing.T, prefix string, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case line, ok := <-c.lines:
			if !ok {
				t.Fatalf("daemon child exited before %q: %s", prefix, c.stderr())
			}
			if strings.HasPrefix(line, prefix) {
				return strings.TrimSpace(strings.TrimPrefix(line, prefix))
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q: %s", prefix, c.stderr())
		}
	}
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return addr
}

func writeRestartConfig(t *testing.T, home, addr string) {
	t.Helper()
	cfg := fmt.Sprintf("[web]\naddr = %q\n", addr)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

type restartDaemonInfo struct {
	PID              int   `json:"pid"`
	StartedAt        int64 `json:"started_at"`
	UptimeMS         int64 `json:"uptime_ms"`
	RestartSupported bool  `json:"restart_supported"`
}

func restartHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{DisableKeepAlives: true},
	}
}

func restartDo(t *testing.T, method, addr, target, body string, cookie *http.Cookie, csrf string) (*http.Response, []byte, error) {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+addr+target, strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Host = addr
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set("X-Lana-CSRF", csrf)
	}
	resp, err := restartHTTPClient().Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp, data, nil
}

func restartLogin(t *testing.T, addr, token string) (*http.Cookie, string) {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		t.Fatal(err)
	}
	resp, body, err := restartDo(t, http.MethodPost, addr, "/v1/auth/login", string(payload), nil, "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login status = %d, body = %q", resp.StatusCode, body)
	}
	var cookie *http.Cookie
	for _, candidate := range resp.Cookies() {
		if candidate.Name == "lana_session" {
			cookie = candidate
		}
	}
	if cookie == nil {
		t.Fatalf("login set no session cookie: %v", resp.Cookies())
	}

	resp, body, err = restartDo(t, http.MethodGet, addr, "/v1/auth/session", "", cookie, "")
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session status = %d, body = %q", resp.StatusCode, body)
	}
	var session struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(body, &session); err != nil {
		t.Fatalf("session body %q: %v", body, err)
	}
	return cookie, session.CSRF
}

// tryRestartDaemonInfo reads GET /v1/daemon, tolerating the gap while the
// listener restarts.
func tryRestartDaemonInfo(addr string, cookie *http.Cookie) (restartDaemonInfo, error) {
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/v1/daemon", nil)
	if err != nil {
		return restartDaemonInfo{}, err
	}
	req.Host = addr
	req.AddCookie(cookie)
	resp, err := restartHTTPClient().Do(req)
	if err != nil {
		return restartDaemonInfo{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return restartDaemonInfo{}, fmt.Errorf("status %d", resp.StatusCode)
	}
	var info restartDaemonInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return restartDaemonInfo{}, err
	}
	return info, nil
}

// TestDaemonRestartInPlace covers AC-WEB-038: the child re-executes itself with
// the same PID and keeps the daemon lock, and GET /v1/daemon reports a new
// started_at.
func TestDaemonRestartInPlace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("restart in place is not supported on Windows")
	}
	home := t.TempDir()
	addr := freeLoopbackAddr(t)
	writeRestartConfig(t, home, addr)

	child := startRestartChild(t, home)
	tokenLine := child.waitLine(t, "web: owner token created at ", 10*time.Second)
	i := strings.Index(tokenLine, restartTokenMark)
	if i < 0 {
		t.Fatalf("token line has no marker: %q", tokenLine)
	}
	token := tokenLine[i+len(restartTokenMark):]
	if listening := child.waitLine(t, "web: listening on http://", 10*time.Second); listening != addr {
		t.Fatalf("listening address = %q, want %q", listening, addr)
	}

	pid := child.cmd.Process.Pid
	cookie, csrf := restartLogin(t, addr, token)
	before, err := tryRestartDaemonInfo(addr, cookie)
	if err != nil {
		t.Fatalf("daemon info before restart: %v", err)
	}
	if before.PID != pid {
		t.Fatalf("pid before restart = %d, want %d", before.PID, pid)
	}
	if !before.RestartSupported {
		t.Fatal("restart_supported = false")
	}

	resp, body, err := restartDo(t, http.MethodPost, addr, "/v1/daemon/restart", `{"confirm":true}`, cookie, csrf)
	if err != nil {
		t.Fatalf("restart request: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("restart status = %d, body = %q, want 202", resp.StatusCode, body)
	}

	var after restartDaemonInfo
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		info, err := tryRestartDaemonInfo(addr, cookie)
		if err == nil && info.StartedAt != before.StartedAt {
			after = info
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if after.StartedAt == before.StartedAt {
		t.Fatalf("started_at did not change: before=%d after=%d stderr=%s", before.StartedAt, after.StartedAt, child.stderr())
	}
	if after.PID != pid {
		t.Fatalf("pid after restart = %d, want %d", after.PID, pid)
	}

	// The lock is still held: a second daemon start is refused.
	second := exec.Command(os.Args[0], "-test.run=^"+restartSecondTest+"$")
	second.Env = append(os.Environ(), restartHelperEnv+"=second", "LANA_HOME="+home)
	out, err := second.CombinedOutput()
	if err != nil {
		t.Fatalf("second start: %v, output=%s", err, out)
	}
	if !strings.Contains(string(out), "daemon already running") {
		t.Fatalf("second start output = %q, want daemon already running", out)
	}
}

// TestDaemonStatusAdapter covers the daemon side of API-WEB-022: the adapter
// reports the process, its home and start time, and whether restart is wired.
func TestDaemonStatusAdapter(t *testing.T) {
	home := t.TempDir()
	backend := &webBackend{home: home, startedAt: time.Now()}

	info := backend.DaemonInfo()
	if info.PID != os.Getpid() {
		t.Fatalf("pid = %d, want %d", info.PID, os.Getpid())
	}
	if info.Home != home || info.StartedAt == 0 {
		t.Fatalf("home/started_at = %q/%d", info.Home, info.StartedAt)
	}
	if info.RestartSupported {
		t.Fatal("restart_supported = true without a restart controller")
	}
	if info.RunningTurns == nil {
		t.Fatal("running_turns is nil, want an empty list")
	}
	if _, err := backend.Restart(context.Background(), false); err == nil {
		t.Fatal("Restart without a controller returned nil")
	}

	lock, err := acquireDaemonLock(t.TempDir())
	if err != nil {
		t.Fatalf("acquire test lock: %v", err)
	}
	defer lock.release()
	backend.SetRestartController(newRestartController(lock))
	if info := backend.DaemonInfo(); !info.RestartSupported {
		t.Fatal("restart_supported = false with a restart controller")
	}
}

// TestRestartScheduleDelays covers the "202 before the restart" rule: the
// controller does not request the restart until the response delay passes, and
// a lost lock descriptor fails the preflight.
func TestRestartScheduleDelays(t *testing.T) {
	lock, err := acquireDaemonLock(t.TempDir())
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	controller := newRestartController(lock)
	if err := controller.schedule(); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	select {
	case <-controller.requested():
		t.Fatal("restart requested before the response delay")
	default:
	}
	select {
	case <-controller.requested():
	case <-time.After(2 * time.Second):
		t.Fatal("restart was not requested after the delay")
	}

	lock.release()
	if err := controller.schedule(); err == nil {
		t.Fatal("schedule with a lost lock descriptor returned nil")
	}
}
