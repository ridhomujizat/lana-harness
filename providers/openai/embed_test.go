package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// mapSecrets is a core.Secrets over a plain map, for tests.
type mapSecrets map[string]string

func (m mapSecrets) Lookup(name string) (core.Secret, bool) {
	v, ok := m[name]
	if !ok {
		return core.Secret{}, false
	}
	return core.NewSecret(v), true
}

// API-KNW-004: Embed posts a batch to /embeddings with the api key and returns
// the vectors in input order, even when the reply lists them out of order.
func TestEmbedOrdersByIndex(t *testing.T) {
	var gotPath, auth string
	var gotBody embedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[` +
			`{"index":2,"embedding":[3,3]},` +
			`{"index":0,"embedding":[1,1]},` +
			`{"index":1,"embedding":[2,2]}]}`))
	}))
	defer srv.Close()

	adapter, err := NewAdapter("openai", Config{BaseURL: srv.URL + "/v1", APIKeyEnv: "OPENAI_API_KEY"}, mapSecrets{"OPENAI_API_KEY": "sk-test"})
	if err != nil {
		t.Fatal(err)
	}
	vecs, err := adapter.Embed(context.Background(), "text-embedding-3-small", []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/embeddings" {
		t.Fatalf("path = %q", gotPath)
	}
	if auth != "Bearer sk-test" {
		t.Fatalf("auth = %q", auth)
	}
	if gotBody.Model != "text-embedding-3-small" || len(gotBody.Input) != 3 || gotBody.Input[0] != "a" || gotBody.Input[2] != "c" {
		t.Fatalf("batch body = %+v", gotBody)
	}
	if len(vecs) != 3 {
		t.Fatalf("vectors = %v", vecs)
	}
	for i, want := range [][2]float32{{1, 1}, {2, 2}, {3, 3}} {
		if len(vecs[i]) != 2 || vecs[i][0] != want[0] || vecs[i][1] != want[1] {
			t.Fatalf("vectors[%d] = %v, want %v", i, vecs[i], want)
		}
	}
}

// API-KNW-004: a non-2xx embeddings reply is classified like a stream error.
func TestEmbedHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	adapter, _ := NewAdapter("p", Config{BaseURL: srv.URL}, nil)
	_, err := adapter.Embed(context.Background(), "m", []string{"x"})
	var pe *core.ProviderError
	if !errors.As(err, &pe) || pe.Class != "auth" {
		t.Fatalf("err = %v", err)
	}
}
