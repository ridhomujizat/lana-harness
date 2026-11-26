package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/router"
)

// AC-ACP-005 (spec 80): openai_compat connections on two ports are two
// pools: a 429 on local/m never calls local-2.
func TestConnectionPoolNeedsSameBaseURL(t *testing.T) {
	var first, second atomic.Int32
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		first.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
	}))
	defer srv1.Close()
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		second.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(doneSSE))
	}))
	defer srv2.Close()
	r := newRouter(t, map[string]core.ProviderConfig{
		"local":   {Kind: "openai_compat", BaseURL: srv1.URL + "/v1"},
		"local-2": {Kind: "openai_compat", BaseURL: srv2.URL + "/v1"},
	}, router.WithSleep(func(context.Context, time.Duration) error { return nil }))
	if pool := r.APIPool("local"); pool != nil {
		t.Fatalf("pool = %v", pool)
	}
	if _, err := r.Stream(context.Background(), "local/m", core.ChatRequest{}, core.LLMCallMeta{}); err == nil {
		t.Fatal("local/m answered")
	}
	if first.Load() == 0 || second.Load() != 0 {
		t.Fatalf("calls local %d local-2 %d", first.Load(), second.Load())
	}
}
