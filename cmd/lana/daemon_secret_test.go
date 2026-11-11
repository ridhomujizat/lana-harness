package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

// fakeSecretBroker records the requests the router hands to the web.
type fakeSecretBroker struct {
	mu    sync.Mutex
	calls []core.SecretRequest
}

func (b *fakeSecretBroker) Ask(_ context.Context, req core.SecretRequest) (core.SecretOutcome, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, req)
	return core.SecretOutcome{Status: core.SecretStatusSaved}, nil
}

// Spec 53 REQ-SEC-009: web and workflow sessions go to the web broker; every
// other channel, or a failed lookup, is unavailable and never reaches it.
func TestSecretRouterRoutesByChannel(t *testing.T) {
	broker := &fakeSecretBroker{}
	channels := map[string]string{"w": "web", "f": "workflow", "t": "telegram", "c": "cli"}
	router := &SecretRouter{
		sessionInfo: func(_ context.Context, id string) (core.SessionInfo, error) {
			channel, ok := channels[id]
			if !ok {
				return core.SessionInfo{}, errors.New("not found")
			}
			return core.SessionInfo{ID: id, Channel: channel}, nil
		},
		web: broker,
	}
	for id, want := range map[string]string{"w": core.SecretStatusSaved, "f": core.SecretStatusSaved, "t": core.SecretStatusUnavailable, "c": core.SecretStatusUnavailable, "x": core.SecretStatusUnavailable} {
		if got := router.Request(context.Background(), core.SecretRequest{SessionID: id, Name: "K"}); got.Status != want {
			t.Fatalf("session %s: outcome %+v, want %s", id, got, want)
		}
	}
	if len(broker.calls) != 2 {
		t.Fatalf("broker calls = %d, want 2 (web and workflow)", len(broker.calls))
	}
	if got := (&SecretRouter{}).Request(context.Background(), core.SecretRequest{SessionID: "w"}); got.Status != core.SecretStatusUnavailable {
		t.Fatalf("router without broker = %+v", got)
	}
}

const secretTestLayer = `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.assistant]
provider = "mock"
model = "m"
permission = "auto"
toolset = "custom"
tools = ["secret_request"]

[[routes]]
channel = "web"
agent = "assistant"
`

