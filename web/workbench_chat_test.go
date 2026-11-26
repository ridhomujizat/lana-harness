package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workbench"
	"github.com/ridhomujizat/lana-harness/internal/workbench/claudechat"
)

// chatTestdataDir is the claudechat package's fixtures, read by the chat tests.
const chatTestdataDir = "../internal/workbench/claudechat/testdata"

// chatTestdata reads a fixture from the claudechat testdata directory.
func chatTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(chatTestdataDir, name))
	if err != nil {
		t.Fatalf("read testdata %s: %v", name, err)
	}
	return b
}

// chatTranscriptFile writes a transcript body to a temp file, with a trailing
// newline so appends land on their own line.
func chatTranscriptFile(t *testing.T, body []byte) string {
	t.Helper()
	data := append([]byte(nil), body...)
	if len(data) == 0 || data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// appendTranscriptLine appends one JSONL line, as Claude Code would.
func appendTranscriptLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

// stubChatLocate points the chat lookup and home at a temp dir and the given
// conversation.
func stubChatLocate(t *testing.T, conv claudechat.Conversation, ok bool) {
	t.Helper()
	home := t.TempDir()
	oldHome, oldLocate := chatHome, chatLocate
	chatHome = func() (string, error) { return home, nil }
	chatLocate = func(int, string) (claudechat.Conversation, bool) { return conv, ok }
	t.Cleanup(func() { chatHome, chatLocate = oldHome, oldLocate })
}

// shortenChatTimers makes the hub poll and its writer/mode waits fast, and
// restores them afterwards.
func shortenChatTimers(t *testing.T) {
	t.Helper()
	poll, idle, writer, enter, gap := chatPollInterval, chatIdleStop, chatWriterIdle, chatEnterDelay, chatKeyGap
	live, modeWait, modePoll, cmdGap := chatLiveInterval, chatModeWait, chatModePoll, chatCmdGap
	chatPollInterval = 5 * time.Millisecond
	chatIdleStop = 60 * time.Millisecond
	chatWriterIdle = 60 * time.Millisecond
	chatEnterDelay = time.Millisecond
	chatKeyGap = time.Millisecond
	chatLiveInterval = 0
	chatModeWait = 25 * time.Millisecond
	chatModePoll = 2 * time.Millisecond
	chatCmdGap = time.Millisecond
	t.Cleanup(func() {
		chatPollInterval, chatIdleStop, chatWriterIdle, chatEnterDelay, chatKeyGap = poll, idle, writer, enter, gap
		chatLiveInterval, chatModeWait, chatModePoll, chatCmdGap = live, modeWait, modePoll, cmdGap
	})
}

// stopHubsAfter stops a test's chat hubs before its package-var restores run,
// so a lingering poll loop cannot race the restores.
func stopHubsAfter(t *testing.T, s *Server) {
	t.Helper()
	t.Cleanup(func() {
		s.chatMu.Lock()
		hubs := make([]*chatHub, 0, len(s.chatHubs))
		for _, hub := range s.chatHubs {
			hubs = append(hubs, hub)
		}
		s.chatMu.Unlock()
		s.stopChatHubs()
		// stopChatHubs gives each loop a second; wait longer under a loaded
		// race run, so no loop is left reading the vars being restored.
		for _, hub := range hubs {
			waitHubLoopDone(t, hub)
		}
	})
}

// stopHubAfter stops a hub the test built with getChatHub and waits for its
// poll loop to end, before the package-var restores of shortenChatTimers and
// stubChatLocate run (cleanups run last-registered first, so call it after
// them). A deferred hub.stop() is not enough: stop drops the hub from the
// server's map, so stopHubsAfter no longer sees it, and its loop can still be
// inside refresh reading chatLocate or idleExpired reading chatIdleStop while
// the cleanups restore them.
func stopHubAfter(t *testing.T, hub *chatHub) {
	t.Helper()
	t.Cleanup(func() {
		hub.stop()
		waitHubLoopDone(t, hub)
	})
}

// waitHubLoopDone waits for a stopped hub's poll loop to return.
func waitHubLoopDone(t *testing.T, hub *chatHub) {
	t.Helper()
	select {
	case <-hub.loopDone:
	case <-time.After(10 * time.Second):
		t.Error("chat hub poll loop did not end")
	}
}

// lengthenChatSwapWait makes a resize's view swap wait for the core's
// replay-end marker for the whole test, so a check that the old mirror still
// serves cannot be undone by the fallback firing on a loaded machine, and a
// swap the test sees is the marker's.
func lengthenChatSwapWait(t *testing.T) {
	t.Helper()
	old := chatViewSwapWait
	chatViewSwapWait = time.Minute
	t.Cleanup(func() { chatViewSwapWait = old })
}

// chatStream reads SSE events off a live chat stream in the background.
type chatStream struct {
	events chan []byte
	errs   chan error
	stop   func()
}

func openChatStream(t *testing.T, addr, id string, cookie *http.Cookie) *chatStream {
	t.Helper()
	url := "http://" + addr + "/v1/workbench/sessions/" + id + "/chat/stream"
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		cancel()
		t.Fatalf("stream status = %d", resp.StatusCode)
	}
	stop := func() {
		cancel()
		_ = resp.Body.Close()
	}
	t.Cleanup(stop)
	cs := &chatStream{events: make(chan []byte, 64), errs: make(chan error, 1), stop: stop}
	go func() {
		br := bufio.NewReader(resp.Body)
		for {
			data, err := readChatSSE(br)
			if err != nil {
				cs.errs <- err
				return
			}
			cs.events <- data
		}
	}()
	return cs
}

