package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
	"github.com/ridhomujizat/lana-harness/web"
)

func writeWebOnlyConfig(t *testing.T, home, webBody string) {
	t.Helper()
	cfg := "[web]\n" + webBody
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

func webAddressFromStderr(t *testing.T, w *syncWriter) string {
	t.Helper()
	const prefix = "web: listening on http://"
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range strings.Split(w.String(), "\n") {
			if strings.HasPrefix(line, prefix) {
				return strings.TrimSpace(strings.TrimPrefix(line, prefix))
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("stderr has no web start line: %q", w.String())
	return ""
}

func waitDaemon(t *testing.T, done <-chan int) int {
	t.Helper()
	select {
	case code := <-done:
		return code
	case <-time.After(shutdownGrace + time.Second):
		t.Fatal("daemon did not stop")
		return -1
	}
}

func TestDaemonWebStart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	writeWebOnlyConfig(t, home, "addr = \"127.0.0.1:0\"\n")

	ctx, cancel := context.WithCancel(context.Background())
	var stderr syncWriter
	done := make(chan int, 1)
	go func() { done <- runDaemon(ctx, &stderr) }()
	addr := webAddressFromStderr(t, &stderr)
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("web address = %q", addr)
	}
	resp, err := (&http.Client{Timeout: time.Second}).Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	body := readHTTPBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "<title>lana</title>") {
		t.Fatalf("GET / status = %d, body = %q", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'self'") {
		t.Fatalf("CSP = %q", got)
	}

	cancel()
	if code := waitDaemon(t, done); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}

func TestDaemonWebDisabledNoListener(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	writeWebOnlyConfig(t, home, fmt.Sprintf("enabled = false\naddr = %q\n", addr))

	var stderr syncWriter
	if code := runDaemon(context.Background(), &stderr); code != 2 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "web:") {
		t.Fatalf("disabled web view printed a web line: %q", stderr.String())
	}
	conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		t.Fatalf("port %s is open although web is disabled", addr)
	}
}

func TestDaemonWebNonLoopback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	writeWebOnlyConfig(t, home, "addr = \"0.0.0.0:18300\"\n")

	var stderr syncWriter
	if code := runDaemon(context.Background(), &stderr); code != 2 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if got := strings.TrimSpace(stderr.String()); got != "error: web: addr must be a loopback address" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestDaemonWebOnlyNoTelegram(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	tg := newTGServer(t, 111, "hi")
	writeWebOnlyConfig(t, home, "addr = \"127.0.0.1:0\"\n")

	ctx, cancel := context.WithCancel(context.Background())
	var stderr syncWriter
	done := make(chan int, 1)
	go func() { done <- runDaemon(ctx, &stderr) }()
	addr := webAddressFromStderr(t, &stderr)
	resp, err := (&http.Client{Timeout: time.Second}).Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d", resp.StatusCode)
	}

	time.Sleep(100 * time.Millisecond)
	if got := tg.callCount(); got != 0 {
		t.Fatalf("web-only daemon made %d Telegram API calls", got)
	}
	if strings.Contains(stderr.String(), "telegram:") {
		t.Fatalf("web-only daemon printed a telegram line: %q", stderr.String())
	}

	cancel()
	if code := waitDaemon(t, done); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}

func TestDaemonWebPortInUse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	writeWebOnlyConfig(t, home, fmt.Sprintf("addr = %q\n", ln.Addr().String()))

	var stderr syncWriter
	if code := runDaemon(context.Background(), &stderr); code != 1 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "error: web: address already in use") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func readHTTPBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

const (
	webTestAddr  = "127.0.0.1:18300"
	webTestToken = "cmd-lana-web-test-token-value"
	webTestLayer = `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.assistant]
provider = "mock"
model = "m"
permission = "ask"
toolset = "custom"
tools = ["bash"]

[[routes]]
channel = "web"
agent = "assistant"
`
)

// writeWebToken writes a version 1 web.json holding webTestToken into home and
// returns the token. Spec #18 no longer prints an owner token, so tests seed
// the legacy token file directly.
func writeWebToken(t *testing.T, home string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(webTestToken))
	body := `{"version":1,"token_sha256":"` + hex.EncodeToString(sum[:]) + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(home, "web.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return webTestToken
}

