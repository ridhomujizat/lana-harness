package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

const (
	webLeaseHelperEnv  = "LANA_WEB_LEASE_HELPER"
	webLeaseSessionEnv = "LANA_WEB_LEASE_SESSION"
	webLeaseHelperTest = "TestWebLeaseHelperProcess"
)

// TestWebLeaseHelperProcess is the re-executed child of the cross-process
// tests. It opens its own runtime on LANA_HOME, holds the session lease, and
// runs scripted turns when the parent writes a "run" line to stdin.
func TestWebLeaseHelperProcess(t *testing.T) {
	if os.Getenv(webLeaseHelperEnv) != "1" {
		return
	}
	if err := runWebLeaseHelper(); err != nil {
		fmt.Fprintln(os.Stderr, "web lease helper:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func runWebLeaseHelper() error {
	home := os.Getenv("LANA_HOME")
	sessionID := os.Getenv(webLeaseSessionEnv)
	if home == "" || sessionID == "" {
		return fmt.Errorf("LANA_HOME and %s must be set", webLeaseSessionEnv)
	}
	text, stop := "child event", "end_turn"
	rt, err := lana.New(context.Background(), lana.Options{
		Home:   home,
		Layers: []config.Layer{{Name: "helper", TOML: []byte(webTestLayer)}},
		Providers: map[string]core.Provider{"mock": mock.New(mock.Script{Responses: []mock.Response{
			{Events: []mock.Event{{Text: &text}, {Stop: &stop}}},
		}})},
	})
	if err != nil {
		return err
	}
	defer func() { _ = rt.Close() }()

	release, err := rt.LeaseSession(context.Background(), sessionID)
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	fmt.Printf("held %d\n", os.Getpid())

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case line == "stop":
			return nil
		case strings.HasPrefix(line, "run "):
			prompt := strings.TrimSpace(strings.TrimPrefix(line, "run "))
			result, err := rt.Run(context.Background(), lana.Request{Prompt: prompt, SessionID: sessionID})
			if err != nil {
				fmt.Printf("error %v\n", err)
				continue
			}
			fmt.Printf("turn %s\n", result.StopReason)
		}
	}
	return scanner.Err()
}

// webLeaseChild is a helper process holding a session lease.
type webLeaseChild struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan string
	stderr *syncWriter
}

func startWebLeaseChild(t *testing.T, home, sessionID string) *webLeaseChild {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+webLeaseHelperTest+"$")
	cmd.Env = append(os.Environ(),
		webLeaseHelperEnv+"=1",
		"LANA_HOME="+home,
		webLeaseSessionEnv+"="+sessionID,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("helper stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("helper stdout: %v", err)
	}
	stderr := &syncWriter{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	child := &webLeaseChild{cmd: cmd, stdin: stdin, lines: make(chan string, 16), stderr: stderr}
	go func() {
		defer close(child.lines)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			child.lines <- scanner.Text()
		}
	}()
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return child
}

func (c *webLeaseChild) send(t *testing.T, line string) {
	t.Helper()
	if _, err := fmt.Fprintln(c.stdin, line); err != nil {
		t.Fatalf("write to helper: %v", err)
	}
}

func (c *webLeaseChild) waitLine(t *testing.T, prefix string) string {
	t.Helper()
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case line, ok := <-c.lines:
			if !ok {
				t.Fatalf("helper exited before %q: %s", prefix, c.stderr.String())
			}
			if strings.HasPrefix(line, prefix) {
				return strings.TrimSpace(strings.TrimPrefix(line, prefix))
			}
		case <-timeout.C:
			t.Fatalf("timed out waiting for helper %q: %s", prefix, c.stderr.String())
		}
	}
}

func waitWebLeaseHeld(t *testing.T, child *webLeaseChild) int {
	t.Helper()
	pid, err := strconv.Atoi(child.waitLine(t, "held "))
	if err != nil {
		t.Fatalf("helper pid: %v", err)
	}
	return pid
}

