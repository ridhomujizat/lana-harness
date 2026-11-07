package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// maxLineBytes caps one stdout line. Longer lines are dropped.
const maxLineBytes = 10 << 20

// scrubbedEnv lists the variables lana never passes to Claude Code with
// subscription auth, so Claude Code uses its own login (REQ-CCR-003).
var scrubbedEnv = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_BASE_URL"}

// buildEnv returns the child environment. It never reads Claude credentials:
// subscription auth only removes variables, and api_key auth sets the key from
// the secrets service.
func buildEnv(base []string, cfg core.RuntimeConfig, secrets core.Secrets) ([]string, string, error) {
	env := make([]string, 0, len(base)+1)
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		drop := false
		for _, s := range scrubbedEnv {
			if name == s {
				drop = true
				break
			}
		}
		if !drop {
			env = append(env, kv)
		}
	}
	switch cfg.Auth {
	case "", core.RuntimeAuthSubscription:
		return env, "", nil
	case core.RuntimeAuthAPIKey:
		if cfg.APIKeyEnv == "" {
			return nil, "", errors.New(`auth = "api_key" needs api_key_env`)
		}
		if secrets == nil {
			return nil, "", fmt.Errorf("secret %s is not available", cfg.APIKeyEnv)
		}
		secret, ok := secrets.Lookup(cfg.APIKeyEnv)
		if !ok || secret.Reveal() == "" {
			return nil, "", fmt.Errorf("secret %s is not set", cfg.APIKeyEnv)
		}
		return append(env, "ANTHROPIC_API_KEY="+secret.Reveal()), secret.Reveal(), nil
	default:
		return nil, "", fmt.Errorf("unknown auth %q (use subscription or api_key)", cfg.Auth)
	}
}

// baseArgs are the flags of every Claude process (REQ-CCR-002).
var baseArgs = []string{
	"-p",
	"--input-format", "stream-json",
	"--output-format", "stream-json",
	"--verbose",
	"--include-partial-messages",
	"--permission-prompts", "host",
	"--permission-prompt-tool", "stdio",
}

// buildArgs returns the argv after the binary. resume selects --resume over
// --session-id.
func buildArgs(cfg core.RuntimeConfig, sessionID string, resume bool) ([]string, error) {
	args := append([]string(nil), baseArgs...)
	if cfg.Model != "" {
		if strings.HasPrefix(cfg.Model, "-") {
			return nil, fmt.Errorf("invalid model %q", cfg.Model)
		}
		args = append(args, "--model", cfg.Model)
	}
	if cfg.Effort != "" {
		if strings.HasPrefix(cfg.Effort, "-") {
			return nil, fmt.Errorf("invalid reasoning_effort %q", cfg.Effort)
		}
		args = append(args, "--effort", cfg.Effort)
	}
	if !isUUID(sessionID) {
		return nil, fmt.Errorf("invalid Claude session ID %q", sessionID)
	}
	if resume {
		args = append(args, "--resume", sessionID)
	} else {
		args = append(args, "--session-id", sessionID)
	}
	return args, nil
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isUUID(s string) bool { return uuidRe.MatchString(s) }

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

// process is one running `claude -p` child serving one lana session.
type process struct {
	sessionID string
	key       string // config key; a change starts a new process
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	writeMu   sync.Mutex
	stderr    *tailBuffer
	apiKey    string

	// state is touched only by the goroutine running the current turn.
	state procState

	done    chan struct{} // closed after the process exited
	exitErr error

	mu       sync.Mutex
	turn     *activeTurn
	pending  map[string]chan controlResponseBody
	seq      int
	lastUsed time.Time
	idle     *time.Timer
	stopping bool
}

// activeTurn is the state of the turn a process is serving.
type activeTurn struct {
	lines       chan []byte
	done        chan struct{}
	interrupted bool // guarded by process.mu
	timers      []*time.Timer
	cancelAsks  context.CancelFunc
}

// startProcess starts the binary and its stdout and stderr readers. onExit runs
// after the process exited and its output was read.
func startProcess(bin string, args, env []string, dir, sessionID, key, apiKey string, onExit func(*process)) (*process, error) {
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
		return nil, fmt.Errorf("start Claude Code: %w", err)
	}
	p := &process{
		sessionID: sessionID, key: key, cmd: cmd, stdin: stdin, apiKey: apiKey,
		stderr: newTailBuffer(8), done: make(chan struct{}),
		pending: map[string]chan controlResponseBody{}, lastUsed: time.Now(),
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

func (p *process) readLoop(stdout io.Reader) {
	r := bufio.NewReaderSize(stdout, 64<<10)
	for {
		line, err := readLine(r)
		if len(bytes.TrimSpace(line)) > 0 {
			p.dispatch(line)
		}
		if err != nil {
			return
		}
	}
}

// readLine reads one line without its newline. A line over maxLineBytes is
// dropped and an empty line returned.
func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	tooLong := false
	for {
		chunk, err := r.ReadSlice('\n')
		if !tooLong {
			if len(buf)+len(chunk) > maxLineBytes {
				tooLong, buf = true, nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if tooLong {
			return nil, err
		}
		return bytes.TrimRight(buf, "\r\n"), err
	}
}

// dispatch routes a stdout line: control responses to their waiter, everything
// else to the running turn. Lines outside a turn are dropped.
func (p *process) dispatch(line []byte) {
	var head struct {
		Type     string `json:"type"`
		Response struct {
			RequestID string `json:"request_id"`
		} `json:"response"`
	}
	if json.Unmarshal(line, &head) == nil && head.Type == "control_response" {
		p.mu.Lock()
		ch := p.pending[head.Response.RequestID]
		delete(p.pending, head.Response.RequestID)
		p.mu.Unlock()
		if ch != nil {
			var body struct {
				Response controlResponseBody `json:"response"`
			}
			_ = json.Unmarshal(line, &body)
			ch <- body.Response
		}
		return
	}
	p.mu.Lock()
	t := p.turn
	p.mu.Unlock()
	if t == nil {
		return
	}
	select {
	case t.lines <- line:
	case <-t.done:
	}
}

func (p *process) write(line []byte) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err := p.stdin.Write(line)
	return err
}

func (p *process) nextID(prefix string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seq++
	return "lana_" + prefix + "_" + strconv.Itoa(p.seq)
}

// request sends a control request and waits for its response.
func (p *process) request(ctx context.Context, request map[string]any) (controlResponseBody, error) {
	id := p.nextID(fmt.Sprint(request["subtype"]))
	ch := make(chan controlResponseBody, 1)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
	}()
	if err := p.write(controlRequestLine(id, request)); err != nil {
		return controlResponseBody{}, err
	}
	select {
	case resp := <-ch:
		if resp.Subtype == "error" {
			return resp, fmt.Errorf("claude rejected %v: %s", request["subtype"], resp.Error)
		}
		return resp, nil
	case <-p.done:
		return controlResponseBody{}, errProcessExited
	case <-ctx.Done():
		return controlResponseBody{}, ctx.Err()
	}
}

var errProcessExited = errors.New("claude process exited")

// signal sends sig to the process group.
func (p *process) signal(sig syscall.Signal) {
	if p.cmd.Process == nil || p.exited() {
		return
	}
	if err := syscall.Kill(-p.cmd.Process.Pid, sig); err != nil {
		_ = p.cmd.Process.Signal(sig)
	}
}

// stop ends the process: stdin is closed so Claude Code can exit cleanly, then
// SIGTERM after termAfter and SIGKILL after killAfter more.
func (p *process) stop(termAfter, killAfter time.Duration) {
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
		select {
		case <-p.done:
			return
		case <-time.After(termAfter):
		}
		p.signal(syscall.SIGTERM)
		select {
		case <-p.done:
		case <-time.After(killAfter):
			p.signal(syscall.SIGKILL)
		}
	}()
}