func webRuntime(t *testing.T, script mock.Script, opts ...lana.Option) (*lana.Runtime, string) {
	t.Helper()
	home := t.TempDir()
	rt, err := lana.New(context.Background(), lana.Options{
		Home:      home,
		Layers:    []config.Layer{{Name: "test", TOML: []byte(webTestLayer)}},
		Providers: map[string]core.Provider{"mock": mock.New(script)},
	}, opts...)
	if err != nil {
		t.Fatalf("lana.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt, home
}

func webServer(t *testing.T, rt *lana.Runtime, home string) (*web.Server, *httptest.Server, *http.Cookie, string) {
	t.Helper()
	leases := newLeaseSet(rt)
	t.Cleanup(leases.releaseAll)
	return webServerWithLeases(t, rt, home, leases)
}

func webServerWithLeases(t *testing.T, rt *lana.Runtime, home string, leases *leaseSet) (*web.Server, *httptest.Server, *http.Cookie, string) {
	t.Helper()
	token := writeWebToken(t, home)
	srv, err := web.New(web.Config{Addr: webTestAddr, Enabled: true, Home: home})
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	srv.SetBackend(newWebBackend(rt, home, leases, context.Background()))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	cookie, csrf := webLogin(t, ts, token)
	return srv, ts, cookie, csrf
}

func webDo(t *testing.T, method, url, body string, cookie *http.Cookie, csrf string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = webTestAddr
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set("X-Lana-CSRF", csrf)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return resp, data
}

func webLogin(t *testing.T, ts *httptest.Server, token string) (*http.Cookie, string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		t.Fatal(err)
	}
	resp, data := webDo(t, http.MethodPost, ts.URL+"/v1/auth/login", string(body), nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, body = %q", resp.StatusCode, data)
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
	resp, data = webDo(t, http.MethodGet, ts.URL+"/v1/auth/session", "", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session status = %d, body = %q", resp.StatusCode, data)
	}
	var session struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(data, &session); err != nil {
		t.Fatal(err)
	}
	return cookie, session.CSRF
}

func webCreateSession(t *testing.T, rt *lana.Runtime, ctx context.Context) core.SessionInfo {
	t.Helper()
	return webCreateAgentSession(t, rt, ctx, "assistant")
}

// webCreateAgentSession creates a web session for agent with an owner title.
// Spec 40: after the first turn of an untitled web session a background title
// pass calls the same provider with no tools and reasoning "off". The owner
// title skips that pass, so it neither takes a scripted mock response from the
// turns under test nor becomes the last request a capture provider records.
func webCreateAgentSession(t *testing.T, rt *lana.Runtime, ctx context.Context, agent string) core.SessionInfo {
	t.Helper()
	info, err := rt.CreateChannelSession(ctx, "web", "owner", agent)
	if err != nil {
		t.Fatalf("CreateChannelSession: %v", err)
	}
	if info, err = rt.SetSessionTitle(ctx, info.ID, "Test session"); err != nil {
		t.Fatalf("SetSessionTitle: %v", err)
	}
	return info
}

func webWaitFor(t *testing.T, timeout time.Duration, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// webWaitIdle waits until the session list no longer reports the session as
// running. turn/end lands in the log before the turn's bookkeeping finishes:
// the web backend's in-process turn marker clears only after the runtime's Run
// returns. Until then the backend's TurnActive is still true, so compact, fork,
// and policy writes answer invalid_state. The list row status is derived from
// that same TurnActive check, so an idle row means those writes are allowed.
func webWaitIdle(t *testing.T, ts *httptest.Server, cookie *http.Cookie, sessionID string) {
	t.Helper()
	webWaitFor(t, 5*time.Second, "session "+sessionID+" to be idle", func() bool {
		resp, body := webDo(t, http.MethodGet, ts.URL+"/v1/sessions", "", cookie, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list sessions status = %d, body = %q", resp.StatusCode, body)
		}
		var list struct {
			Sessions []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"sessions"`
		}
		if err := json.Unmarshal(body, &list); err != nil {
			t.Fatalf("list sessions body %q: %v", body, err)
		}
		for _, row := range list.Sessions {
			if row.ID == sessionID {
				return row.Status != web.SessionStatusRunning
			}
		}
		t.Fatalf("session %s is missing from the list: %q", sessionID, body)
		return false
	})
}

func webHistory(t *testing.T, rt *lana.Runtime, sessionID string) []core.SessionEvent {
	t.Helper()
	events, err := rt.History(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

type webSSEBlock struct {
	Name    string
	ID      string
	Data    string
	Comment string
}

func readWebSSEBlock(r *bufio.Reader) (webSSEBlock, error) {
	var block webSSEBlock
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return block, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if block != (webSSEBlock{}) {
				return block, nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			block.Comment = strings.TrimSpace(strings.TrimPrefix(line, ":"))
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			block.Name = value
		case "id":
			block.ID = value
		case "data":
			block.Data = value
		}
	}
}

func nextWebSSE(t *testing.T, r *bufio.Reader) webSSEBlock {
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
		return got.block
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for SSE")
		return webSSEBlock{}
	}
}

func waitWebSSE(t *testing.T, r *bufio.Reader, name string) webSSEBlock {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		block := nextWebSSE(t, r)
		if block.Name == name {
			return block
		}
	}
	t.Fatalf("no %q SSE event", name)
	return webSSEBlock{}
}

func openWebStream(t *testing.T, ts *httptest.Server, cookie *http.Cookie, target string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+target, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = webTestAddr
	req.AddCookie(cookie)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("stream status = %d, body = %q", resp.StatusCode, body)
	}
	return resp
}

// TestWebTurnFramesFinalText covers AC-WEB-012: text frames arrive in order and
// the final text equals the last assistant/message event.
func TestWebTurnFramesFinalText(t *testing.T) {
	text := "hello"
	end := "end_turn"
	rt, home := webRuntime(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{Text: &text}, {Stop: &end}}},
	}})
	_, ts, cookie, csrf := webServer(t, rt, home)
	_ = csrf
	info := webCreateSession(t, rt, context.Background())

	stream := openWebStream(t, ts, cookie, "/v1/sessions/"+info.ID+"/stream")
	defer func() { _ = stream.Body.Close() }()
	reader := bufio.NewReader(stream.Body)
	waitWebSSE(t, reader, "status")

	resp, body := webDo(t, http.MethodPost, ts.URL+"/v1/sessions/"+info.ID+"/messages", `{"text":"hello"}`, cookie, csrf)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("send status = %d, body = %q", resp.StatusCode, body)
	}

	var frames strings.Builder
	var assistantText string
	var turnStopped bool
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && !turnStopped {
		block := nextWebSSE(t, reader)
		switch block.Name {
		case "frame":
			var frame struct {
				Kind string `json:"kind"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal([]byte(block.Data), &frame); err != nil {
				t.Fatal(err)
			}
			if frame.Kind == "text_delta" {
				frames.WriteString(frame.Text)
			}
		case "event":
			var event struct {
				Kind    string          `json:"kind"`
				Payload json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal([]byte(block.Data), &event); err != nil {
				t.Fatal(err)
			}
			if event.Kind == core.KindAssistantMessage {
				var message core.AssistantMessage
				if err := json.Unmarshal(event.Payload, &message); err != nil {
					t.Fatal(err)
				}
				for _, block := range message.Content {
					if block.Kind == core.BlockText {
						assistantText += block.Text
					}
				}
			}
		case "turn":
			var turn struct {
				StopReason string `json:"stop_reason"`
			}
			if err := json.Unmarshal([]byte(block.Data), &turn); err != nil {
				t.Fatal(err)
			}
			if turn.StopReason != "end_turn" {
				t.Fatalf("stop reason = %q", turn.StopReason)
			}
			turnStopped = true
		}
	}
	if !turnStopped {
		t.Fatal("stream did not end the turn")
	}
	if frames.String() != "hello" || assistantText != frames.String() {
		t.Fatalf("frames = %q, last assistant message = %q", frames.String(), assistantText)
	}
}

// TestWebTurnQueuesDuringTurn covers AC-WEB-013: a second message goes to the
// inbox and is claimed at the next step.
func TestWebTurnQueuesDuringTurn(t *testing.T) {
	delay := 300
	first, second := "one", "two"
	end := "end_turn"
	rt, home := webRuntime(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{DelayMS: &delay}, {Text: &first}, {Stop: &end}}},
		{Events: []mock.Event{{Text: &second}, {Stop: &end}}},
	}})
	_, ts, cookie, csrf := webServer(t, rt, home)
	info := webCreateSession(t, rt, context.Background())

	resp, body := webDo(t, http.MethodPost, ts.URL+"/v1/sessions/"+info.ID+"/messages", `{"text":"first"}`, cookie, csrf)
	if resp.StatusCode != http.StatusAccepted || !strings.Contains(string(body), `"turn":"started"`) {
		t.Fatalf("first send status = %d, body = %q", resp.StatusCode, body)
	}
	var started struct {
		Turn   string `json:"turn"`
		TurnID string `json:"turn_id"`
	}
	if err := json.Unmarshal(body, &started); err != nil {
		t.Fatal(err)
	}
	if started.TurnID == "" {
		t.Fatalf("started response has no turn_id: %q", body)
	}
	webWaitFor(t, 3*time.Second, "the first turn to start", func() bool {
		for _, event := range webHistory(t, rt, info.ID) {
			if event.Kind == core.KindTurnStart {
				return true
			}
		}
		return false
	})

	resp, body = webDo(t, http.MethodPost, ts.URL+"/v1/sessions/"+info.ID+"/messages", `{"text":"second"}`, cookie, csrf)
	if resp.StatusCode != http.StatusAccepted || !strings.Contains(string(body), `"turn":"queued"`) {
		t.Fatalf("second send status = %d, body = %q", resp.StatusCode, body)
	}
	var turns, users, assistants int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		turns, users, assistants = 0, 0, 0
		for _, event := range webHistory(t, rt, info.ID) {
			switch event.Kind {
			case core.KindTurnEnd:
				turns++
			case core.KindUserMessage:
				users++
			case core.KindAssistantMessage:
				assistants++
			}
		}
		if turns == 1 && users == 2 && assistants == 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	var kinds []string
	for _, event := range webHistory(t, rt, info.ID) {
		kinds = append(kinds, event.Kind)
	}
	t.Fatalf("turns=%d users=%d assistants=%d events=%v", turns, users, assistants, kinds)
}

// TestWebCancelWithPendingApproval covers AC-WEB-018.
func TestWebCancelWithPendingApproval(t *testing.T) {
	use := "tool_use"
	approvalStarted := make(chan struct{})
	var once sync.Once
	approve := func(ctx context.Context, _ core.ApprovalRequest) core.ApprovalOutcome {
		once.Do(func() { close(approvalStarted) })
		<-ctx.Done()
		return core.OutcomeCancelled
	}
	rt, home := webRuntime(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{ToolUse: &mock.ToolUse{ID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"echo hi"}`)}}, {Stop: &use}}},
	}}, lana.ApproveWith(approve), lana.ApproveChannels("web"))
	_, ts, cookie, csrf := webServer(t, rt, home)
	info := webCreateSession(t, rt, context.Background())

	resp, body := webDo(t, http.MethodPost, ts.URL+"/v1/sessions/"+info.ID+"/messages", `{"text":"run"}`, cookie, csrf)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("send status = %d, body = %q", resp.StatusCode, body)
	}
	select {
	case <-approvalStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("approval did not open")
	}

	resp, body = webDo(t, http.MethodPost, ts.URL+"/v1/sessions/"+info.ID+"/cancel", "", cookie, csrf)
	if resp.StatusCode != http.StatusAccepted || !strings.Contains(string(body), `"turn":"cancelling"`) {
		t.Fatalf("cancel status = %d, body = %q", resp.StatusCode, body)
	}

	var cancelledTurn, cancelledApproval bool
	webWaitFor(t, 5*time.Second, "the cancelled turn", func() bool {
		for _, event := range webHistory(t, rt, info.ID) {
			switch event.Kind {
			case core.KindTurnEnd:
				var end core.TurnEnd
				if err := json.Unmarshal(event.Payload, &end); err == nil && end.Reason == "cancelled" {
					cancelledTurn = true
				}
			case core.KindApprovalDecided:
				var decided core.ApprovalDecided
				if err := json.Unmarshal(event.Payload, &decided); err == nil && decided.Outcome == core.OutcomeCancelled {
					cancelledApproval = true
				}
			}
		}
		return cancelledTurn && cancelledApproval
	})
}

// TestWebTurnSurvivesRequestCancel covers AC-WEB-032.
func TestWebTurnSurvivesRequestCancel(t *testing.T) {
	delay := 300
	text := "slow"
	end := "end_turn"
	rt, home := webRuntime(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{DelayMS: &delay}, {Text: &text}, {Stop: &end}}},
	}})
	_, ts, cookie, csrf := webServer(t, rt, home)
	info := webCreateSession(t, rt, context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/v1/sessions/"+info.ID+"/messages", strings.NewReader(`{"text":"slow"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = webTestAddr
	req.AddCookie(cookie)
	req.Header.Set("X-Lana-CSRF", csrf)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("send status = %d", resp.StatusCode)
	}
	cancel()

	var finished bool
	webWaitFor(t, 5*time.Second, "the turn to finish after the request context ended", func() bool {
		for _, event := range webHistory(t, rt, info.ID) {
			if event.Kind == core.KindTurnEnd {
				var end core.TurnEnd
				if err := json.Unmarshal(event.Payload, &end); err == nil && end.Reason == "end_turn" {
					finished = true
				}
			}
		}
		return finished
	})
}

// TestWebCompactForkAndPolicy covers AC-WEB-019.
func TestWebCompactForkAndPolicy(t *testing.T) {
	text, summary := "one", "summary"
	end := "end_turn"
	rt, home := webRuntime(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{Text: &text}, {Stop: &end}}},
		{Events: []mock.Event{{Text: &summary}, {Stop: &end}}},
	}})
	_, ts, cookie, csrf := webServer(t, rt, home)
	info := webCreateSession(t, rt, context.Background())
	if _, body := webDo(t, http.MethodPost, ts.URL+"/v1/sessions/"+info.ID+"/messages", `{"text":"one"}`, cookie, csrf); !strings.Contains(string(body), "started") {
		t.Fatalf("send body = %q", body)
	}
	webWaitFor(t, 5*time.Second, "the turn to finish", func() bool {
		for _, event := range webHistory(t, rt, info.ID) {
			if event.Kind == core.KindTurnEnd {
				return true
			}
		}
		return false
	})
	// turn/end is written before the turn's bookkeeping clears; compact is
	// refused until the session reports idle.
	webWaitIdle(t, ts, cookie, info.ID)

	resp, body := webDo(t, http.MethodPost, ts.URL+"/v1/sessions/"+info.ID+"/compact", "", cookie, csrf)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("compact status = %d, body = %q", resp.StatusCode, body)
	}
	foundSummary := false
	for _, event := range webHistory(t, rt, info.ID) {
		if event.Kind == core.KindCompactionSummary {
			foundSummary = true
		}
	}
	if !foundSummary {
		t.Fatal("compaction/summary event is missing")
	}

	resp, body = webDo(t, http.MethodPost, ts.URL+"/v1/sessions/"+info.ID+"/fork", "", cookie, csrf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("fork status = %d, body = %q", resp.StatusCode, body)
	}
	var forked struct {
		ID              string `json:"id"`
		ParentSessionID string `json:"parent_session_id"`
		InheritedSeq    int64  `json:"inherited_seq"`
	}
	if err := json.Unmarshal(body, &forked); err != nil {
		t.Fatal(err)
	}
	if forked.ID == info.ID || forked.ParentSessionID != info.ID || forked.InheritedSeq == 0 {
		t.Fatalf("forked = %+v", forked)
	}

	resp, body = webDo(t, http.MethodPatch, ts.URL+"/v1/sessions/"+info.ID, `{"policy":"never"}`, cookie, csrf)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"policy":"never"`) {
		t.Fatalf("policy status = %d, body = %q", resp.StatusCode, body)
	}
	resp, body = webDo(t, http.MethodPatch, ts.URL+"/v1/sessions/"+info.ID, `{"policy":"trust"}`, cookie, csrf)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "invalid_request") {
		t.Fatalf("trust without confirm status = %d, body = %q", resp.StatusCode, body)
	}
	countPolicies := func() (int, bool) {
		var count int
		found := false
		for _, event := range webHistory(t, rt, info.ID) {
			if event.Kind != core.KindApprovalPolicy {
				continue
			}
			count++
			var policy core.ApprovalPolicy
			if err := json.Unmarshal(event.Payload, &policy); err == nil && policy.Policy == core.PolicyNever {
				found = true
			}
		}
		return count, found
	}
	policies, foundPolicy := countPolicies()
	if !foundPolicy {
		t.Fatal("approval/policy event with never is missing")
	}
	if resp, body = webDo(t, http.MethodPatch, ts.URL+"/v1/sessions/"+info.ID, `{"policy":"never"}`, cookie, csrf); resp.StatusCode != http.StatusOK {
		t.Fatalf("idempotent policy status = %d, body = %q", resp.StatusCode, body)
	}
	if again, _ := countPolicies(); again != policies {
		t.Fatalf("setting the same policy wrote another event: %d -> %d", policies, again)
	}
}

// TestWebWritesRefusedDuringTurn covers the invalid_state rule.
func TestWebWritesRefusedDuringTurn(t *testing.T) {
	delay := 500
	text := "slow"
	end := "end_turn"
	rt, home := webRuntime(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{DelayMS: &delay}, {Text: &text}, {Stop: &end}}},
	}})
	_, ts, cookie, csrf := webServer(t, rt, home)
	info := webCreateSession(t, rt, context.Background())
	if resp, body := webDo(t, http.MethodPost, ts.URL+"/v1/sessions/"+info.ID+"/messages", `{"text":"slow"}`, cookie, csrf); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("send status = %d, body = %q", resp.StatusCode, body)
	}
	webWaitFor(t, 3*time.Second, "the turn to start", func() bool {
		for _, event := range webHistory(t, rt, info.ID) {
			if event.Kind == core.KindTurnStart {
				return true
			}
		}
		return false
	})
	for _, call := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPatch, "/v1/sessions/" + info.ID, `{"policy":"never"}`},
		{http.MethodPost, "/v1/sessions/" + info.ID + "/compact", ""},
		{http.MethodPost, "/v1/sessions/" + info.ID + "/fork", ""},
	} {
		resp, body := webDo(t, call.method, ts.URL+call.path, call.body, cookie, csrf)
		if resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "invalid_state") {
			t.Fatalf("%s %s status = %d, body = %q", call.method, call.path, resp.StatusCode, body)
		}
	}
}

// TestWebHolderSharedLeaseSet covers AC-WEB-021: a Telegram session leased by
// this daemon is used from web without taking a second lease, and in_use is
// reported as self.
func TestWebHolderSharedLeaseSet(t *testing.T) {
	text, end := "hello", "end_turn"
	rt, home := webRuntime(t, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{Text: &text}, {Stop: &end}}},
	}})
	ctx := context.Background()
	leases := newLeaseSet(rt)
	t.Cleanup(leases.releaseAll)

	info, err := rt.CreateChannelSession(ctx, "telegram", "owner", "assistant")
	if err != nil {
		t.Fatalf("CreateChannelSession: %v", err)
	}
	if err := leases.ensure(ctx, info.ID); err != nil {
		t.Fatalf("lease the Telegram session: %v", err)
	}

	_, ts, cookie, csrf := webServerWithLeases(t, rt, home, leases)
	resp, body := webDo(t, http.MethodGet, ts.URL+"/v1/sessions/"+info.ID, "", cookie, "")
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
	if detail.InUse != "self" || detail.HolderPID != nil {
		t.Fatalf("detail = %+v, want in_use self without holder_pid", detail)
	}

	resp, body = webDo(t, http.MethodPost, ts.URL+"/v1/sessions/"+info.ID+"/messages", `{"text":"hello"}`, cookie, csrf)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("send status = %d, body = %q, want 202", resp.StatusCode, body)
	}
	webWaitFor(t, 5*time.Second, "the web turn to finish", func() bool {
		for _, event := range webHistory(t, rt, info.ID) {
			if event.Kind == core.KindTurnEnd {
				return true
			}
		}
		return false
	})

	same, err := rt.ChannelSession(ctx, "telegram", "owner", "assistant")
	if err != nil {
		t.Fatalf("ChannelSession after the web turn: %v", err)
	}
	if same.ID != info.ID {
		t.Fatalf("next Telegram message would use %s, want %s", same.ID, info.ID)
	}
}

// TestWebConfigSaveKeepsRunningModel covers AC-WEB-025: a saved model change
// does not touch the running runtime, and the settings page reports restart
// required until the daemon restarts.
func TestWebConfigSaveKeepsRunningModel(t *testing.T) {
	home := t.TempDir()
	const original = `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.helper]