// TestWebLeaseHeldByChildProcess covers AC-WEB-020: reads work and show the
// holder, and writes are refused with 409 session_in_use naming the PID.
func TestWebLeaseHeldByChildProcess(t *testing.T) {
	rt, home := webRuntime(t, mock.Script{})
	info := webCreateSession(t, rt, context.Background())
	child := startWebLeaseChild(t, home, info.ID)
	pid := waitWebLeaseHeld(t, child)

	_, ts, cookie, csrf := webServer(t, rt, home)

	resp, body := webDo(t, http.MethodGet, ts.URL+"/v1/sessions", "", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, body = %q", resp.StatusCode, body)
	}
	var list struct {
		Sessions []struct {
			ID        string `json:"id"`
			InUse     string `json:"in_use"`
			HolderPID *int   `json:"holder_pid"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("list body %q: %v", body, err)
	}
	found := false
	for _, session := range list.Sessions {
		if session.ID != info.ID {
			continue
		}
		found = true
		if session.InUse != "other" || session.HolderPID == nil || *session.HolderPID != pid {
			t.Fatalf("list session = %+v, want in_use other with pid %d", session, pid)
		}
	}
	if !found {
		t.Fatalf("list did not contain %s: %q", info.ID, body)
	}

	resp, body = webDo(t, http.MethodGet, ts.URL+"/v1/sessions/"+info.ID, "", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d, body = %q", resp.StatusCode, body)
	}
	var detail struct {
		InUse     string `json:"in_use"`
		HolderPID *int   `json:"holder_pid"`
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("detail body %q: %v", body, err)
	}
	if detail.InUse != "other" || detail.HolderPID == nil || *detail.HolderPID != pid {
		t.Fatalf("detail = %+v, want in_use other with pid %d", detail, pid)
	}

	resp, body = webDo(t, http.MethodGet, ts.URL+"/v1/sessions/"+info.ID+"/events", "", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d, body = %q", resp.StatusCode, body)
	}

	writes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/v1/sessions/" + info.ID + "/messages", `{"text":"hello"}`},
		{http.MethodPost, "/v1/sessions/" + info.ID + "/cancel", ""},
		{http.MethodPatch, "/v1/sessions/" + info.ID, `{"policy":"never"}`},
		{http.MethodPost, "/v1/sessions/" + info.ID + "/compact", ""},
		{http.MethodPost, "/v1/sessions/" + info.ID + "/fork", ""},
	}
	for _, call := range writes {
		resp, body := webDo(t, call.method, ts.URL+call.path, call.body, cookie, csrf)
		assertSessionInUse(t, resp, body, pid, call.method+" "+call.path)
	}
}

// TestWebLeaseChildProcessPoll covers AC-WEB-014: while a child process runs a
// turn, the 1-second log poll delivers its events and the status names its PID.
func TestWebLeaseChildProcessPoll(t *testing.T) {
	rt, home := webRuntime(t, mock.Script{})
	info := webCreateSession(t, rt, context.Background())
	child := startWebLeaseChild(t, home, info.ID)
	pid := waitWebLeaseHeld(t, child)

	_, ts, cookie, _ := webServer(t, rt, home)
	stream := openWebStream(t, ts, cookie, "/v1/sessions/"+info.ID+"/stream")
	defer func() { _ = stream.Body.Close() }()
	reader := bufio.NewReader(stream.Body)

	status, ok := nextWebSSEWithin(t, reader, 3*time.Second)
	if !ok {
		t.Fatal("no status line within 3 seconds")
	}
	if status.Name != "status" {
		t.Fatalf("first SSE block = %+v, want status", status)
	}
	var state struct {
		Holder      string `json:"holder"`
		PID         *int   `json:"pid"`
		TurnRunning bool   `json:"turn_running"`
	}
	if err := json.Unmarshal([]byte(status.Data), &state); err != nil {
		t.Fatalf("status data %q: %v", status.Data, err)
	}
	if state.Holder != "other" || state.PID == nil || *state.PID != pid {
		t.Fatalf("status = %+v, want holder other with pid %d", state, pid)
	}

	child.send(t, "run child event")
	start := time.Now()
	var sawUser, sawAssistant bool
	for !sawUser || !sawAssistant {
		remaining := time.Until(start.Add(3 * time.Second))
		if remaining <= 0 {
			t.Fatalf("events not seen within 3 seconds (user=%v assistant=%v)", sawUser, sawAssistant)
		}
		block, ok := nextWebSSEWithin(t, reader, remaining)
		if !ok {
			t.Fatalf("events not seen within 3 seconds (user=%v assistant=%v)", sawUser, sawAssistant)
		}
		if block.Name != "event" {
			continue
		}
		var event struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal([]byte(block.Data), &event); err != nil {
			t.Fatalf("event data %q: %v", block.Data, err)
		}
		switch event.Kind {
		case core.KindUserMessage:
			sawUser = true
		case core.KindAssistantMessage:
			sawAssistant = true
		}
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("events arrived after %s, want at most 3 seconds", elapsed)
	}
	child.waitLine(t, "turn ")
}

// nextWebSSEWithin reads one SSE block and gives up after timeout.
func nextWebSSEWithin(t *testing.T, r *bufio.Reader, timeout time.Duration) (webSSEBlock, bool) {
	t.Helper()
	type result struct {
		block webSSEBlock
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		block, err := readWebSSEBlock(r)
		ch <- result{block: block, err: err}
	}()
	select {
	case got := <-ch:
		if got.err != nil {
			t.Fatalf("read SSE: %v", got.err)
		}
		return got.block, true
	case <-time.After(timeout):
		return webSSEBlock{}, false
	}
}

func assertSessionInUse(t *testing.T, resp *http.Response, body []byte, pid int, what string) {
	t.Helper()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("%s status = %d, body = %q, want 409", what, resp.StatusCode, body)
	}
	var envelope struct {
		Error struct {
			Code    string           `json:"code"`
			Details []map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("%s body %q: %v", what, body, err)
	}
	if envelope.Error.Code != "session_in_use" {
		t.Fatalf("%s code = %q, body = %q", what, envelope.Error.Code, body)
	}
	if len(envelope.Error.Details) != 1 {
		t.Fatalf("%s details = %v, body = %q", what, envelope.Error.Details, body)
	}
	gotPID, ok := envelope.Error.Details[0]["pid"].(float64)
	if !ok || int(gotPID) != pid {
		t.Fatalf("%s details pid = %v, want %d", what, envelope.Error.Details[0]["pid"], pid)
	}
}
