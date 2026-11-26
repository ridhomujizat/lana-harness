package commandcode

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

type poolRecorder struct {
	mu   sync.Mutex
	recs []router.CallRecord
}

func (r *poolRecorder) Record(rec router.CallRecord) {
	r.mu.Lock()
	r.recs = append(r.recs, rec)
	r.mu.Unlock()
}

func (r *poolRecorder) all() []router.CallRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]router.CallRecord(nil), r.recs...)
}

// AC-ACP-001 (spec 80): [providers.cc] and [providers.cc-2] of kind
// commandcode share a base URL; the server answers 402 for cc's key. A call
// on cc/m succeeds from cc-2 and the record names cc-2.
func TestConnectionPoolPaymentRequiredMovesToNextConnection(t *testing.T) {
	var mu sync.Mutex
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		mu.Lock()
		auths = append(auths, auth)
		mu.Unlock()
		if auth == "Bearer sk-one" {
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = io.WriteString(w, `{"error":"payment required"}`)
			return
		}
		_, _ = io.WriteString(w, ndjson(`{"type":"text-delta","text":"ok"}`, `{"type":"finish","finishReason":"stop"}`))
	}))
	defer srv.Close()
	rec := &poolRecorder{}
	rt, err := router.New(router.Config{Providers: map[string]core.ProviderConfig{
		"cc":   {Kind: Kind, BaseURL: srv.URL, APIKeyEnv: "CC_KEY"},
		"cc-2": {Kind: Kind, BaseURL: srv.URL + "/", APIKeyEnv: "CC2_KEY"},
	}}, router.WithSecrets(secrets{"CC_KEY": "sk-one", "CC2_KEY": "sk-two"}), router.WithRecorder(rec))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()
	stream, err := rt.Stream(context.Background(), "cc/m", core.ChatRequest{Messages: []core.Message{userText("hi")}}, core.LLMCallMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collect(t, stream); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := strings.Join(auths, ",")
	mu.Unlock()
	if got != "Bearer sk-one,Bearer sk-two" {
		t.Fatalf("auths = %s", got)
	}
	recs := rec.all()
	if len(recs) != 2 || recs[0].Provider != "cc" || recs[0].Outcome != core.ErrorClassQuota ||
		recs[1].Provider != "cc-2" || recs[1].Outcome != "ok" {
		t.Fatalf("records = %+v", recs)
	}
}
