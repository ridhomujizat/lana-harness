package lana

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/routerdb"
	"github.com/ridhomujizat/lana-harness/store"
)

// statusServer answers every request with code and counts the requests.
type statusServer struct {
	server   *httptest.Server
	requests atomic.Int32
}

func newStatusServer(t *testing.T, code int, body string) *statusServer {
	t.Helper()
	s := &statusServer{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.server.Close)
	return s
}

// routedRuntime boots a runtime over a SQLite store from cfg, so router call
// records are kept. Close the runtime before reading the records: Close
// flushes the async recorder.
func routedRuntime(t *testing.T, cfg string) (*Runtime, *store.Store) {
	t.Helper()
	st := openRouterTestStore(t)
	rt, err := New(context.Background(), Options{Store: st, Layers: []config.Layer{{Name: "test", TOML: []byte(cfg)}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt, st
}

func routerCalls(t *testing.T, st *store.Store, source string) []routerdb.Call {
	t.Helper()
	page, err := st.ListRouterCalls(context.Background(), routerdb.CallFilter{Source: source, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	// Newest first in the store; oldest first here.
	calls := page.Calls
	for i, j := 0, len(calls)-1; i < j; i, j = i+1, j-1 {
		calls[i], calls[j] = calls[j], calls[i]
	}
	return calls
}

func sessionPayloads[T any](t *testing.T, rt *Runtime, sessionID, kind string) []T {
	t.Helper()
	events, err := rt.History(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var out []T
	for _, event := range events {
		if event.Kind != kind {
			continue
		}
		var payload T
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		out = append(out, payload)
	}
	return out
}

func twoProviderConfig(a, b, agent string) string {
	return fmt.Sprintf(`
[providers.a]
kind = "openai_compat"
base_url = %q

[providers.b]
kind = "openai_compat"
base_url = %q

[combos.duo]
models = ["a/x", "b/y"]

[router]
cooldown = "0s"

[agents.assistant]
%s

[[routes]]
channel = "library"
agent = "assistant"
`, a, b, agent)
}

// AC-RTR-007: a 401 on the first target falls back to the second; the turn
// records one assistant/attempt for a/x (auth), step/end names b/y, and the
// router keeps a call record with source turn for each attempt.
func TestRouterTurnFallsBackOnAuth(t *testing.T) {
	a := newStatusServer(t, http.StatusUnauthorized, `{"error":{"message":"bad key"}}`)
	b := newSSEServer(t, sseResponse{body: textSSE("from b")})
	rt, st := routedRuntime(t, twoProviderConfig(a.server.URL, b.server.URL, `model = "duo"`))

	result, err := rt.Run(context.Background(), Request{Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	// The result names the target that answered, not the combo's first.
	if result.Text != "from b" || result.Model != "b/y" {
		t.Fatalf("result=%+v", result)
	}
	if a.requests.Load() != 1 || b.requests.Load() != 1 {
		t.Fatalf("requests a=%d b=%d", a.requests.Load(), b.requests.Load())
	}
	attempts := sessionPayloads[core.AssistantAttempt](t, rt, result.SessionID, core.KindAssistantAttempt)
	if len(attempts) != 1 || attempts[0].Provider != "a" || attempts[0].Model != "x" || attempts[0].Error.Class != "auth" {
		t.Fatalf("attempts=%+v", attempts)
	}
	starts := sessionPayloads[core.StepStart](t, rt, result.SessionID, core.KindStepStart)
	ends := sessionPayloads[core.StepEnd](t, rt, result.SessionID, core.KindStepEnd)
	if len(starts) != 1 || starts[0].Provider != "a" || starts[0].Model != "x" {
		t.Fatalf("step/start=%+v", starts)
	}
	if len(ends) != 1 || ends[0].Provider != "b" || ends[0].Model != "y" {
		t.Fatalf("step/end=%+v", ends)
	}

	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	calls := routerCalls(t, st, "turn")
	if len(calls) != 2 {
		t.Fatalf("calls=%+v", calls)
	}
	first, second := calls[0], calls[1]
	if first.Provider != "a" || first.Model != "x" || first.Outcome != "auth" || first.Requested != "duo" || first.Agent != "assistant" || first.SessionID != result.SessionID {
		t.Fatalf("first call=%+v", first)
	}
	if second.Provider != "b" || second.Model != "y" || second.Outcome != "ok" || second.Requested != "duo" || second.TurnID == "" {
		t.Fatalf("second call=%+v", second)
	}
}

// AC-RTR-008: a 400 client error on the first target fails the turn and the
// second target is never called.
func TestRouterTurnClientErrorStops(t *testing.T) {
	a := newStatusServer(t, http.StatusBadRequest, `{"error":{"message":"bad request"}}`)
	b := newSSEServer(t, sseResponse{body: textSSE("from b")})
	rt, _ := routedRuntime(t, twoProviderConfig(a.server.URL, b.server.URL, "model = \"a/x\"\nfallback = [\"b/y\"]"))

	result, err := rt.Run(context.Background(), Request{Prompt: "hi"})
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Class != "client" {
		t.Fatalf("err=%v", err)
	}
	// No target answered: the result names the address the turn asked for.
	if result.StopReason != StopError || result.Model != "a/x" {
		t.Fatalf("result=%+v", result)
	}
	if a.requests.Load() != 1 || b.requests.Load() != 0 {
		t.Fatalf("requests a=%d b=%d", a.requests.Load(), b.requests.Load())
	}
}

// AC-RTR-010: compaction of a combo agent whose first target is down is
// answered by the second, with call records of source compaction.
func TestRouterCompactionFallsBack(t *testing.T) {
	a := newStatusServer(t, http.StatusUnauthorized, `{"error":{"message":"bad key"}}`)
	b := newSSEServer(t,
		sseResponse{body: textSSE("first answer")},
		sseResponse{body: textSSE("second answer")},
		sseResponse{body: textSSE("checkpoint summary")},
	)
	rt, st := routedRuntime(t, twoProviderConfig(a.server.URL, b.server.URL, `model = "duo"`))

	first, err := rt.Run(context.Background(), Request{Prompt: "first question"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Run(context.Background(), Request{Prompt: "second question", SessionID: first.SessionID}); err != nil {
		t.Fatal(err)
	}
	res, err := rt.Compact(context.Background(), first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if res.UptoSeq == 0 {
		t.Fatalf("nothing compacted: %+v", res)
	}
	summaries := sessionPayloads[core.CompactionSummary](t, rt, first.SessionID, core.KindCompactionSummary)
	if len(summaries) != 1 || summaries[0].Summary != "checkpoint summary" || summaries[0].Model != "y" {
		t.Fatalf("summaries=%+v", summaries)
	}

	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	calls := routerCalls(t, st, "compaction")
	if len(calls) != 2 {
		t.Fatalf("compaction calls=%+v", calls)
	}
	if calls[0].Provider != "a" || calls[0].Outcome != "auth" || calls[1].Provider != "b" || calls[1].Outcome != "ok" {
		t.Fatalf("compaction calls=%+v", calls)
	}
	if calls[1].Requested != "duo" || calls[1].Agent != "assistant" || calls[1].SessionID != first.SessionID {
		t.Fatalf("compaction record=%+v", calls[1])
	}
}

// AC-RTR-011 (runtime side): ApplyConfig adds a provider and switches the
// agent to it; the next turn uses it without a restart. The new provider is
// kind openai, so its key comes from the default env OA_API_KEY.
func TestRouterApplyConfigLive(t *testing.T) {
	a := newSSEServer(t, sseResponse{body: textSSE("from a")})
	var auth atomic.Value
	var oaRequests atomic.Int32
	oa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oaRequests.Add(1)
		auth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(textSSE("from oa")))
	}))
	t.Cleanup(oa.Close)
	oaURL := oa.URL
	t.Setenv("OA_API_KEY", "oa-test-key")

	base := fmt.Sprintf("[providers.a]\nkind = \"openai_compat\"\nbase_url = %q\n\n[agents.assistant]\nmodel = \"a/x\"\n\n[[routes]]\nchannel = \"library\"\nagent = \"assistant\"\n", a.server.URL)
	rt, _ := routedRuntime(t, base)
	result, err := rt.Run(context.Background(), Request{Prompt: "hi"})
	if err != nil || result.Text != "from a" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if got := rt.AgentModel("assistant"); got != "a/x" {
		t.Fatalf("AgentModel=%q", got)
	}

	next := fmt.Sprintf("[providers.a]\nkind = \"openai_compat\"\nbase_url = %q\n\n[providers.oa]\nkind = \"openai\"\nbase_url = %q\n\n[agents.assistant]\nmodel = \"oa/gpt-5-mini\"\n\n[[routes]]\nchannel = \"library\"\nagent = \"assistant\"\n", a.server.URL, oaURL)
	loaded, err := config.Load(context.Background(), config.Options{Layers: []config.Layer{{Name: "next", TOML: []byte(next)}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.ApplyConfig(loaded.Config); err != nil {
		t.Fatal(err)
	}
	result, err = rt.Run(context.Background(), Request{Prompt: "again", SessionID: result.SessionID})
	if err != nil || result.Text != "from oa" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if got, _ := auth.Load().(string); got != "Bearer oa-test-key" {
		t.Fatalf("Authorization=%q", got)
	}
	if a.requests.Load() != 1 || oaRequests.Load() != 1 {
		t.Fatalf("requests a=%d oa=%d", a.requests.Load(), oaRequests.Load())
	}
	ends := sessionPayloads[core.StepEnd](t, rt, result.SessionID, core.KindStepEnd)
	if last := ends[len(ends)-1]; last.Provider != "oa" || last.Model != "gpt-5-mini" {
		t.Fatalf("last step/end=%+v", last)
	}

	// A router config the router rejects leaves the old one in place.
	bad := loaded.Config
	bad.Combos = map[string]core.ComboConfig{"broken": {Models: []string{"nope/x"}}}
	if err := rt.ApplyConfig(bad); err == nil {
		t.Fatal("ApplyConfig accepted a combo with an unknown provider")
	}
	if _, ok := rt.Router().Provider("oa"); !ok {
		t.Fatal("rejected apply changed the router")
	}
}