// Spec 53 AC-SEC-008 (the leak test): an agent calls secret_request in a web
// chat, the owner saves a value through the HTTP route, and the value is in
// secrets.env but nowhere else: not in the session events, the tool result,
// any model request, the SSE stream, the logs, or the audit file.
func TestSecretRequestValueNeverLeaks(t *testing.T) {
	const secretValue = "sk-live-NEVER-LEAK-4c1d2e"
	logs := &lockedBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	use, end, done := "tool_use", "end_turn", "Saved."
	capture := &promptCaptureProvider{llm: mock.New(mock.Script{Responses: []mock.Response{
		{Events: []mock.Event{{ToolUse: &mock.ToolUse{ID: "call-1", Name: "secret_request", Input: json.RawMessage(`{"name":"STRIPE_KEY","description":"The workflow charges cards; copy the secret key from the Stripe dashboard.","link":"https://dashboard.stripe.com/apikeys"}`)}}, {Stop: &use}}},
		{Events: []mock.Event{{Text: &done}, {Stop: &end}}},
	}})}
	router := &SecretRouter{}
	home := t.TempDir()
	rt, err := lana.New(context.Background(), lana.Options{
		Home:      home,
		Layers:    []config.Layer{{Name: "test", TOML: []byte(secretTestLayer)}},
		Providers: map[string]core.Provider{"mock": capture},
	}, lana.SecretRequestWith(router.Request, "web", "workflow"))
	if err != nil {
		t.Fatalf("lana.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	router.sessionInfo = rt.SessionInfo
	srv, ts, cookie, csrf := webServer(t, rt, home)
	router.web = srv.SecretRequests()
	info := webCreateSession(t, rt, context.Background())

	stream := openWebStream(t, ts, cookie, "/v1/sessions/"+info.ID+"/stream")
	defer func() { _ = stream.Body.Close() }()
	reader := bufio.NewReader(stream.Body)
	var streamed strings.Builder

	runErr := make(chan error, 1)
	go func() {
		_, err := rt.Run(context.Background(), lana.Request{Prompt: "set up billing", SessionID: info.ID})
		runErr <- err
	}()
	block := waitWebSSE(t, reader, "secret_request")
	streamed.WriteString(block.Data)
	var event struct {
		RequestID string `json:"request_id"`
		Name      string `json:"name"`
		Replaces  bool   `json:"replaces"`
	}
	if err := json.Unmarshal([]byte(block.Data), &event); err != nil {
		t.Fatal(err)
	}
	if event.Name != "STRIPE_KEY" || event.Replaces || event.RequestID == "" {
		t.Fatalf("secret_request event = %s", block.Data)
	}

	resp, body := webDo(t, http.MethodPost, ts.URL+"/v1/secret-requests/"+event.RequestID, `{"value":"`+secretValue+`"}`, cookie, csrf)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save status = %d, body = %s", resp.StatusCode, body)
	}
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the turn did not finish")
	}
	// Read the stream until the resolved event arrives, keeping every frame.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		next := nextWebSSE(t, reader)
		streamed.WriteString(next.Data)
		if strings.Contains(next.Data, core.KindSecretResolved) {
			break
		}
	}

	values, err := webfiles.ReadSecrets(filepath.Join(home, "secrets.env"))
	if err != nil {
		t.Fatal(err)
	}
	if values["STRIPE_KEY"] != secretValue {
		t.Fatalf("secrets.env STRIPE_KEY = %q", values["STRIPE_KEY"])
	}

	events := webHistory(t, rt, info.ID)
	var requested, resolved bool
	var toolResult string
	for _, ev := range events {
		switch ev.Kind {
		case core.KindSecretRequested:
			requested = true
		case core.KindSecretResolved:
			var p core.SecretResolved
			_ = json.Unmarshal(ev.Payload, &p)
			resolved = p.Outcome == core.SecretStatusSaved && p.Name == "STRIPE_KEY"
		case core.KindToolResult:
			toolResult = string(ev.Payload)
		}
	}
	if !requested || !resolved {
		t.Fatalf("requested=%v resolved=%v", requested, resolved)
	}
	if !strings.Contains(toolResult, `{\"status\":\"saved\",\"name\":\"STRIPE_KEY\"}`) {
		t.Fatalf("tool result = %s", toolResult)
	}
	allEvents, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	capture.mu.Lock()
	prompts, err := json.Marshal(capture.requests)
	capture.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prompts), "STRIPE_KEY") {
		t.Fatal("the model never saw the tool result")
	}
	if !strings.Contains(logs.String(), "secret request closed") {
		t.Fatalf("the log capture missed the service lines:\n%s", logs.String())
	}
	audit, _ := os.ReadFile(webfiles.AuditPath(home))
	if !strings.Contains(string(audit), "STRIPE_KEY") {
		t.Fatalf("the audit file lacks the name: %s", audit)
	}
	for label, text := range map[string]string{
		"session events": string(allEvents),
		"model requests": string(prompts),
		"SSE stream":     streamed.String(),
		"logs":           logs.String(),
		"audit":          string(audit),
		"save response":  string(body),
	} {
		if strings.Contains(text, secretValue) {
			t.Fatalf("%s contain the secret value", label)
		}
	}
}

// Spec 53 REQ-SEC-009: a Telegram session gets a tool error that sends the
// owner to the web, and no card opens.
func TestSecretRequestTelegramToolError(t *testing.T) {
	use, end, done := "tool_use", "end_turn", "ok"
	rt, err := lana.New(context.Background(), lana.Options{
		Home:   t.TempDir(),
		Layers: []config.Layer{{Name: "test", TOML: []byte(secretTestLayer)}},
		Providers: map[string]core.Provider{"mock": mock.New(mock.Script{Responses: []mock.Response{
			{Events: []mock.Event{{ToolUse: &mock.ToolUse{ID: "call-1", Name: "secret_request", Input: json.RawMessage(`{"name":"API_KEY","description":"why"}`)}}, {Stop: &use}}},
			{Events: []mock.Event{{Text: &done}, {Stop: &end}}},
		}})},
	}, lana.SecretRequestWith(func(context.Context, core.SecretRequest) core.SecretOutcome {
		t.Error("the responder was called for a Telegram session")
		return core.SecretOutcome{Status: core.SecretStatusSaved}
	}, "web", "workflow"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	info, err := rt.CreateChannelSession(context.Background(), "telegram", "owner", "assistant")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Run(context.Background(), lana.Request{Prompt: "add my key", SessionID: info.ID}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var result core.ToolResultPayload
	for _, ev := range webHistory(t, rt, info.ID) {
		if ev.Kind == core.KindSecretRequested {
			t.Fatal("a Telegram session wrote secret/requested")
		}
		if ev.Kind == core.KindToolResult {
			_ = json.Unmarshal(ev.Payload, &result)
		}
	}
	if result.Status != core.ToolStatusError || !strings.Contains(result.Output, "Telegram") {
		t.Fatalf("tool result = %+v", result)
	}
}
