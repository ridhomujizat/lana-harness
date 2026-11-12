package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/web"
)

// TestProviderTestAdapterFakeServer exercises the real provider adapter (the
// one the runtime uses) through a fake provider server, for the 200 and 401
// cases of AC-WEB-036.
func TestProviderTestAdapterFakeServer(t *testing.T) {
	if providerTestTimeout != 15*time.Second {
		t.Fatalf("provider test timeout = %s, want 15s", providerTestTimeout)
	}
	const key = "sk-test-inline-key"
	var requests atomic.Int64
	var auth string
	var payload struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		Stream    bool   `json:"stream"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if skipModelList(w, r) {
			return
		}
		requests.Add(1)
		auth = r.Header.Get("Authorization")
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	backend := &webBackend{home: t.TempDir()}
	result, err := backend.TestProvider(context.Background(), web.ProviderTest{
		Kind:    "openai_compat",
		BaseURL: server.URL + "/v1",
		Model:   "gpt-5-mini",
		APIKey:  core.NewSecret(key),
	})
	if err != nil {
		t.Fatalf("TestProvider: %v", err)
	}
	if !result.OK {
		t.Fatalf("result = %+v, want ok", result)
	}
	if requestCount := requests.Load(); requestCount != 1 {
		t.Fatalf("provider requests = %d, want exactly 1", requestCount)
	}
	if payload.Model != "gpt-5-mini" {
		t.Fatalf("model = %q", payload.Model)
	}
	if payload.MaxTokens != 1 {
		t.Fatalf("max_tokens = %d, want 1", payload.MaxTokens)
	}
	if !payload.Stream {
		t.Fatalf("stream = false, want true")
	}
	if auth != "Bearer "+key {
		t.Fatalf("Authorization = %q, want the inline key", auth)
	}
	if _, err := os.Stat(filepath.Join(backend.home, "secrets.env")); !os.IsNotExist(err) {
		t.Fatalf("provider test wrote secrets.env: %v", err)
	}

	// The same adapter reports a refused key as a result, never as a web
	// error, with the provider status.
	requests.Store(0)
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if skipModelList(w, r) {
			return
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid key"}}`))
	}))
	defer refusing.Close()
	result, err = backend.TestProvider(context.Background(), web.ProviderTest{
		Kind:    "openai_compat",
		BaseURL: refusing.URL + "/v1",
		Model:   "gpt-5-mini",
		APIKey:  core.NewSecret(key),
	})
	if err != nil {
		t.Fatalf("TestProvider 401: %v", err)
	}
	if result.OK || result.Status != http.StatusUnauthorized {
		t.Fatalf("401 result = %+v", result)
	}
	host := mustURLHost(t, refusing.URL)
	if result.Message != "401 Unauthorized from "+host {
		t.Fatalf("401 message = %q, want 401 Unauthorized from %s", result.Message, host)
	}
	if requests.Load() != 1 {
		t.Fatalf("refused provider requests = %d, want exactly 1", requests.Load())
	}
}

// TestProviderTestStatusMessage pins the AC-WEB-036 wording without talking to
// api.openai.com.
func TestProviderTestStatusMessage(t *testing.T) {
	err := &core.ProviderError{Class: "auth", Status: http.StatusUnauthorized, Message: "HTTP 401: bad key"}
	result := providerTestFailure(err, "https://api.openai.com/v1", "sk-secret")
	if result.Status != http.StatusUnauthorized {
		t.Fatalf("status = %d", result.Status)
	}
	if result.Message != "401 Unauthorized from api.openai.com" {
		t.Fatalf("message = %q", result.Message)
	}
	redacted := providerTestFailure(errors.New("provider echoed sk-secret"), "https://api.openai.com/v1", "sk-secret")
	if strings.Contains(redacted.Message, "sk-secret") {
		t.Fatalf("failure leaked the key: %q", redacted.Message)
	}
}

// TestProviderTestEnvKey proves api_key_env can resolve from secrets.env.
func TestProviderTestEnvKey(t *testing.T) {
	const envName = "LANA_TEST_PROVIDER_FILE_KEY"
	const key = "sk-from-secrets-file"
	t.Setenv(envName, "")
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "secrets.env"), []byte(envName+"="+key+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if skipModelList(w, r) {
			return
		}
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	backend := &webBackend{home: home}
	result, err := backend.TestProvider(context.Background(), web.ProviderTest{
		Kind:      "openai_compat",
		BaseURL:   server.URL + "/v1",
		Model:     "gpt-5-mini",
		APIKeyEnv: envName,
	})
	if err != nil {
		t.Fatalf("TestProvider: %v", err)
	}
	if !result.OK {
		t.Fatalf("result = %+v", result)
	}
	if auth != "Bearer "+key {
		t.Fatalf("Authorization = %q, want the secrets.env key", auth)
	}
}

func mustURLHost(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Hostname()
}

func TestProviderTestEnvUnset(t *testing.T) {
	const envName = "LANA_TEST_PROVIDER_MISSING_KEY"
	t.Setenv(envName, "")
	backend := &webBackend{home: t.TempDir()}
	result, err := backend.TestProvider(context.Background(), web.ProviderTest{
		Kind:      "openai_compat",
		BaseURL:   "https://api.openai.com/v1",
		Model:     "gpt-5-mini",
		APIKeyEnv: envName,
	})
	if err != nil {
		t.Fatalf("TestProvider: %v", err)
	}
	if result.OK || result.Status != 0 {
		t.Fatalf("result = %+v, want ok false with status 0", result)
	}
	if !strings.Contains(result.Message, envName) {
		t.Fatalf("message does not name the variable: %q", result.Message)
	}
}
