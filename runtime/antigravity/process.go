package antigravity

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// acpArgs is the argv after the binary: `agy acp` only. lana never passes
// --dangerously-skip-permissions or --sandbox (REQ-AGY-002, ADR 0022).
var acpArgs = []string{"acp"}

// scrubbedEnv lists the variables lana never passes to agy, so agy always
// uses the account's own Google login and never an API key, Agent Platform
// (Vertex) credentials, or a cloud project (ADR 0022, API-AGY-010).
var scrubbedEnv = []string{
	"GEMINI_API_KEY",
	"GOOGLE_API_KEY",
	"GOOGLE_APPLICATION_CREDENTIALS",
	"GOOGLE_GENAI_USE_VERTEXAI",
	"GOOGLE_CLOUD_PROJECT",
	"GOOGLE_CLOUD_LOCATION",
}

// autoUpdateEnv is pinned on every child so agy never updates itself mid-turn
// (REQ-AGY-002).
const autoUpdateEnv = "AGY_CLI_DISABLE_AUTO_UPDATE=true"

// File-based token storage for added accounts (spec 77 REQ-WRA-013, pending
// the owner's live check: sign an added account out and in again on the
// scratch daemon; no "Keychain Not Found" dialog means it works).
//
// An added account runs agy with its own HOME, which has no macOS login
// Keychain, so a keychain write (sign-in, token refresh) pops "Keychain Not
// Found — A keychain cannot be found to store “antigravity.”". agy 1.5.17's
// changelog: "Fixed the macOS "Keychain Not Found" dialog reappearing about
// once an hour: after a keychain write fails, the CLI now saves your sign-in
// token to its file-based storage for the rest of the session" and "Fixed
// running `agy` over SSH on macOS always bypassing your existing macOS Keychain
// sign-in …; when your login Keychain is unlocked, `agy` now reuses it and
// falls back to file-based token storage when locked." The binary logs "Using
// file-based token storage because %s detected" and reads SSH_CONNECTION,
// SSH_CLIENT and SSH_TTY. Setting SSH_CONNECTION makes agy treat the HOME's
// missing login Keychain as unavailable and use its file-based storage
// without trying a keychain write. The built-in account (the owner's real
// HOME and login Keychain) never gets it.
const (
	// FileTokenStorageEnvName is the variable set for added accounts.
	FileTokenStorageEnvName = "SSH_CONNECTION"
	// FileTokenStorageEnvValue is its value: a loopback SSH connection.
	FileTokenStorageEnvValue = "127.0.0.1 0 127.0.0.1 0"
)

// buildArgs returns the argv after the binary.
func buildArgs() []string { return append([]string(nil), acpArgs...) }

// buildEnv returns the child environment: the daemon environment minus the
// Google API-key and Agent Platform variables, with AGY_CLI_DISABLE_AUTO_UPDATE
// pinned. A configured Home replaces HOME and USERPROFILE (each exactly once),
// so an account's agy finds only its own login, and sets SSH_CONNECTION when
// the daemon environment has none (REQ-WRA-013), so agy keeps that login in
// its file-based storage instead of a Keychain the HOME does not have.
func buildEnv(base []string, cfg core.RuntimeConfig) []string {
	env := make([]string, 0, len(base)+4)
	hasFileStorageEnv := false
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		if name == FileTokenStorageEnvName {
			hasFileStorageEnv = true
		}
		drop := false
		for _, s := range scrubbedEnv {
			if name == s {
				drop = true
				break
			}
		}
		if cfg.Home != "" && (name == "HOME" || name == "USERPROFILE") {
			drop = true
		}
		if !drop {
			env = append(env, kv)
		}
	}
	env = append(env, autoUpdateEnv)
	if cfg.Home != "" {
		env = append(env, "HOME="+cfg.Home, "USERPROFILE="+cfg.Home)
		if !hasFileStorageEnv {
			env = append(env, FileTokenStorageEnvName+"="+FileTokenStorageEnvValue)
		}
	}
	return env
}