// crashText is the turn error for a process that exited mid-turn: the last
// stderr line, redacted and cut to 300 characters.
func (p *process) crashText() string {
	line := redact(p.stderr.lastLine(), p.apiKey)
	if line != "" {
		return "Claude Code exited: " + truncate(line, 300)
	}
	if p.exitErr != nil {
		return "Claude Code exited unexpectedly (" + p.exitErr.Error() + ")"
	}
	return "Claude Code exited unexpectedly"
}

var (
	redactPatterns = []*regexp.Regexp{
		regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]+`),
		regexp.MustCompile(`sk-[A-Za-z0-9_\-]{16,}`),
		regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._\-]+`),
		regexp.MustCompile(`(?i)((?:api[_-]?key|token|secret|password|authorization)["']?\s*[:=]\s*["']?)[^\s"',}]+`),
		regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`),
	}
)

// redact removes credential-like values, e-mail addresses, and the home
// directory from text that lana stores.
func redact(text, apiKey string) string {
	if text == "" {
		return ""
	}
	if apiKey != "" {
		text = strings.ReplaceAll(text, apiKey, "***")
	}
	for i, re := range redactPatterns {
		switch i {
		case 2, 3:
			text = re.ReplaceAllString(text, "${1}***")
		default:
			text = re.ReplaceAllString(text, "***")
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && home != "/" {
		text = strings.ReplaceAll(text, home, "~")
	}
	return text
}

// tailBuffer keeps the last lines of a stream.
type tailBuffer struct {
	mu    sync.Mutex
	max   int
	lines []string
}

func newTailBuffer(max int) *tailBuffer { return &tailBuffer{max: max} }

func (b *tailBuffer) readFrom(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if len(line) > 2048 {
			line = line[:2048]
		}
		b.mu.Lock()
		b.lines = append(b.lines, line)
		if len(b.lines) > b.max {
			b.lines = b.lines[len(b.lines)-b.max:]
		}
		b.mu.Unlock()
	}
	_, _ = io.Copy(io.Discard, r)
}

func (b *tailBuffer) lastLine() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.lines) == 0 {
		return ""
	}
	return b.lines[len(b.lines)-1]
}
