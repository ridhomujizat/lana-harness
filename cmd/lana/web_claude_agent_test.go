package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

var (
	cmdFakeClaudeOnce sync.Once
	cmdFakeClaudeBin  string
	cmdFakeClaudeErr  error
)

// buildCmdFakeClaude builds the runtime/claudecode fake `claude` once per test
// binary, next to the test's temp dirs.
func buildCmdFakeClaude(t *testing.T) string {
	t.Helper()
	cmdFakeClaudeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "lana-cmd-fakeclaude")
		if err != nil {
			cmdFakeClaudeErr = err
			return
		}
		bin := filepath.Join(dir, "claude")
		out, err := exec.Command("go", "build", "-o", bin, "../../runtime/claudecode/testdata/fakeclaude").CombinedOutput()
		if err != nil {
			cmdFakeClaudeErr = fmt.Errorf("build fakeclaude: %v\n%s", err, out)
			return
		}
		cmdFakeClaudeBin = bin
	})
	if cmdFakeClaudeErr != nil {
		t.Fatal(cmdFakeClaudeErr)
	}
	return cmdFakeClaudeBin
}

// claudeFixture returns the absolute path of a fake claude fixture.
func claudeFixture(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "runtime", "claudecode", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// claudeDaemon is a real in-process daemon (runDaemon) with the web channel on
// and one Claude Agent on the fake binary.
type claudeDaemon struct {
	addr    string
	cookie  *http.Cookie
	csrf    string
	logPath string
}

// startClaudeDaemon starts runDaemon with a web-routed "claude" agent on the
// fake binary replaying fixture. agentExtra is appended to the agent table.
func startClaudeDaemon(t *testing.T, fixture, agentExtra string) *claudeDaemon {
	t.Helper()
	bin := buildCmdFakeClaude(t)
	logPath := filepath.Join(t.TempDir(), "fake.jsonl")
	t.Setenv("FAKECLAUDE_FIXTURE", claudeFixture(t, fixture))
	t.Setenv("FAKECLAUDE_LOG", logPath)
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	addr := freeLoopbackAddr(t)
	cfg := fmt.Sprintf(`[web]
addr = %q
open_browser = false

[agents.claude]
runtime = "claude-code"
binary = %q
workspace = %q
permission = "ask"
model = "haiku"
%s

[[routes]]
channel = "web"
agent = "claude"
`, addr, bin, t.TempDir(), agentExtra)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	writeWebToken(t, home)

	ctx, cancel := context.WithCancel(context.Background())
	stderr := &syncWriter{}
	done := make(chan int, 1)
	go func() { done <- runDaemon(ctx, stderr) }()
	t.Cleanup(func() {
		cancel()
		if code := waitDaemon(t, done); code != 0 {
			t.Errorf("daemon exit = %d, stderr = %s", code, stderr.String())
		}
	})
	if listening := webAddressFromStderr(t, stderr); listening != addr {
		t.Fatalf("listening on %q, want %q", listening, addr)
	}
	cookie, csrf := restartLogin(t, addr, webTestToken)
	return &claudeDaemon{addr: addr, cookie: cookie, csrf: csrf, logPath: logPath}
}

func (d *claudeDaemon) do(t *testing.T, method, target, body string) (int, []byte) {
	t.Helper()
	resp, data, err := restartDo(t, method, d.addr, target, body, d.cookie, d.csrf)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	return resp.StatusCode, data
}

// newSession creates a web session of the claude agent through the API.
func (d *claudeDaemon) newSession(t *testing.T) string {
	t.Helper()
	code, body := d.do(t, http.MethodPost, "/v1/sessions", `{"agent":"claude"}`)
	if code != http.StatusCreated {
		t.Fatalf("create session = %d %s", code, body)
	}
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &session); err != nil || session.ID == "" {
		t.Fatalf("create session body = %s (%v)", body, err)
	}
	return session.ID
}

// stream opens the session's SSE stream as the browser does.
func (d *claudeDaemon) stream(t *testing.T, sessionID string) *bufio.Reader {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+d.addr+"/v1/sessions/"+sessionID+"/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = d.addr
	req.AddCookie(d.cookie)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d", resp.StatusCode)
	}
	return bufio.NewReader(resp.Body)
}

func (d *claudeDaemon) send(t *testing.T, sessionID, text string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"text": text})
	if code, data := d.do(t, http.MethodPost, "/v1/sessions/"+sessionID+"/messages", string(body)); code != http.StatusAccepted {
		t.Fatalf("send = %d %s", code, data)
	}
}

// sseUntil reads SSE blocks until one named name arrives and returns it with
// every block read before it.
func sseUntil(t *testing.T, r *bufio.Reader, name string) (webSSEBlock, []webSSEBlock) {
	t.Helper()
	var seen []webSSEBlock
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		block := nextWebSSE(t, r)
		if block.Name == name {
			return block, seen
		}
		seen = append(seen, block)
	}
	t.Fatalf("no %q SSE event; saw %+v", name, seen)
	return webSSEBlock{}, nil
}