// readChatSSE reads one SSE event's data payload.
func readChatSSE(br *bufio.Reader) ([]byte, error) {
	var data []byte
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if data != nil {
				return data, nil
			}
		case strings.HasPrefix(line, ":"):
			// heartbeat comment
		case strings.HasPrefix(line, "data: "):
			data = []byte(strings.TrimPrefix(line, "data: "))
		}
	}
}

func (cs *chatStream) next(t *testing.T) []byte {
	t.Helper()
	select {
	case ev := <-cs.events:
		return ev
	case err := <-cs.errs:
		t.Fatalf("stream read: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a chat event")
	}
	return nil
}

// waitChatItem reads events until one carries an item with the given id and
// status, or the deadline passes.
func waitChatItem(t *testing.T, cs *chatStream, id, status string) claudechat.Item {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var up ChatUpdate
		if err := json.Unmarshal(cs.next(t), &up); err != nil {
			t.Fatal(err)
		}
		for _, it := range up.Items {
			if it.ID == id && it.Status == status {
				return it
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no item %s with status %q", id, status)
		}
	}
}

// waitFakeStream waits for a session/mode stream to appear on the fake.
func waitFakeStream(t *testing.T, fake *fakeWorkbench, id, mode string) *fakeWorkbenchStream {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if s := fake.streamFor(id, mode); s != nil {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s/%s stream", id, mode)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// waitChatDialog waits for the hub's mirror to show a dialog.
func waitChatDialog(t *testing.T, hub *chatHub) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if hub.hasDialog() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no dialog on the mirror")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitFakeStreamChange waits for a session/mode stream other than prev to
// appear on the fake, so a re-attach can be told from the first attach.
func waitFakeStreamChange(t *testing.T, fake *fakeWorkbench, id, mode string, prev *fakeWorkbenchStream) *fakeWorkbenchStream {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if s := fake.streamFor(id, mode); s != nil && s != prev {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("no new %s/%s stream", id, mode)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// hubMirror reads the hub's current screen mirror.
func hubMirror(hub *chatHub) *claudechat.Mirror {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return hub.mirror
}

// waitHubMirror waits for the hub's mirror to come up.
func waitHubMirror(t *testing.T, hub *chatHub) *claudechat.Mirror {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if m := hubMirror(hub); m != nil {
			return m
		}
		if time.Now().After(deadline) {
			t.Fatal("hub has no mirror")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// waitHubMirrorChange waits for the hub's mirror to become something other than
// prev, as it does after a view swap.
func waitHubMirrorChange(t *testing.T, hub *chatHub, prev *claudechat.Mirror) *claudechat.Mirror {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if m := hubMirror(hub); m != nil && m != prev {
			return m
		}
		if time.Now().After(deadline) {
			t.Fatal("hub mirror did not change")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// waitStreamClosed waits for a fake attachment to be closed. A view swap
// publishes the new mirror under the hub lock and closes the old stream after
// it, so the close can trail the mirror change a test observes.
func waitStreamClosed(t *testing.T, stream *fakeWorkbenchStream, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !stream.isClosed() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// waitChatHubGone waits for the server to drop a session's chat hub.
func waitChatHubGone(t *testing.T, s *Server, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if s.lookupChatHub(id) == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("chat hub was not removed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// chatFooter renders the footer the mode parser reads.
func chatFooter(mode string) string {
	phrase := map[string]string{
		"default":     "manual mode on",
		"acceptEdits": "accept edits on",
		"plan":        "plan mode on",
		"auto":        "auto mode on",
	}[mode]
	return "────────────────────\r\n❯ \r\n────────────────────\r\n  ⏵⏵ " + phrase +
		" (shift+tab to cycle) · esc to interrupt\r\n"
}

func TestWorkbenchChatSnapshot(t *testing.T) {
	path := chatTranscriptFile(t, chatTestdata(t, "basic.jsonl"))
	stubChatLocate(t, claudechat.Conversation{PID: 4242, SessionID: "3f2a9c1e-0000-0000-0000-000000000000", Cwd: "/work", Path: path}, true)

	s, cookie, csrf := newWorkbenchServer(t)

	stopHubsAfter(t, s)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc", Agent: "claude"}}
	s.SetWorkbench(fake)

	rr := workbenchRequest(s, http.MethodGet, "/v1/workbench/sessions/abc/chat", "", cookie, csrf)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var up ChatUpdate
	if err := json.Unmarshal(rr.Body.Bytes(), &up); err != nil {
		t.Fatal(err)
	}
	if !up.Available || !up.Reset || up.Agent != "claude" {
		t.Fatalf("snapshot = %+v", up)
	}
	if up.Conversation == nil || up.Conversation.ID != "3f2a9c1e-0000-0000-0000-000000000000" {
		t.Fatalf("conversation = %+v", up.Conversation)
	}
	if len(up.Items) == 0 || up.Meta == nil || up.Meta.Model != "claude-opus-5-5" {
		t.Fatalf("items = %d, meta = %+v", len(up.Items), up.Meta)
	}
	if up.Live != nil {
		t.Fatalf("live = %+v, want nil without a hub", up.Live)
	}

	// A ?before page goes back, without reset.
	last := up.Items[len(up.Items)-1].ID
	rr = workbenchRequest(s, http.MethodGet, "/v1/workbench/sessions/abc/chat?before="+last, "", cookie, csrf)
	if rr.Code != http.StatusOK {
		t.Fatalf("page status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var page ChatUpdate
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Reset || len(page.Items) == 0 {
		t.Fatalf("page = %+v", page)
	}
	for _, it := range page.Items {
		if it.ID == last {
			t.Fatalf("page still holds %q", last)
		}
	}
}

func TestWorkbenchChatUnavailableReasons(t *testing.T) {
	stubChatLocate(t, claudechat.Conversation{}, false)
	s, cookie, csrf := newWorkbenchServer(t)

	stopHubsAfter(t, s)

	cases := []struct {
		name   string
		agent  string
		reason string
	}{
		{"no agent", "", "no_agent"},
		{"unsupported", "omp", "unsupported_agent"},
		{"waiting", "claude", "waiting"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeWorkbench()
			fake.sessions = []workbench.Session{{ID: "abc", Agent: tc.agent}}
			s.SetWorkbench(fake)

			rr := workbenchRequest(s, http.MethodGet, "/v1/workbench/sessions/abc/chat", "", cookie, csrf)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
			}
			var up ChatUpdate
			if err := json.Unmarshal(rr.Body.Bytes(), &up); err != nil {
				t.Fatal(err)
			}
			if up.Available || up.Reason != tc.reason || up.Agent != tc.agent {
				t.Fatalf("update = %+v, want reason %q", up, tc.reason)
			}
		})
	}
}

func TestWorkbenchChatStream(t *testing.T) {
	shortenChatTimers(t)
	path := chatTranscriptFile(t, chatTestdata(t, "basic.jsonl"))
	stubChatLocate(t, claudechat.Conversation{PID: 7, SessionID: "sid-1", Cwd: "/work", Path: path}, true)

	s, addr := newWorkbenchLiveServer(t)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc", Agent: "claude", AgentState: "idle", Cols: 120, Rows: 40}}
	s.SetWorkbench(fake)
	cookie := workbenchSessionCookie(t, s)

	stream := openChatStream(t, addr, "abc", cookie)

	var first ChatUpdate
	if err := json.Unmarshal(stream.next(t), &first); err != nil {
		t.Fatal(err)
	}
	if !first.Reset || !first.Available || len(first.Items) == 0 {
		t.Fatalf("first event = %+v", first)
	}
	if first.Meta == nil || first.Meta.Model != "claude-opus-5-5" {
		t.Fatalf("first meta = %+v", first.Meta)
	}

	appendTranscriptLine(t, path,
		`{"type":"assistant","uuid":"a9","timestamp":"2026-11-23T12:00:20.000Z","message":{"id":"m9","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"all done"}],"usage":{"input_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":1}}}`)

	var delta ChatUpdate
	if err := json.Unmarshal(stream.next(t), &delta); err != nil {
		t.Fatal(err)
	}
	if len(delta.Items) != 1 || delta.Items[0].ID != "a9:0" || delta.Items[0].Text != "all done" {
		t.Fatalf("delta = %+v", delta)
	}
	if delta.Reset {
		t.Fatalf("delta reset = true, want false")
	}
}

func TestWorkbenchChatStreamToolResult(t *testing.T) {
	shortenChatTimers(t)
	path := chatTranscriptFile(t, chatTestdata(t, "basic.jsonl"))
	stubChatLocate(t, claudechat.Conversation{PID: 7, SessionID: "sid-1", Cwd: "/work", Path: path}, true)

	s, addr := newWorkbenchLiveServer(t)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc", Agent: "claude", Cols: 120, Rows: 40}}
	s.SetWorkbench(fake)
	cookie := workbenchSessionCookie(t, s)

	stream := openChatStream(t, addr, "abc", cookie)
	stream.next(t) // snapshot

	appendTranscriptLine(t, path,
		`{"type":"assistant","uuid":"a9","timestamp":"2026-11-23T12:00:20.000Z","message":{"id":"m9","role":"assistant","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"toolu_9","name":"Read","input":{"file_path":"/work/x.go"}}],"usage":{"input_tokens":5,"output_tokens":1}}}`)
	if it := waitChatItem(t, stream, "a9:0", "running"); it.Kind != "tool" {
		t.Fatalf("running item = %+v", it)
	}

	appendTranscriptLine(t, path,
		`{"type":"user","uuid":"r9","timestamp":"2026-11-23T12:00:21.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_9","content":"package x","is_error":false}]}}`)
	it := waitChatItem(t, stream, "a9:0", "done")
	if it.Result == nil || it.Result.Text != "package x" {
		t.Fatalf("done item = %+v", it)
	}
}

func TestWorkbenchChatStreamLive(t *testing.T) {
	shortenChatTimers(t)
	path := chatTranscriptFile(t, chatTestdata(t, "basic.jsonl"))
	stubChatLocate(t, claudechat.Conversation{PID: 7, SessionID: "sid-1", Cwd: "/work", Path: path}, true)

	s, addr := newWorkbenchLiveServer(t)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc", Agent: "claude", AgentState: "idle", Cols: 120, Rows: 40}}
	s.SetWorkbench(fake)
	cookie := workbenchSessionCookie(t, s)

	stream := openChatStream(t, addr, "abc", cookie)
	stream.next(t) // snapshot

	view := waitFakeStream(t, fake, "abc", "view")
	view.push([]byte("────────────────────\r\n❯ \r\n────────────────────\r\n  ⏵⏵ accept edits on (shift+tab to cycle) · esc to interrupt\r\n"))

	deadline := time.Now().Add(3 * time.Second)
	for {
		var up ChatUpdate
		if err := json.Unmarshal(stream.next(t), &up); err != nil {
			t.Fatal(err)
		}
		if up.Live != nil && up.Live.Mode == "acceptEdits" {
			if up.Live.State != "idle" {
				t.Fatalf("live state = %q, want idle", up.Live.State)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no live event with acceptEdits")
		}
	}
}

func TestWorkbenchChatSay(t *testing.T) {
	shortenChatTimers(t)
	stubChatLocate(t, claudechat.Conversation{}, false)
	s, cookie, csrf := newWorkbenchServer(t)

	stopHubsAfter(t, s)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc", Agent: "claude"}}
	s.SetWorkbench(fake)

	rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/abc/chat/say", `{"text":"hello"}`, cookie, csrf)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("say status = %d, body = %q", rr.Code, rr.Body.String())
	}
	ctrl := fake.streamFor("abc", "control")
	if ctrl == nil {
		t.Fatal("no control stream")
	}
	if got, want := string(ctrl.input()), "\x15\x1b[200~hello\x1b[201~\r"; got != want {
		t.Fatalf("control input = %q, want %q", got, want)
	}

	// An empty say is refused.
	rr = workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/abc/chat/say", `{"text":"  "}`, cookie, csrf)
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")

	// A path-only say is accepted.
	rr = workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/abc/chat/say", `{"paths":["/tmp/a.png"]}`, cookie, csrf)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("path say status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

func TestWorkbenchChatSayDialog(t *testing.T) {
	shortenChatTimers(t)
	path := chatTranscriptFile(t, chatTestdata(t, "basic.jsonl"))
	stubChatLocate(t, claudechat.Conversation{PID: 7, SessionID: "sid-1", Cwd: "/work", Path: path}, true)

	s, cookie, csrf := newWorkbenchServer(t)

	stopHubsAfter(t, s)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc", Agent: "claude", AgentState: "blocked", Cols: 200, Rows: 40}}
	s.SetWorkbench(fake)

	hub := s.getChatHub("abc")
	stopHubAfter(t, hub)
	hub.addView()

	view := waitFakeStream(t, fake, "abc", "view")
	perm := chatTestdata(t, "screens/p-permission.txt")
	view.push([]byte(strings.ReplaceAll(string(perm), "\n", "\r\n")))
	waitChatDialog(t, hub)

	rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/abc/chat/say", `{"text":"hi"}`, cookie, csrf)
	assertAuthError(t, rr, http.StatusConflict, "dialog_open")

	rr = workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/abc/chat/say", `{"text":"hi","force":true}`, cookie, csrf)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("force say status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

// TestChatHubHasDialog covers that hasDialog follows the hub's live state, the
// state the stream publishes: a parsed dialog the chat hides must not report an
// open dialog.
func TestChatHubHasDialog(t *testing.T) {
	hub := newChatHub(nil, "abc")
	mirror := claudechat.NewMirror(200, 40)
	trust := chatTestdata(t, "screens/00-trust.txt")
	if _, err := mirror.Write([]byte(strings.ReplaceAll(string(trust), "\n", "\r\n"))); err != nil {
		t.Fatal(err)
	}
	hub.mirror = mirror

	// The trust prompt parses as a dialog, but the session is idle and its
	// kind is one the chat does not show, so the live state holds none.
	hub.updateLive(workbench.Session{ID: "abc", AgentState: "idle", Cols: 200, Rows: 40})
	if hub.hasDialog() {
		t.Fatal("hasDialog = true, want false for a dialog the live state hides")
	}

	// A blocked session keeps the dialog, and hasDialog follows it.
	hub.updateLive(workbench.Session{ID: "abc", AgentState: "blocked", Cols: 200, Rows: 40})
	if !hub.hasDialog() {
		t.Fatal("hasDialog = false, want true for a blocked session")
	}
}

func TestWorkbenchChatKeys(t *testing.T) {
	shortenChatTimers(t)
	stubChatLocate(t, claudechat.Conversation{}, false)
	s, cookie, csrf := newWorkbenchServer(t)

	stopHubsAfter(t, s)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc", Agent: "claude"}}
	s.SetWorkbench(fake)

	rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/abc/chat/keys", `{"keys":["1","enter"]}`, cookie, csrf)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("keys status = %d, body = %q", rr.Code, rr.Body.String())
	}
	ctrl := fake.streamFor("abc", "control")
	if ctrl == nil {
		t.Fatal("no control stream")
	}
	if got := string(ctrl.input()); got != "1\r" {
		t.Fatalf("control input = %q, want %q", got, "1\r")
	}

	rr = workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/abc/chat/keys", `{"keys":["bogus"]}`, cookie, csrf)
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_key")
	if got := string(ctrl.input()); got != "1\r" {
		t.Fatalf("invalid keys wrote %q, want nothing new", got)
	}
}

func TestWorkbenchChatMode(t *testing.T) {
	shortenChatTimers(t)
	path := chatTranscriptFile(t, chatTestdata(t, "basic.jsonl"))
	stubChatLocate(t, claudechat.Conversation{PID: 7, SessionID: "sid-1", Cwd: "/work", Path: path}, true)

	s, cookie, csrf := newWorkbenchServer(t)

	stopHubsAfter(t, s)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc", Agent: "claude", Cols: 120, Rows: 40}}
	s.SetWorkbench(fake)

	modes := []string{"acceptEdits", "plan"}
	var mu sync.Mutex
	idx := 0
	fake.controlHook = func(p []byte) {
		if !bytes.Contains(p, []byte("\x1b[Z")) {
			return
		}
		mu.Lock()
		m := modes[len(modes)-1]
		if idx < len(modes) {
			m = modes[idx]
			idx++
		}
		mu.Unlock()
		fake.pushView("abc", []byte(chatFooter(m)))
	}

	rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/abc/chat/mode", `{"mode":"plan"}`, cookie, csrf)
	if rr.Code != http.StatusOK {
		t.Fatalf("mode status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Mode != "plan" {
		t.Fatalf("mode = %q, want plan", body.Mode)
	}
}

func TestWorkbenchChatModeUnreachable(t *testing.T) {
	shortenChatTimers(t)
	path := chatTranscriptFile(t, chatTestdata(t, "basic.jsonl"))
	stubChatLocate(t, claudechat.Conversation{PID: 7, SessionID: "sid-1", Cwd: "/work", Path: path}, true)

	s, cookie, csrf := newWorkbenchServer(t)

	stopHubsAfter(t, s)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc", Agent: "claude", Cols: 120, Rows: 40}}
	s.SetWorkbench(fake)
	fake.controlHook = func(p []byte) {
		if bytes.Contains(p, []byte("\x1b[Z")) {
			fake.pushView("abc", []byte(chatFooter("acceptEdits")))
		}
	}

	rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/abc/chat/mode", `{"mode":"plan"}`, cookie, csrf)
	assertAuthError(t, rr, http.StatusConflict, "mode_unavailable")
	if !strings.Contains(rr.Body.String(), "acceptEdits") {
		t.Fatalf("mode_unavailable body = %q, want the modes seen", rr.Body.String())
	}
}

func TestWorkbenchChatModel(t *testing.T) {
	shortenChatTimers(t)
	stubChatLocate(t, claudechat.Conversation{}, false)
	s, cookie, csrf := newWorkbenchServer(t)

	stopHubsAfter(t, s)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc", Agent: "claude", AgentState: "working"}}
	s.SetWorkbench(fake)

	rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/abc/chat/model", `{"model":"opus"}`, cookie, csrf)
	assertAuthError(t, rr, http.StatusConflict, "busy")

	fake.setSession("abc", func(s *workbench.Session) { s.AgentState = "idle" })

	rr = workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/abc/chat/model", `{"model":"not a model!"}`, cookie, csrf)
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")

	rr = workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/abc/chat/model", `{"model":"sonnet","effort":"high"}`, cookie, csrf)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("model status = %d, body = %q", rr.Code, rr.Body.String())
	}
	ctrl := fake.streamFor("abc", "control")
	if ctrl == nil {
		t.Fatal("no control stream")
	}
	got := string(ctrl.input())
	if !strings.Contains(got, "/model sonnet") || !strings.Contains(got, "/effort high") {
		t.Fatalf("control input = %q, want both commands", got)
	}
}

func TestWorkbenchChatCommands(t *testing.T) {
	stubChatLocate(t, claudechat.Conversation{}, false)
	s, cookie, csrf := newWorkbenchServer(t)

	stopHubsAfter(t, s)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc", Agent: "claude", Cwd: "/work"}}
	s.SetWorkbench(fake)

	rr := workbenchRequest(s, http.MethodGet, "/v1/workbench/sessions/abc/chat/commands", "", cookie, csrf)
	if rr.Code != http.StatusOK {
		t.Fatalf("commands status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var body struct {
		Commands []claudechat.Command `json:"commands"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range body.Commands {
		if c.Name == "/compact" && c.Source == "built-in" {
			found = true
		}
	}
	if !found {
		t.Fatalf("built-ins missing from %+v", body.Commands)
	}
}

func TestWorkbenchChatImage(t *testing.T) {
	path := chatTranscriptFile(t, chatTestdata(t, "images.jsonl"))
	stubChatLocate(t, claudechat.Conversation{PID: 7, SessionID: "sid-1", Cwd: "/work", Path: path}, true)
	s, cookie, csrf := newWorkbenchServer(t)

	stopHubsAfter(t, s)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc", Agent: "claude"}}
	s.SetWorkbench(fake)

	rr := workbenchRequest(s, http.MethodGet, "/v1/workbench/sessions/abc/chat/image?item=u1&i=1", "", cookie, csrf)
	if rr.Code != http.StatusOK {
		t.Fatalf("image status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("content type = %q, want image/png", got)
	}
	if rr.Body.String() != "hello" {
		t.Fatalf("image body = %q, want hello", rr.Body.String())
	}
	if got := rr.Header().Get("Cache-Control"); got != "private, max-age=3600" {
		t.Fatalf("cache control = %q", got)
	}

	rr = workbenchRequest(s, http.MethodGet, "/v1/workbench/sessions/abc/chat/image?item=u1&i=9", "", cookie, csrf)
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
}

func TestWorkbenchChatAgent(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "sid.jsonl")
	subDir := filepath.Join(dir, "sid", "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := strings.Join([]string{
		`{"type":"user","uuid":"s1","isSidechain":true,"timestamp":"2026-11-23T12:00:00Z","message":{"role":"user","content":"do the thing"}}`,
		`{"type":"assistant","uuid":"s2","isSidechain":true,"timestamp":"2026-11-23T12:00:01Z","message":{"id":"m1","role":"assistant","model":"claude-haiku-5-5","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/work/a.go"}}]}}`,
		`{"type":"assistant","uuid":"s3","isSidechain":true,"timestamp":"2026-11-23T12:00:02Z","message":{"id":"m2","role":"assistant","model":"claude-haiku-5-5","content":[{"type":"text","text":"done"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(subDir, "agent-abc.jsonl"), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	meta := `{"agentType":"dsh-coder","description":"T-002 transcript reader","toolUseId":"toolu_1","model":"claude-haiku-5-5","finished":true}`
	if err := os.WriteFile(filepath.Join(subDir, "agent-abc.meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}

	stubChatLocate(t, claudechat.Conversation{PID: 7, SessionID: "sid", Cwd: "/work", Path: transcriptPath}, true)
	s, cookie, csrf := newWorkbenchServer(t)

	stopHubsAfter(t, s)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc", Agent: "claude"}}
	s.SetWorkbench(fake)

	rr := workbenchRequest(s, http.MethodGet, "/v1/workbench/sessions/abc/chat/agents/abc", "", cookie, csrf)
	if rr.Code != http.StatusOK {
		t.Fatalf("agent status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var body struct {
		AgentID     string            `json:"agent_id"`
		AgentType   string            `json:"agent_type"`
		Description string            `json:"description"`
		Model       string            `json:"model"`
		Finished    bool              `json:"finished"`
		Steps       int               `json:"steps"`
		Items       []claudechat.Item `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.AgentID != "abc" || body.AgentType != "dsh-coder" || body.Description != "T-002 transcript reader" {
		t.Fatalf("agent meta = %+v", body)
	}
	if body.Model != "claude-haiku-5-5" || !body.Finished || body.Steps != 1 {
		t.Fatalf("agent model/finished/steps = %+v", body)
	}
	if len(body.Items) != 3 {
		t.Fatalf("agent items = %+v", body.Items)
	}

	rr = workbenchRequest(s, http.MethodGet, "/v1/workbench/sessions/abc/chat/agents/bad.id", "", cookie, csrf)
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_request")

	rr = workbenchRequest(s, http.MethodGet, "/v1/workbench/sessions/abc/chat/agents/missing", "", cookie, csrf)
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
}

func TestWorkbenchChatNotClaude(t *testing.T) {
	stubChatLocate(t, claudechat.Conversation{}, false)
	s, cookie, csrf := newWorkbenchServer(t)

	stopHubsAfter(t, s)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc", Agent: "omp"}}
	s.SetWorkbench(fake)

	rr := workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/abc/chat/say", `{"text":"hi"}`, cookie, csrf)
	assertAuthError(t, rr, http.StatusConflict, "not_claude")
}

func TestWorkbenchChatControlLocked(t *testing.T) {
	stubChatLocate(t, claudechat.Conversation{}, false)
	s, token, _ := newRemoteTestServer(t, remoteEnabledConfig())
	cookie, csrf := remoteTokenLogin(t, s, token)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc", Agent: "claude"}}
	s.SetWorkbench(fake)

	rr := serveAuth(s, remoteControlRequest(http.MethodPost, "/v1/workbench/sessions/abc/chat/say", `{"text":"hi"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusForbidden, "control_locked")
}

func TestWorkbenchChatHubIdleStop(t *testing.T) {
	shortenChatTimers(t)
	path := chatTranscriptFile(t, chatTestdata(t, "basic.jsonl"))
	stubChatLocate(t, claudechat.Conversation{PID: 7, SessionID: "sid-1", Cwd: "/work", Path: path}, true)

	s, addr := newWorkbenchLiveServer(t)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc", Agent: "claude", Cols: 120, Rows: 40}}
	s.SetWorkbench(fake)
	cookie := workbenchSessionCookie(t, s)

	stream := openChatStream(t, addr, "abc", cookie)
	stream.next(t) // snapshot
	view := waitFakeStream(t, fake, "abc", "view")

	stream.stop()

	waitChatHubGone(t, s, "abc")
	if !view.isClosed() {
		t.Fatal("view attachment was not closed")
	}
}

// TestChatHubResizeReattaches checks that a pane size change makes the hub open
// a new view attachment instead of resizing the running mirror, and that the new
// attachment replaces the old stream and mirror once the core's replay-end
// marker arrives.
func TestChatHubResizeReattaches(t *testing.T) {
	shortenChatTimers(t)
	lengthenChatSwapWait(t)
	// addView alone leaves the hub with no subscriber, so keep it from idling
	// out while the test drives it.
	chatIdleStop = time.Minute
	path := chatTranscriptFile(t, chatTestdata(t, "basic.jsonl"))
	stubChatLocate(t, claudechat.Conversation{PID: 7, SessionID: "sid-1", Cwd: "/work", Path: path}, true)

	s, _, _ := newWorkbenchServer(t)

	stopHubsAfter(t, s)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc", Agent: "claude", AgentState: "idle", Cols: 120, Rows: 40}}
	s.SetWorkbench(fake)

	hub := s.getChatHub("abc")
	stopHubAfter(t, hub)
	hub.addView()

	view1 := waitFakeStream(t, fake, "abc", "view")
	mirror1 := waitHubMirror(t, hub)

	fake.setSession("abc", func(sess *workbench.Session) { sess.Cols = 200; sess.Rows = 50 })
	view2 := waitFakeStreamChange(t, fake, "abc", "view", view1)

	// The old stream and mirror keep serving until the replay-end marker.
	if got := hubMirror(hub); got != mirror1 {
		t.Fatal("hub swapped the mirror before the replay-end marker")
	}

	view2.push([]byte("redrawn\r\n\x1b]7702\x07"))
	if got := waitHubMirrorChange(t, hub, mirror1); got == mirror1 {
		t.Fatal("hub kept the old mirror after the replay-end marker")
	}
	waitStreamClosed(t, view1, "old view stream was not closed after the swap")
}

// TestChatHubResizeWaitsForReplay checks that the old mirror keeps serving until
// the new attachment has replayed. chatViewSwapWait is lengthened so its
// fallback cannot fire inside the test, however slow the machine.
func TestChatHubResizeWaitsForReplay(t *testing.T) {
	shortenChatTimers(t)
	lengthenChatSwapWait(t)
	// addView alone leaves the hub with no subscriber, so keep it from idling
	// out while the test drives it.
	chatIdleStop = time.Minute
	path := chatTranscriptFile(t, chatTestdata(t, "basic.jsonl"))
	stubChatLocate(t, claudechat.Conversation{PID: 7, SessionID: "sid-1", Cwd: "/work", Path: path}, true)

	s, _, _ := newWorkbenchServer(t)

	stopHubsAfter(t, s)
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: "abc", Agent: "claude", AgentState: "idle", Cols: 120, Rows: 40}}
	s.SetWorkbench(fake)

	hub := s.getChatHub("abc")
	stopHubAfter(t, hub)
	hub.addView()

	view1 := waitFakeStream(t, fake, "abc", "view")
	mirror1 := waitHubMirror(t, hub)

	fake.setSession("abc", func(sess *workbench.Session) { sess.Cols = 200; sess.Rows = 50 })
	view2 := waitFakeStreamChange(t, fake, "abc", "view", view1)

	// A redraw without the replay-end marker must not swap the mirror, and the
	// old attachment must stay open, while chatViewSwapWait has not passed.
	view2.push([]byte("partial redraw\r\n"))
	time.Sleep(50 * time.Millisecond)
	if got := hubMirror(hub); got != mirror1 {
		t.Fatal("hub swapped the mirror before the replay-end marker")
	}
	if view1.isClosed() {
		t.Fatal("old view stream closed before the replay-end marker")
	}

	view2.push([]byte("\x1b]7702\x07"))
	waitHubMirrorChange(t, hub, mirror1)
}

func TestWorkbenchChatUnavailableWithoutBackend(t *testing.T) {
	s, cookie, csrf := newWorkbenchServer(t)

	stopHubsAfter(t, s)
	rr := workbenchRequest(s, http.MethodGet, "/v1/workbench/sessions/abc/chat", "", cookie, csrf)
	assertAuthError(t, rr, http.StatusServiceUnavailable, "workbench_unavailable")
	rr = workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/abc/chat/say", `{"text":"hi"}`, cookie, csrf)
	assertAuthError(t, rr, http.StatusServiceUnavailable, "workbench_unavailable")
}

func TestWorkbenchChatUnknownSession(t *testing.T) {
	stubChatLocate(t, claudechat.Conversation{}, false)
	s, cookie, csrf := newWorkbenchServer(t)

	stopHubsAfter(t, s)
	s.SetWorkbench(newFakeWorkbench())

	rr := workbenchRequest(s, http.MethodGet, "/v1/workbench/sessions/missing/chat", "", cookie, csrf)
	assertAuthError(t, rr, http.StatusNotFound, "not_found")

	rr = workbenchRequest(s, http.MethodPost, "/v1/workbench/sessions/missing/chat/keys", `{"keys":["enter"]}`, cookie, csrf)
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
}

// newChatFilesServer returns a logged-in server whose preview policy allows a
// resolved temp tree, so the chat files route can walk it, and root, that tree.
// The shared walk cache is cleared so a test never reads another test's root.
func newChatFilesServer(t *testing.T) (*Server, *http.Cookie, string) {
	t.Helper()
	s, cookie, _ := newWorkbenchServer(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	s.previewHome = home
	s.previewTemps = []string{root}
	chatFilesCacheMu.Lock()
	chatFilesCache = map[string]chatFilesEntry{}
	chatFilesCacheMu.Unlock()
	return s, cookie, root
}

// setChatFilesSession installs a fake backend with one Claude session at cwd.
func setChatFilesSession(s *Server, id, cwd string) *fakeWorkbench {
	fake := newFakeWorkbench()
	fake.sessions = []workbench.Session{{ID: id, Agent: "claude", Cwd: cwd}}
	s.SetWorkbench(fake)
	return fake
}

// chatFilesGet sends an authenticated GET to the chat files route and decodes
// its body.
func chatFilesGet(t *testing.T, s *Server, cookie *http.Cookie, id, q string) (*httptest.ResponseRecorder, chatFilesBody) {
	t.Helper()
	target := "/v1/workbench/sessions/" + id + "/chat/files?q=" + url.QueryEscape(q)
	rr := workbenchRequest(s, http.MethodGet, target, "", cookie, "")
	var body chatFilesBody
	if rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body %q: %v", rr.Body.String(), err)
		}
	}
	return rr, body
}

func TestWorkbenchChatFilesWalk(t *testing.T) {
	stubChatLocate(t, claudechat.Conversation{}, false)
	s, cookie, root := newChatFilesServer(t)
	cwd := filepath.Join(root, "proj")
	writePreviewFile(t, filepath.Join(cwd, "a.txt"), "a")
	writePreviewFile(t, filepath.Join(cwd, "src", "main.go"), "package main")
	writePreviewFile(t, filepath.Join(cwd, "src", "sub", "deep.txt"), "deep")
	writePreviewFile(t, filepath.Join(cwd, "node_modules", "pkg.js"), "x")
	writePreviewFile(t, filepath.Join(cwd, "dist", "out.js"), "x")
	writePreviewFile(t, filepath.Join(cwd, ".hidden", "x.txt"), "x")
	setChatFilesSession(s, "abc", cwd)

	rr, body := chatFilesGet(t, s, cookie, "abc", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if body.Root != cwd {
		t.Fatalf("root = %q, want %q", body.Root, cwd)
	}
	want := []string{"a.txt", "src/", "src/main.go", "src/sub/", "src/sub/deep.txt"}
	if !reflect.DeepEqual(body.Files, want) {
		t.Fatalf("files = %v, want %v", body.Files, want)
	}

	// A query that matches nothing answers an empty list, never null.
	rr, none := chatFilesGet(t, s, cookie, "abc", "zzz")
	if rr.Code != http.StatusOK {
		t.Fatalf("no-match status = %d", rr.Code)
	}
	if none.Files == nil || len(none.Files) != 0 {
		t.Fatalf("no-match files = %#v, want an empty non-null list", none.Files)
	}
	if !strings.Contains(rr.Body.String(), `"files":[]`) {
		t.Fatalf("no-match body = %q, want files []", rr.Body.String())
	}
}

func TestWorkbenchChatFilesQueryRanking(t *testing.T) {
	stubChatLocate(t, claudechat.Conversation{}, false)
	s, cookie, root := newChatFilesServer(t)
	cwd := filepath.Join(root, "proj")
	writePreviewFile(t, filepath.Join(cwd, "src", "main.go"), "x")
	writePreviewFile(t, filepath.Join(cwd, "m", "a", "i", "n.txt"), "x")
	writePreviewFile(t, filepath.Join(cwd, "other.txt"), "x")
	setChatFilesSession(s, "abc", cwd)

	// The base-name substring hit ranks before the subsequence-only hit.
	rr, body := chatFilesGet(t, s, cookie, "abc", "main")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	want := []string{"src/main.go", "m/a/i/n.txt"}
	if !reflect.DeepEqual(body.Files, want) {
		t.Fatalf("files = %v, want %v", body.Files, want)
	}

	// The match ignores case.
	_, upper := chatFilesGet(t, s, cookie, "abc", "MAIN")
	if !reflect.DeepEqual(upper.Files, want) {
		t.Fatalf("uppercase files = %v, want %v", upper.Files, want)
	}
}

func TestWorkbenchChatFilesCache(t *testing.T) {
	stubChatLocate(t, claudechat.Conversation{}, false)
	s, cookie, root := newChatFilesServer(t)
	cwd := filepath.Join(root, "proj")
	writePreviewFile(t, filepath.Join(cwd, "cacheme.txt"), "x")
	setChatFilesSession(s, "abc", cwd)

	_, body := chatFilesGet(t, s, cookie, "abc", "cacheme")
	if !reflect.DeepEqual(body.Files, []string{"cacheme.txt"}) {
		t.Fatalf("files = %v, want the walked entry", body.Files)
	}

	// Removing the file does not change the answer while the walk is cached.
	if err := os.Remove(filepath.Join(cwd, "cacheme.txt")); err != nil {
		t.Fatal(err)
	}
	_, again := chatFilesGet(t, s, cookie, "abc", "cacheme")
	if !reflect.DeepEqual(again.Files, []string{"cacheme.txt"}) {
		t.Fatalf("cached files = %v, want the stale entry", again.Files)
	}
}

func TestWorkbenchChatFilesGitRepo(t *testing.T) {
	stubChatLocate(t, claudechat.Conversation{}, false)
	s, cookie, root := newChatFilesServer(t)
	repo := filepath.Join(root, "repo")
	gitRepo(t, repo)
	gitWriteFile(t, filepath.Join(repo, "src", "main.go"), []byte("package main\n"))
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-m", "add main")
	setChatFilesSession(s, "abc", repo)

	rr, body := chatFilesGet(t, s, cookie, "abc", "main")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if body.Root != repo {
		t.Fatalf("root = %q, want %q", body.Root, repo)
	}
	if !reflect.DeepEqual(body.Files, []string{"src/main.go"}) {
		t.Fatalf("files = %v, want the tracked match", body.Files)
	}
}

func TestWorkbenchChatFilesGitRepoEmptyQuery(t *testing.T) {
	stubChatLocate(t, claudechat.Conversation{}, false)
	s, cookie, root := newChatFilesServer(t)
	repo := filepath.Join(root, "repo")
	gitRepo(t, repo)
	gitWriteFile(t, filepath.Join(repo, "a.txt"), []byte("a\n"))
	gitWriteFile(t, filepath.Join(repo, "src", "main.go"), []byte("package main\n"))
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-m", "add files")
	setChatFilesSession(s, "abc", repo)

	// The runner's search matches nothing without a query, so an empty query
	// inside a repo lists the worktree walk, like the non-git folder.
	rr, body := chatFilesGet(t, s, cookie, "abc", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if body.Root != repo {
		t.Fatalf("root = %q, want %q", body.Root, repo)
	}
	want := []string{"a.txt", "src/", "src/main.go"}
	if !reflect.DeepEqual(body.Files, want) {
		t.Fatalf("files = %v, want %v", body.Files, want)
	}
}

func TestWorkbenchChatFilesForbidden(t *testing.T) {
	stubChatLocate(t, claudechat.Conversation{}, false)
	s, cookie, _ := newChatFilesServer(t)
	forbidden := t.TempDir()
	setChatFilesSession(s, "abc", forbidden)

	rr, _ := chatFilesGet(t, s, cookie, "abc", "")
	assertAuthError(t, rr, http.StatusForbidden, "forbidden_path")
}
