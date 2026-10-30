package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

type apiCall struct {
	Method    string
	Params    map[string]any
	MessageID int64
}

type cannedResponse struct {
	status int
	body   string
}

// fakeAPI is an in-memory Bot API server.
type fakeAPI struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	calls   []apiCall
	updates []Update
	nextID  int64
	msgID   int64
	fail    map[string][]cannedResponse
	offsets []int64
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{t: t, nextID: 1, msgID: 100, fail: map[string][]cannedResponse{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) handle(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	method := parts[len(parts)-1]
	var params map[string]any
	_ = json.NewDecoder(r.Body).Decode(&params)
	f.mu.Lock()
	f.calls = append(f.calls, apiCall{Method: method, Params: params})
	index := len(f.calls) - 1
	if queue := f.fail[method]; len(queue) > 0 {
		f.fail[method] = queue[1:]
		f.mu.Unlock()
		w.WriteHeader(queue[0].status)
		_, _ = w.Write([]byte(queue[0].body))
		return
	}
	f.mu.Unlock()
	var result any = true
	switch method {
	case "getMe":
		result = User{ID: 999, IsBot: true, FirstName: "Lana", Username: "lana_bot"}
	case "getUpdates":
		offset := int64(params["offset"].(float64))
		deadline := time.Now().Add(50 * time.Millisecond)
		for {
			f.mu.Lock()
			f.offsets = append(f.offsets, offset)
			var out []Update
			for _, u := range f.updates {
				if u.UpdateID >= offset {
					out = append(out, u)
				}
			}
			f.mu.Unlock()
			if len(out) > 0 || time.Now().After(deadline) {
				result = out
				if out == nil {
					result = []Update{}
				}
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
	case "sendMessage":
		f.mu.Lock()
		f.msgID++
		id := f.msgID
		f.calls[index].MessageID = id
		f.mu.Unlock()
		result = Message{MessageID: id}
	case "deleteMessage":
		result = true
	}
	data, _ := json.Marshal(map[string]any{"ok": true, "result": result})
	_, _ = w.Write(data)
}

func (f *fakeAPI) failNext(method string, status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail[method] = append(f.fail[method], cannedResponse{status, body})
}

func (f *fakeAPI) push(updates ...Update) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range updates {
		u.UpdateID = f.nextID
		f.nextID++
		f.updates = append(f.updates, u)
	}
}

func (f *fakeAPI) text(chatID int64, chatType string, from int64, text string) {
	f.push(Update{Message: &Message{MessageID: 1000 + f.nextID, From: &User{ID: from, FirstName: "Budi"}, Chat: Chat{ID: chatID, Type: chatType}, Text: text}})
}

func (f *fakeAPI) callback(chatID int64, chatType string, from int64, data string) {
	f.push(Update{CallbackQuery: &CallbackQuery{ID: fmt.Sprintf("cb%d", f.nextID), From: User{ID: from}, Data: data, Message: &Message{MessageID: 1, Chat: Chat{ID: chatID, Type: chatType}}}})
}

func (f *fakeAPI) snapshot() []apiCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]apiCall(nil), f.calls...)
}