// webApprovalFrame is the data of the approval SSE event (API-WEB-008).
type webApprovalFrame struct {
	RequestID string `json:"request_id"`
	Tool      string `json:"tool"`
	Argument  string `json:"argument"`
	Risk      string `json:"risk"`
	ExpiresAt int64  `json:"expires_at"`
	// Seq and Kind are set only on a session event, never on an approval
	// frame.
	Seq  int64  `json:"seq"`
	Kind string `json:"kind"`
}

func waitApprovalFrame(t *testing.T, r *bufio.Reader) webApprovalFrame {
	t.Helper()
	block, _ := sseUntil(t, r, "approval")
	var frame webApprovalFrame
	if err := json.Unmarshal([]byte(block.Data), &frame); err != nil {
		t.Fatalf("approval data %q: %v", block.Data, err)
	}
	if frame.Seq != 0 || frame.Kind != "" || frame.RequestID == "" || frame.ExpiresAt == 0 {
		t.Fatalf("approval SSE is not a web approval frame: %s", block.Data)
	}
	return frame
}

func waitTurnFrame(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	block, _ := sseUntil(t, r, "turn")
	var turn struct {
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal([]byte(block.Data), &turn); err != nil {
		t.Fatalf("turn data %q: %v", block.Data, err)
	}
	return turn.StopReason
}

// permissionAnswers returns the behavior of every can_use_tool answer lana
// wrote to the fake's stdin, in order.
func permissionAnswers(t *testing.T, logPath string) []string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		var entry struct {
			Kind string `json:"kind"`
			Line struct {
				Type     string `json:"type"`
				Response struct {
					Response struct {
						Behavior string `json:"behavior"`
					} `json:"response"`
				} `json:"response"`
			} `json:"line"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil || entry.Kind != "stdin" || entry.Line.Type != "control_response" {
			continue
		}
		if behavior := entry.Line.Response.Response.Behavior; behavior != "" {
			out = append(out, behavior)
		}
	}
	return out
}

// sessionKinds reads the session's events through the web API.
func (d *claudeDaemon) sessionEvents(t *testing.T, sessionID string) []core.SessionEvent {
	t.Helper()
	code, body := d.do(t, http.MethodGet, "/v1/sessions/"+sessionID+"/events", "")
	if code != http.StatusOK {
		t.Fatalf("events = %d %s", code, body)
	}
	var page struct {
		Events []core.SessionEvent `json:"events"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("events body %s: %v", body, err)
	}
	return page.Events
}

func eventOf[T any](t *testing.T, events []core.SessionEvent, kind string) (T, bool) {
	t.Helper()
	var out T
	found := false
	for _, event := range events {
		if event.Kind != kind {
			continue
		}
		if err := json.Unmarshal(event.Payload, &out); err != nil {
			t.Fatalf("%s payload: %v", kind, err)
		}
		found = true
	}
	return out, found
}