provider = "mock"
model = "X"
permission = "never"
tools = ["bash"]

[[routes]]
channel = "web"
agent = "helper"
`
	configPath := filepath.Join(home, "config.toml")
	if err := os.WriteFile(configPath, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	rt, err := lana.New(context.Background(), lana.Options{
		Home:      home,
		Providers: map[string]core.Provider{"mock": mock.New(mock.Script{})},
	})
	if err != nil {
		t.Fatalf("lana.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	_, ts, cookie, csrf := webServer(t, rt, home)
	ctx := context.Background()
	info, err := rt.CreateChannelSession(ctx, "web", "owner", "helper")
	if err != nil {
		t.Fatalf("CreateChannelSession: %v", err)
	}

	resp, body := webDo(t, http.MethodGet, ts.URL+"/v1/sessions/"+info.ID, "", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session status = %d, body = %q", resp.StatusCode, body)
	}
	var detail struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Model != "mock:X" {
		t.Fatalf("running model = %q, want mock:X", detail.Model)
	}

	resp, body = webDo(t, http.MethodGet, ts.URL+"/v1/config", "", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("config status = %d, body = %q", resp.StatusCode, body)
	}
	var before struct {
		Version         string `json:"version"`
		LoadedVersion   string `json:"loaded_version"`
		RestartRequired bool   `json:"restart_required"`
		Settings        struct {
			Agents map[string]struct {
				Model string `json:"model"`
			} `json:"agents"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(body, &before); err != nil {
		t.Fatal(err)
	}
	if before.Version == "" || before.Version != before.LoadedVersion || before.RestartRequired {
		t.Fatalf("initial config = %+v", before)
	}
	if got := before.Settings.Agents["helper"].Model; got != "X" {
		t.Fatalf("initial settings model = %q", got)
	}

	changed := strings.Replace(original, `model = "X"`, `model = "Y"`, 1)
	payload, err := json.Marshal(map[string]string{"raw": changed, "expected_version": before.Version})
	if err != nil {
		t.Fatal(err)
	}
	resp, body = webDo(t, http.MethodPut, ts.URL+"/v1/config", string(payload), cookie, csrf)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save status = %d, body = %q", resp.StatusCode, body)
	}
	var saved struct {
		Version         string `json:"version"`
		RestartRequired bool   `json:"restart_required"`
	}
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.RestartRequired || saved.Version == before.Version {
		t.Fatalf("save response = %+v", saved)
	}

	resp, body = webDo(t, http.MethodGet, ts.URL+"/v1/sessions/"+info.ID, "", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session after save status = %d, body = %q", resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Model != "mock:X" {
		t.Fatalf("running model after save = %q, want mock:X", detail.Model)
	}

	resp, body = webDo(t, http.MethodGet, ts.URL+"/v1/config", "", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("config after save status = %d, body = %q", resp.StatusCode, body)
	}
	var after struct {
		Version         string `json:"version"`
		LoadedVersion   string `json:"loaded_version"`
		RestartRequired bool   `json:"restart_required"`
		Settings        struct {
			Agents map[string]struct {
				Model string `json:"model"`
			} `json:"agents"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(body, &after); err != nil {
		t.Fatal(err)
	}
	if !after.RestartRequired || after.LoadedVersion != before.Version || after.Version != saved.Version {
		t.Fatalf("config after save = %+v, before = %+v", after, before)
	}
	if got := after.Settings.Agents["helper"].Model; got != "Y" {
		t.Fatalf("settings model after save = %q, want Y", got)
	}
}

// TestWebConfigSettingsMaskMCPSecrets covers AC-WEB-022 through the real
// daemon adapter: both the structured view and the raw text mask MCP headers.
func TestWebConfigSettingsMaskMCPSecrets(t *testing.T) {
	home := t.TempDir()
	const raw = `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.helper]
provider = "mock"
model = "X"

[mcp.remote]
url = "https://example.com/mcp"

[mcp.remote.headers]
Authorization = "Bearer x"

[[routes]]
channel = "web"
agent = "helper"
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	rt, err := lana.New(context.Background(), lana.Options{
		Home:      home,
		Providers: map[string]core.Provider{"mock": mock.New(mock.Script{})},
	})
	if err != nil {
		t.Fatalf("lana.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	_, ts, cookie, _ := webServer(t, rt, home)
	resp, body := webDo(t, http.MethodGet, ts.URL+"/v1/config", "", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("config status = %d, body = %q", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "Bearer x") {
		t.Fatalf("config response leaked the MCP header: %s", body)
	}
	var got struct {
		Version    string `json:"version"`
		Validation struct {
			OK bool `json:"ok"`
		} `json:"validation"`
		Raw      string `json:"raw"`
		Settings struct {
			MCP map[string]struct {
				Headers map[string]string `json:"headers"`
			} `json:"mcp"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Validation.OK {
		t.Fatalf("validation = %+v, body = %q", got.Validation, body)
	}
	if !strings.Contains(got.Raw, `"***"`) {
		t.Fatalf("raw text is not masked: %q", got.Raw)
	}
	if got.Settings.MCP["remote"].Headers["Authorization"] != "***" {
		t.Fatalf("structured headers = %v", got.Settings.MCP["remote"].Headers)
	}
}

// webDoAt is webDo for a daemon bound to a dynamic address: the Host header
// must match the address the server actually listens on.
func webDoAt(t *testing.T, addr, method, target, body string, cookie *http.Cookie, csrf string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+addr+target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = addr
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set("X-Lana-CSRF", csrf)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return resp, data
}

func webLoginAt(t *testing.T, addr, token string) (*http.Cookie, string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		t.Fatal(err)
	}
	resp, data := webDoAt(t, addr, http.MethodPost, "/v1/auth/login", string(body), nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, body = %q", resp.StatusCode, data)
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
	resp, data = webDoAt(t, addr, http.MethodGet, "/v1/auth/session", "", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session status = %d, body = %q", resp.StatusCode, data)
	}
	var session struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(data, &session); err != nil {
		t.Fatal(err)
	}
	return cookie, session.CSRF
}

func openWebStreamAt(t *testing.T, addr string, cookie *http.Cookie, target string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+target, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = addr
	req.AddCookie(cookie)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("stream status = %d, body = %q", resp.StatusCode, body)
	}
	return resp
}

// TestWebShutdownCancelsTurn covers AC-WEB-031: SIGTERM with an open SSE stream
// and a running turn sends event: shutdown, ends the turn cancelled, and exits
// 0 within the 10-second grace period. A context cancel drives the same
// signal.NotifyContext path that SIGTERM uses in cmdDaemon.
func TestWebShutdownCancelsTurn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	llmStarted := make(chan struct{}, 1)
	stop := make(chan struct{})
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case llmStarted <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-stop:
		}
	}))
	defer llm.Close()
	defer close(stop)

	cfg := fmt.Sprintf(`[providers.local]
kind = "openai_compat"
base_url = %q

[agents.helper]
provider = "local"
model = "m"
permission = "never"

[web]
addr = "127.0.0.1:0"
open_browser = false

[[routes]]
channel = "web"
agent = "helper"
`, llm.URL)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	token := writeWebToken(t, home)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stderr syncWriter
	done := make(chan int, 1)
	go func() { done <- runDaemon(ctx, &stderr) }()
	addr := webAddressFromStderr(t, &stderr)
	cookie, csrf := webLoginAt(t, addr, token)

	resp, body := webDoAt(t, addr, http.MethodPost, "/v1/sessions", `{"agent":"helper"}`, cookie, csrf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create session status = %d, body = %q", resp.StatusCode, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.ID == "" {
		t.Fatalf("create session body %q: %v", body, err)
	}

	stream := openWebStreamAt(t, addr, cookie, "/v1/sessions/"+created.ID+"/stream")
	defer func() { _ = stream.Body.Close() }()
	reader := bufio.NewReader(stream.Body)
	waitWebSSE(t, reader, "status")

	resp, body = webDoAt(t, addr, http.MethodPost, "/v1/sessions/"+created.ID+"/messages", `{"text":"slow"}`, cookie, csrf)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("send status = %d, body = %q", resp.StatusCode, body)
	}
	select {
	case <-llmStarted:
	case <-time.After(5 * time.Second):
		t.Fatalf("turn did not reach the provider; stderr=%q", stderr.String())
	}

	started := time.Now()
	cancel()

	var sawShutdown bool
	for !sawShutdown {
		block, ok := nextWebSSEWithin(t, reader, 10*time.Second)
		if !ok {
			break
		}
		if block.Name == "shutdown" {
			sawShutdown = true
		}
	}
	if !sawShutdown {
		t.Fatalf("stream did not receive event: shutdown; stderr=%q", stderr.String())
	}

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
		}
		if elapsed := time.Since(started); elapsed >= shutdownGrace {
			t.Fatalf("daemon took %s to exit, want less than %s", elapsed, shutdownGrace)
		}
	case <-time.After(shutdownGrace):
		t.Fatalf("daemon did not exit within %s; stderr=%q", shutdownGrace, stderr.String())
	}

	// The turn end is durable even if the stream closed first.
	rt, err := lana.New(context.Background(), lana.Options{Home: home})
	if err != nil {
		t.Fatalf("open runtime after shutdown: %v", err)
	}
	defer func() { _ = rt.Close() }()
	events, err := rt.History(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	for _, event := range events {
		if event.Kind != core.KindTurnEnd {
			continue
		}
		var end core.TurnEnd
		if err := json.Unmarshal(event.Payload, &end); err != nil {
			t.Fatal(err)
		}
		if end.Reason == "cancelled" {
			return
		}
	}
	t.Fatalf("no cancelled turn/end in %d events", len(events))
}

// REQ-TEAM-022: a team session refuses a model change with invalid_request.
// The runtime error wraps ErrInvalidModel, so the port mapping must check the
// team error first.
func TestWebModelSelectionErrorMapsTeamCoversREQTEAM022(t *testing.T) {
	got := modelSelectionError(lana.ErrTeamModel)
	if !errors.Is(got, web.ErrTeamModel) {
		t.Fatalf("modelSelectionError(ErrTeamModel) = %v, want web.ErrTeamModel", got)
	}
	if errors.Is(got, web.ErrInvalidModel) {
		t.Fatalf("team model error mapped to web.ErrInvalidModel: %v", got)
	}
	if got := modelSelectionError(lana.ErrUnknownProvider); !errors.Is(got, web.ErrInvalidModel) {
		t.Fatalf("modelSelectionError(ErrUnknownProvider) = %v, want web.ErrInvalidModel", got)
	}
}

// REQ-MDL-001: the web PATCH writes the session effort, GET returns it, an
// invalid value is rejected, and "Model default" clears it back to the catalog
// default on the next turn.
func TestWebSessionReasoningEffort(t *testing.T) {
	raw := `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.lana]
toolset = "custom"
provider = "mock"
model = "deepseek-flash"

[[routes]]
channel = "web"
agent = "lana"
`
	home := t.TempDir()
	writeLiveConfig(t, home, raw)
	text, end := "ok", "end_turn"
	rt, capture := liveWebRuntime(t, home, mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{Text: &text}, {Stop: &end}}},
		{Events: []mock.Event{{Text: &text}, {Stop: &end}}},
		{Events: []mock.Event{{Text: &text}, {Stop: &end}}},
		{Events: []mock.Event{{Text: &text}, {Stop: &end}}},
	}})
	_, ts, cookie, csrf := webServer(t, rt, home)

	info := webCreateAgentSession(t, rt, context.Background(), "lana")
	runTurn := func() {
		t.Helper()
		if _, err := rt.Run(context.Background(), lana.Request{Prompt: "hi", SessionID: info.ID}); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}
	patchEffort := func(body string) {
		t.Helper()
		resp, data := webDo(t, http.MethodPatch, ts.URL+"/v1/sessions/"+info.ID, body, cookie, csrf)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PATCH %s status = %d, body = %q", body, resp.StatusCode, data)
		}
	}
	readEffort := func() string {
		t.Helper()
		resp, data := webDo(t, http.MethodGet, ts.URL+"/v1/sessions/"+info.ID, "", cookie, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET session status = %d, body = %q", resp.StatusCode, data)
		}
		var detail struct {
			ReasoningEffort string `json:"reasoning_effort"`
		}
		if err := json.Unmarshal(data, &detail); err != nil {
			t.Fatal(err)
		}
		return detail.ReasoningEffort
	}

	// The catalog default applies before any selection.
	runTurn()
	if got := capture.lastReasoningEffort(); got != "high" {
		t.Fatalf("default effort = %q, want high", got)
	}

	// A valid PATCH is stored, returned by GET, and reaches the next turn.
	patchEffort(`{"reasoning_effort":"low"}`)
	if got := readEffort(); got != "low" {
		t.Fatalf("read effort = %q, want low", got)
	}
	runTurn()
	if got := capture.lastReasoningEffort(); got != "low" {
		t.Fatalf("turn effort = %q, want low", got)
	}

	// An invalid value is rejected and changes nothing.
	resp, _ := webDo(t, http.MethodPatch, ts.URL+"/v1/sessions/"+info.ID, `{"reasoning_effort":"turbo"}`, cookie, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid effort status = %d, want 400", resp.StatusCode)
	}
	if got := readEffort(); got != "low" {
		t.Fatalf("effort after invalid = %q, want low", got)
	}

	// "Model default" clears the session effort, so the catalog default returns.
	patchEffort(`{"reasoning_effort":""}`)
	if got := readEffort(); got != "" {
		t.Fatalf("cleared effort = %q, want empty", got)
	}
	runTurn()
	if got := capture.lastReasoningEffort(); got != "high" {
		t.Fatalf("effort after clear = %q, want high", got)
	}
}