// workspaceDir expands a leading ~ and checks that the directory exists.
func workspaceDir(ws string) (string, error) {
	if ws == "" {
		return "", nil
	}
	if ws == "~" || strings.HasPrefix(ws, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("workspace %s: %w", ws, err)
		}
		ws = filepath.Join(home, strings.TrimPrefix(ws, "~"))
	}
	info, err := os.Stat(ws)
	if err != nil {
		return "", fmt.Errorf("workspace %s does not exist", ws)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace %s is not a directory", ws)
	}
	return ws, nil
}

// procState is the ACP session state of one process that outlives a turn.
type procState struct {
	acpSessionID   string
	sessionCreated bool
	mcpServers     []acpMCPServer
	version        string
	tier           string
}

// process is one running `agy acp` child serving one lana session.
type process struct {
	sessionID string
	key       string // config key; a change starts a new process
	dir       string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	writeMu   sync.Mutex
	stderr    *tailBuffer

	state procState

	done    chan struct{} // closed after the process exited
	exitErr error

	mu       sync.Mutex
	turn     *activeTurn
	pending  map[string]chan rpcResponse
	seq      int
	ready    bool // a session/new or session/load completed
	lastUsed time.Time
	idle     *time.Timer
	stopping bool
}

// activeTurn is the state of the turn a process is serving.
type activeTurn struct {
	lines       chan []byte
	done        chan struct{}
	handlerDone chan struct{}
	interrupted bool // guarded by process.mu
	timers      []*time.Timer
	cancelAsks  context.CancelFunc
}

// startProcess starts the binary and its stdout and stderr readers. onExit runs
// after the process exited and its output was read.
func startProcess(bin string, args, env []string, dir, sessionID, key string, onExit func(*process)) (*process, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start Antigravity: %w", err)
	}
	p := &process{
		sessionID: sessionID, key: key, dir: dir, cmd: cmd, stdin: stdin,
		stderr: newTailBuffer(8), done: make(chan struct{}),
		pending: map[string]chan rpcResponse{}, lastUsed: time.Now(),
	}
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		p.stderr.readFrom(stderr)
	}()
	go func() {
		p.readLoop(stdout)
		<-stderrDone
		p.exitErr = cmd.Wait()
		close(p.done)
		if onExit != nil {
			onExit(p)
		}
	}()
	return p, nil
}

func (p *process) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func (p *process) write(line []byte) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err := p.stdin.Write(line)
	return err
}

// signal sends sig to the process group.
func (p *process) signal(sig syscall.Signal) {
	if p.cmd.Process == nil || p.exited() {
		return
	}
	if err := syscall.Kill(-p.cmd.Process.Pid, sig); err != nil {
		_ = p.cmd.Process.Signal(sig)
	}
}

// stop closes stdin and ends the process group: SIGTERM now, SIGKILL after
// killGrace.
func (p *process) stop(killGrace time.Duration) {
	p.mu.Lock()
	if p.stopping {
		p.mu.Unlock()
		return
	}
	p.stopping = true
	if p.idle != nil {
		p.idle.Stop()
	}
	p.mu.Unlock()
	p.writeMu.Lock()
	_ = p.stdin.Close()
	p.writeMu.Unlock()
	go func() {
		p.signal(syscall.SIGTERM)
		select {
		case <-p.done:
		case <-time.After(killGrace):
			p.signal(syscall.SIGKILL)
		}
	}()
}

// crashText is the turn error for a process that exited mid-turn: the last
// stderr line, cut to 300 characters.
func (p *process) crashText() string {
	line := truncate(p.stderr.lastLine(), 300)
	if line != "" {
		return "Antigravity exited: " + line
	}
	if p.exitErr != nil {
		return "Antigravity exited unexpectedly (" + p.exitErr.Error() + ")"
	}
	return "Antigravity exited unexpectedly"
}

// userError returns an error whose text is shown to the owner as is.
func userError(format string, args ...any) error {
	if len(args) == 0 {
		return &TurnError{Message: format}
	}
	return &TurnError{Message: fmt.Sprintf(format, args...)}
}

// truncate cuts s to at most n runes.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// errNotInstalled is the turn error when the binary is missing.
const errNotInstalled = "Antigravity is not installed or not on PATH"