// AC-CCR-005 (web side): a Claude Agent's permission request reaches the
// browser as the same approval frame a native turn sends, and the answer to
// that frame's request_id decides Claude's tool call.
func TestDaemonClaudeAgentWebApproval(t *testing.T) {
	d := startClaudeDaemon(t, "bash.jsonl", "")

	t.Run("allowed", func(t *testing.T) {
		id := d.newSession(t)
		stream := d.stream(t, id)
		d.send(t, id, "list files")
		frame := waitApprovalFrame(t, stream)
		if frame.Tool != "bash" || frame.Argument != "ls" || frame.Risk != "exec" {
			t.Fatalf("approval frame = %+v", frame)
		}
		if code, body := d.do(t, http.MethodPost, "/v1/approvals/"+frame.RequestID, `{"outcome":"allowed_once"}`); code != http.StatusOK {
			t.Fatalf("answer = %d %s", code, body)
		}
		if reason := waitTurnFrame(t, stream); reason != "end_turn" {
			t.Fatalf("turn stop_reason = %q", reason)
		}
		if got := permissionAnswers(t, d.logPath); len(got) == 0 || got[len(got)-1] != "allow" {
			t.Fatalf("permission answers = %v", got)
		}
		events := d.sessionEvents(t, id)
		decided, ok := eventOf[core.ApprovalDecided](t, events, core.KindApprovalDecided)
		if !ok || decided.Outcome != core.OutcomeAllowedOnce {
			t.Fatalf("approval/decided = %+v (found %v)", decided, ok)
		}
		result, ok := eventOf[core.ToolResultPayload](t, events, core.KindToolResult)
		if !ok || result.Status != core.ToolStatusSuccess || !strings.Contains(result.Output, "notes.txt") {
			t.Fatalf("tool/result = %+v (found %v)", result, ok)
		}
		// A second answer to the same frame is a conflict, not a 404.
		if code, body := d.do(t, http.MethodPost, "/v1/approvals/"+frame.RequestID, `{"outcome":"allowed_once"}`); code != http.StatusConflict {
			t.Fatalf("second answer = %d %s", code, body)
		}
	})

	t.Run("rejected", func(t *testing.T) {
		id := d.newSession(t)
		stream := d.stream(t, id)
		d.send(t, id, "list files")
		frame := waitApprovalFrame(t, stream)
		before := len(permissionAnswers(t, d.logPath))
		if code, body := d.do(t, http.MethodPost, "/v1/approvals/"+frame.RequestID, `{"outcome":"rejected"}`); code != http.StatusOK {
			t.Fatalf("answer = %d %s", code, body)
		}
		if reason := waitTurnFrame(t, stream); reason != "end_turn" {
			t.Fatalf("turn stop_reason = %q", reason)
		}
		got := permissionAnswers(t, d.logPath)
		if len(got) != before+1 || got[len(got)-1] != "deny" {
			t.Fatalf("permission answers = %v", got)
		}
		decided, ok := eventOf[core.ApprovalDecided](t, d.sessionEvents(t, id), core.KindApprovalDecided)
		if !ok || decided.Outcome != core.OutcomeRejected {
			t.Fatalf("approval/decided = %+v (found %v)", decided, ok)
		}
	})

	t.Run("replayed on connect", func(t *testing.T) {
		// A browser that opens the session after the request opened gets it
		// from the stream's replay of open approvals.
		id := d.newSession(t)
		d.send(t, id, "list files")
		webWaitFor(t, 10*time.Second, "approval/asked", func() bool {
			_, ok := eventOf[core.ApprovalAsked](t, d.sessionEvents(t, id), core.KindApprovalAsked)
			return ok
		})
		stream := d.stream(t, id)
		frame := waitApprovalFrame(t, stream)
		if code, body := d.do(t, http.MethodPost, "/v1/approvals/"+frame.RequestID, `{"outcome":"allowed_once"}`); code != http.StatusOK {
			t.Fatalf("answer = %d %s", code, body)
		}
		if reason := waitTurnFrame(t, stream); reason != "end_turn" {
			t.Fatalf("turn stop_reason = %q", reason)
		}
	})
}

// AC-CCR-005: an agent deny rule decides a Claude tool call before any owner
// approval, so no approval frame is sent.
func TestDaemonClaudeAgentDenyRule(t *testing.T) {
	d := startClaudeDaemon(t, "bash.jsonl", `rules = { deny = ["bash(ls*)"] }`)
	id := d.newSession(t)
	stream := d.stream(t, id)
	d.send(t, id, "list files")
	block, seen := sseUntil(t, stream, "turn")
	for _, item := range append(seen, block) {
		if item.Name == "approval" {
			t.Fatalf("deny rule sent an approval frame: %s", item.Data)
		}
	}
	if got := permissionAnswers(t, d.logPath); len(got) != 1 || got[0] != "deny" {
		t.Fatalf("permission answers = %v", got)
	}
	events := d.sessionEvents(t, id)
	if _, ok := eventOf[core.ApprovalAsked](t, events, core.KindApprovalAsked); ok {
		t.Fatal("deny rule wrote approval/asked")
	}
	end, ok := eventOf[core.TurnEnd](t, events, core.KindTurnEnd)
	if !ok || end.Reason != "end_turn" {
		t.Fatalf("turn/end = %+v (found %v)", end, ok)
	}
}

// AC-CCR-006: cancelling a Claude Agent turn that waits on a web approval
// closes the approval and ends the turn cancelled.
func TestDaemonClaudeAgentCancelPendingApproval(t *testing.T) {
	d := startClaudeDaemon(t, "bash.jsonl", "")
	id := d.newSession(t)
	stream := d.stream(t, id)
	d.send(t, id, "list files")
	frame := waitApprovalFrame(t, stream)
	if code, body := d.do(t, http.MethodPost, "/v1/sessions/"+id+"/cancel", ""); code != http.StatusAccepted {
		t.Fatalf("cancel = %d %s", code, body)
	}
	if reason := waitTurnFrame(t, stream); reason != "cancelled" {
		t.Fatalf("turn stop_reason = %q", reason)
	}
	events := d.sessionEvents(t, id)
	decided, ok := eventOf[core.ApprovalDecided](t, events, core.KindApprovalDecided)
	if !ok || decided.Outcome != core.OutcomeCancelled {
		t.Fatalf("approval/decided = %+v (found %v)", decided, ok)
	}
	if code, body := d.do(t, http.MethodPost, "/v1/approvals/"+frame.RequestID, `{"outcome":"allowed_once"}`); code != http.StatusConflict {
		t.Fatalf("answer after cancel = %d %s", code, body)
	}
}
