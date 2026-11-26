package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/router"
)

type keyRecorder struct {
	mu   sync.Mutex
	recs []router.CallRecord
}

func (r *keyRecorder) Record(rec router.CallRecord) {
	r.mu.Lock()
	r.recs = append(r.recs, rec)
	r.mu.Unlock()
}

func (r *keyRecorder) all() []router.CallRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]router.CallRecord(nil), r.recs...)
}

// AC-PAF-005: [providers.ds] api_key_env = "K1", api_key_envs = ["K2"] and a
// server that answers 402 for K1: a call on ds/deepseek-flash succeeds with
// K2 and the record names key K2, never a key value.
func TestKeyPoolPaymentRequiredMovesToNextKey(t *testing.T) {
	var mu sync.Mutex
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		mu.Lock()
		auths = append(auths, auth)
		mu.Unlock()
		if auth == "Bearer sk-one" {
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = io.WriteString(w, `{"error":{"message":"Insufficient Balance"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, doneSSE)
	}))
	defer srv.Close()
	rec := &keyRecorder{}
	r := newRouter(t, map[string]core.ProviderConfig{
		"ds": {Kind: "deepseek", BaseURL: srv.URL + "/v1", APIKeyEnv: "K1", APIKeyEnvs: []string{"K2"}},
	}, router.WithSecrets(mapSecrets{"K1": "sk-one", "K2": "sk-two"}), router.WithRecorder(rec))
	chain, err := r.Resolve("ds/deepseek-flash")
	if err != nil {
		t.Fatal(err)
	}
	stream, target, err := r.StreamChain(context.Background(), chain, core.ChatRequest{}, core.LLMCallMeta{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	drain(t, stream)
	if target.Provider != "ds" {
		t.Fatalf("target = %v", target)
	}
	mu.Lock()
	got := strings.Join(auths, ",")
	mu.Unlock()
	if got != "Bearer sk-one,Bearer sk-two" {
		t.Fatalf("auths = %s", got)
	}
	recs := rec.all()
	if len(recs) != 2 {
		t.Fatalf("records = %+v", recs)
	}
	if recs[0].Key != "K1" || recs[0].Outcome != core.ErrorClassQuota || recs[1].Key != "K2" || recs[1].Outcome != "ok" {
		t.Fatalf("records = %+v", recs)
	}
	for _, rc := range recs {
		if strings.Contains(rc.Error, "sk-one") || strings.Contains(rc.Error, "sk-two") {
			t.Fatalf("record leaks a key: %q", rc.Error)
		}
	}
}