// waitCall waits for a call to method whose params satisfy match.
func (f *fakeAPI) waitCall(method string, match func(map[string]any) bool) apiCall {
	f.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, call := range f.snapshot() {
			if call.Method == method && (match == nil || match(call.Params)) {
				return call
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.t.Fatalf("no %s call matched; calls:\n%s", method, f.dump())
	return apiCall{}
}

func (f *fakeAPI) count(method string, match func(map[string]any) bool) int {
	n := 0
	for _, call := range f.snapshot() {
		if call.Method == method && (match == nil || match(call.Params)) {
			n++
		}
	}
	return n
}

func (f *fakeAPI) dump() string {
	var b strings.Builder
	for _, call := range f.snapshot() {
		if call.Method == "getUpdates" {
			continue
		}
		data, _ := json.Marshal(call.Params)
		fmt.Fprintf(&b, "%s %s\n", call.Method, data)
	}
	return b.String()
}

func textIs(want string) func(map[string]any) bool {
	return func(p map[string]any) bool { return p["text"] == want }
}

func textHas(want string) func(map[string]any) bool {
	return func(p map[string]any) bool {
		text, _ := p["text"].(string)
		return strings.Contains(text, want)
	}
}

// fakeBackend implements Backend in memory.
type fakeBackend struct {
	mu        sync.Mutex
	obs       *core.Observe[core.LiveFrame]
	sessions  []core.SessionInfo
	teams     []string
	nextID    int
	sent      []string
	prompts   []string
	cancels   []string
	continued []string
	turn      func(ctx context.Context, session, prompt string) (TurnResult, error)
	contTurn  func(ctx context.Context, session string) (TurnResult, error)
	contErr   error
	cancelCh  chan struct{}
	draining  bool
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{obs: core.NewObserve[core.LiveFrame](), cancelCh: make(chan struct{}, 1)}
}

func (b *fakeBackend) ActiveAgent(_ context.Context, address string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := len(b.sessions) - 1; i >= 0; i-- {
		if b.sessions[i].Address == address {
			return b.sessions[i].Agent, nil
		}
	}
	return "", nil
}

func (b *fakeBackend) ChatSession(ctx context.Context, address, agent string) (core.SessionInfo, error) {
	b.mu.Lock()
	for i := len(b.sessions) - 1; i >= 0; i-- {
		if b.sessions[i].Address == address && b.sessions[i].Agent == agent {
			info := b.sessions[i]
			b.mu.Unlock()
			return info, nil
		}
	}
	b.mu.Unlock()
	return b.NewSession(ctx, address, agent)
}

func (b *fakeBackend) NewSession(_ context.Context, address, agent string) (core.SessionInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	info := core.SessionInfo{ID: fmt.Sprintf("S%d", b.nextID), Agent: agent, Channel: "telegram", Address: address}
	b.sessions = append(b.sessions, info)
	return info, nil
}

func (b *fakeBackend) Lease(context.Context, string) error { return nil }

func (b *fakeBackend) SessionInfo(_ context.Context, id string) (core.SessionInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, info := range b.sessions {
		if info.ID == id {
			info.TurnCount = 3
			return info, nil
		}
	}
	return core.SessionInfo{ID: id, TurnCount: 3}, nil
}

func (b *fakeBackend) RunTurn(ctx context.Context, session, prompt string) (TurnResult, error) {
	b.mu.Lock()
	b.prompts = append(b.prompts, prompt)
	turn := b.turn
	b.mu.Unlock()
	if turn == nil {
		b.obs.Publish(core.LiveFrame{SessionID: session, Kind: "text_delta", Text: "hello **there**"})
		return TurnResult{Text: "hello **there**", StopReason: "end_turn"}, nil
	}
	return turn(ctx, session, prompt)
}

func (b *fakeBackend) Send(_ context.Context, _ string, text string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = append(b.sent, text)
	return nil
}

func (b *fakeBackend) CanContinue(_ context.Context, _ string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.contErr
}

func (b *fakeBackend) ContinueTurn(ctx context.Context, session string) (TurnResult, error) {
	b.mu.Lock()
	b.continued = append(b.continued, session)
	cont := b.contTurn
	b.mu.Unlock()
	if cont == nil {
		b.obs.Publish(core.LiveFrame{SessionID: session, Kind: "text_delta", Text: "continued"})
		return TurnResult{Text: "continued", StopReason: "end_turn"}, nil
	}
	return cont(ctx, session)
}

func (b *fakeBackend) Cancel(_ context.Context, session string) error {
	b.mu.Lock()
	b.cancels = append(b.cancels, session)
	b.mu.Unlock()
	select {
	case b.cancelCh <- struct{}{}:
	default:
	}
	return nil
}

func (b *fakeBackend) SubscribeLive(context.Context, string) (*core.Subscription[core.LiveFrame], error) {
	return b.obs.Subscribe(), nil
}

func (b *fakeBackend) Compact(context.Context, string) (int64, int64, error) { return 100, 40, nil }
func (b *fakeBackend) Memory(string) ([]string, []string, error) {
	return []string{"Name: Ridho"}, nil, nil
}
func (b *fakeBackend) MCPStatus(string) []core.MCPServerStatus {
	return []core.MCPServerStatus{{Name: "github", State: "running", Tools: 26}}
}
func (b *fakeBackend) Skills(string) []agenthome.Skill {
	return []agenthome.Skill{{Name: "demo", Scope: "global", Description: "Demo skill"}}
}
func (b *fakeBackend) Policy(context.Context, string) (string, error) { return "ask", nil }
func (b *fakeBackend) AgentNames() []string                           { return []string{"coder", "helper"} }
func (b *fakeBackend) TeamNames() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.teams...)
}
func (b *fakeBackend) AgentModel(agent string) string { return "mock:" + agent }

func (b *fakeBackend) Draining() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.draining
}

func (b *fakeBackend) promptList() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.prompts...)
}

const (
	ownerChat = int64(111)
	groupChat = int64(-500)
	ownerID   = int64(111)
	memberID  = int64(222)
)

// startChannel runs a channel against fake API and backend until the test ends.
func startChannel(t *testing.T, api *fakeAPI, backend *fakeBackend) (*Channel, context.CancelFunc, chan error) {
	t.Helper()
	cfg, err := ParseConfig(map[string]any{"api_base": api.srv.URL, "owner_ids": []any{ownerID}},
		[]core.RouteConfig{{Channel: "telegram", Agent: "helper", Match: map[string]any{"chat_id": ownerChat}},
			{Channel: "telegram", Agent: "helper", Match: map[string]any{"chat_id": groupChat}}})
	if err != nil {
		t.Fatal(err)
	}
	ch := New(cfg, "TOKEN123", backend)
	ch.EditInterval = 20 * time.Millisecond
	ch.TypingInterval = time.Hour
	ch.ApprovalTimeout = time.Second
	ch.PollTimeout = 0
	ch.MaxBackoff = 40 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := ch.Start(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	stopped := make(chan struct{})
	go func() {
		done <- ch.Run(ctx)
		close(stopped)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(3 * time.Second):
			t.Error("channel did not stop")
		}
	})
	return ch, cancel, done
}
