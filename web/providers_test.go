package web

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/models"
)

// testProviderTesterBackend adds the provider port to the in-memory backend.
type testProviderTesterBackend struct {
	*fakeWebBackend
	test func(ctx context.Context, test ProviderTest) (TestResult, error)
}

func (b *testProviderTesterBackend) TestProvider(ctx context.Context, test ProviderTest) (TestResult, error) {
	return b.test(ctx, test)
}

func newProviderTestServer(t *testing.T, store *testConfigStore, test func(context.Context, ProviderTest) (TestResult, error)) (*Server, *http.Cookie, string, *bytes.Buffer) {
	t.Helper()
	s, cookie, csrf := newConfigTestServer(t, store)
	if test != nil {
		s.SetBackend(&testProviderTesterBackend{fakeWebBackend: newFakeWebBackend(), test: test})
	}
	var logs bytes.Buffer
	s.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return s, cookie, csrf, &logs
}

func serveProvider(t *testing.T, s *Server, method, target, body string, cookie *http.Cookie, csrf string) *httptestResponse {
	t.Helper()
	req := authRequest(method, target, body)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set(csrfHeaderName, csrf)
	}
	rr := serveAuth(s, req)
	return &httptestResponse{code: rr.Code, body: rr.Body.Bytes()}
}

func TestProviderTestFakeProvider(t *testing.T) {
	raw := providerTestConfig
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	var seen []ProviderTest
	s, cookie, csrf, logs := newProviderTestServer(t, store, func(_ context.Context, test ProviderTest) (TestResult, error) {
		seen = append(seen, test)
		if test.APIKey.Reveal() == "sk-bad" {
			return TestResult{OK: false, Status: 401, Message: "401 Unauthorized from api.openai.com"}, nil
		}
		return TestResult{OK: true, LatencyMS: 12}, nil
	})

	badBody := `{"kind":"openai_compat","base_url":"https://api.openai.com/v1","model":"gpt-5-mini","api_key":"sk-bad"}`
	bad := serveProvider(t, s, http.MethodPost, "/v1/providers/test", badBody, cookie, csrf)
	if bad.code != http.StatusOK {
		t.Fatalf("bad key status = %d, body = %q", bad.code, bad.body)
	}
	var badResult map[string]any
	bad.json(t, &badResult)
	if badResult["ok"] != false {
		t.Fatalf("bad key ok = %v, want false", badResult["ok"])
	}
	if badResult["status"] != float64(http.StatusUnauthorized) {
		t.Fatalf("bad key status field = %v, want 401", badResult["status"])
	}
	if badResult["message"] != "401 Unauthorized from api.openai.com" {
		t.Fatalf("bad key message = %v", badResult["message"])
	}

	goodBody := `{"kind":"openai_compat","base_url":"https://api.openai.com/v1","model":"gpt-5-mini","api_key":"sk-good"}`
	good := serveProvider(t, s, http.MethodPost, "/v1/providers/test", goodBody, cookie, csrf)
	if good.code != http.StatusOK {
		t.Fatalf("good key status = %d, body = %q", good.code, good.body)
	}
	var goodResult map[string]any
	good.json(t, &goodResult)
	if goodResult["ok"] != true {
		t.Fatalf("good key ok = %v, want true", goodResult["ok"])
	}
	if goodResult["latency_ms"] != float64(12) {
		t.Fatalf("good key latency_ms = %v, want 12", goodResult["latency_ms"])
	}
	if _, ok := goodResult["status"]; ok {
		t.Fatalf("good key response has a status field: %v", goodResult)
	}

	if len(seen) != 2 {
		t.Fatalf("tester calls = %d, want 2", len(seen))
	}
	if seen[0].APIKey.Reveal() != "sk-bad" || seen[1].APIKey.Reveal() != "sk-good" {
		t.Fatalf("tester did not receive the inline keys")
	}
	// The inline key is used for the call only. It never reaches a response,
	// a log line, or a file.
	if strings.Contains(string(bad.body)+string(good.body), "sk-bad") || strings.Contains(string(good.body), "sk-good") {
		t.Fatalf("response leaked the inline key: %q %q", bad.body, good.body)
	}
	if strings.Contains(logs.String(), "sk-bad") || strings.Contains(logs.String(), "sk-good") {
		t.Fatalf("log leaked the inline key: %q", logs.String())
	}
	if _, err := os.Stat(filepath.Join(store.home, "secrets.env")); !os.IsNotExist(err) {
		t.Fatalf("test call wrote secrets.env: %v", err)
	}
}

func TestProviderTestEnvUnset(t *testing.T) {
	store := newTestConfigStore(t, providerTestConfig, 0o600, ConfigSettings{})
	s, cookie, csrf, _ := newProviderTestServer(t, store, func(_ context.Context, test ProviderTest) (TestResult, error) {
		return TestResult{OK: false, Status: 0, Message: "env MISSING_KEY is not set"}, nil
	})
	body := `{"kind":"openai_compat","base_url":"https://api.openai.com/v1","model":"gpt-5-mini","api_key_env":"MISSING_KEY"}`
	response := serveProvider(t, s, http.MethodPost, "/v1/providers/test", body, cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	var result map[string]any
	response.json(t, &result)
	if result["ok"] != false || result["status"] != float64(0) {
		t.Fatalf("result = %v, want ok false with status 0", result)
	}
	if !strings.Contains(result["message"].(string), "MISSING_KEY") {
		t.Fatalf("message does not name the variable: %v", result["message"])
	}
}

func TestProviderTestRejectsBadBody(t *testing.T) {
	store := newTestConfigStore(t, providerTestConfig, 0o600, ConfigSettings{})
	s, cookie, csrf, _ := newProviderTestServer(t, store, func(context.Context, ProviderTest) (TestResult, error) {
		return TestResult{OK: true}, nil
	})
	cases := []string{
		`{"kind":"openai_compat","base_url":"https://api.openai.com/v1","model":"m","api_key":"a","api_key_env":"A"}`,
		`{"kind":"openai_compat","base_url":"https://api.openai.com/v1","model":"m"}`,
		`{"kind":"nope","base_url":"https://api.openai.com/v1","model":"m","api_key":"a"}`,
		`{"kind":"openai_compat","base_url":"ftp://api.openai.com","model":"m","api_key":"a"}`,
	}
	for _, body := range cases {
		response := serveProvider(t, s, http.MethodPost, "/v1/providers/test", body, cookie, csrf)
		if response.code != http.StatusBadRequest {
			t.Fatalf("body %s status = %d, want 400", body, response.code)
		}
		if envelope := decodeError(t, response); envelope.Error.Code != "invalid_request" {
			t.Fatalf("body %s error code = %q", body, envelope.Error.Code)
		}
	}
}

func TestProvidersList(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-env")
	raw := `[providers.openai]
kind = "openai_compat"
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"

[providers.anthropic]
kind = "anthropic"
base_url = "https://api.anthropic.com"
api_key_env = "ANTHROPIC_API_KEY"

[agents.coder]
provider = "openai"
model = "gpt-5-mini"
`
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	if err := os.WriteFile(filepath.Join(store.home, "secrets.env"), []byte("OPENAI_API_KEY=sk-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, cookie, csrf, _ := newProviderTestServer(t, store, nil)
	response := serveProvider(t, s, http.MethodGet, "/v1/providers", "", cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	var body listProvidersResponse
	response.json(t, &body)
	if body.Version != shaHex([]byte(raw)) {
		t.Fatalf("version = %q", body.Version)
	}
	if body.RestartRequired != (body.Version != store.ConfigLoadedVersion()) {
		t.Fatalf("restart_required = %v", body.RestartRequired)
	}
	if len(body.Providers) != 2 {
		t.Fatalf("providers = %d, want 2", len(body.Providers))
	}
	byName := map[string]providerJSON{}
	for _, row := range body.Providers {
		byName[row.Name] = row
	}
	if state := byName["openai"].KeyState; state != "set" {
		t.Fatalf("openai key_state = %q, want set", state)
	}
	if state := byName["anthropic"].KeyState; state != "overridden_by_env" {
		t.Fatalf("anthropic key_state = %q, want overridden_by_env", state)
	}
	if used := byName["openai"].UsedBy; len(used) != 1 || used[0] != "agents.coder" {
		t.Fatalf("openai used_by = %v", used)
	}
	if used := byName["anthropic"].UsedBy; len(used) != 0 {
		t.Fatalf("anthropic used_by = %v", used)
	}
	if strings.Contains(string(response.body), "sk-file") || strings.Contains(string(response.body), "sk-env") {
		t.Fatalf("provider list leaked a key value: %q", response.body)
	}
}

const providerTestConfig = `[providers.openai]
kind = "openai_compat"
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"

[agents.coder]
provider = "openai"
model = "gpt-5-mini"
`

func TestProvidersPutSectionRewrite(t *testing.T) {
	oldTable := `[providers.openai]
kind = "openai_compat"
base_url = "https://old.example.com/v1"
api_key_env = "OPENAI_API_KEY"
timeout = "30s"
`
	raw := "# top comment\n" + oldTable + "\n[agents.coder]\nprovider = \"openai\"\nmodel = \"gpt-5-mini\"\n"
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	s, cookie, csrf, _ := newProviderTestServer(t, store, nil)

	body := `{"kind":"openai_compat","base_url":"https://new.example.com/v1","expected_version":"` + shaHex([]byte(raw)) + `"}`
	response := serveProvider(t, s, http.MethodPut, "/v1/providers/openai", body, cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	newTable := `[providers.openai]
kind = 'openai_compat'
base_url = 'https://new.example.com/v1'
api_key_env = 'OPENAI_API_KEY'
timeout = '30s'
`
	want := "# top comment\n" + newTable + "\n[agents.coder]\nprovider = \"openai\"\nmodel = \"gpt-5-mini\"\n"
	if string(data) != want {
		t.Fatalf("config changed outside the provider table:\ngot:\n%s\nwant:\n%s", data, want)
	}
	var saved providerSaveResponse
	response.json(t, &saved)
	if saved.Name != "openai" || saved.Version != shaHex(data) || !saved.RestartRequired {
		t.Fatalf("save response = %+v", saved)
	}

	// A stale expected_version is refused and leaves the file unchanged.
	staleBody := `{"kind":"openai_compat","base_url":"https://other.example.com/v1","expected_version":"` + shaHex([]byte(raw)) + `"}`
	stale := serveProvider(t, s, http.MethodPut, "/v1/providers/openai", staleBody, cookie, csrf)
	if stale.code != http.StatusConflict {
		t.Fatalf("stale status = %d, body = %q", stale.code, stale.body)
	}
	if envelope := decodeError(t, stale); envelope.Error.Code != "stale_version" {
		t.Fatalf("stale error code = %q", envelope.Error.Code)
	}
	unchanged, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != string(data) {
		t.Fatalf("stale save changed the file")
	}

	// An invalid candidate is refused and names the key.
	badBody := `{"kind":"nope","base_url":"https://new.example.com/v1","expected_version":"` + shaHex(data) + `"}`
	bad := serveProvider(t, s, http.MethodPut, "/v1/providers/openai", badBody, cookie, csrf)
	if bad.code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid status = %d, body = %q", bad.code, bad.body)
	}
	envelope := decodeError(t, bad)
	if envelope.Error.Code != "validation_failed" {
		t.Fatalf("invalid error code = %q", envelope.Error.Code)
	}
	if !strings.Contains(string(bad.body), "providers.openai.kind") {
		t.Fatalf("invalid details do not name providers.openai.kind: %q", bad.body)
	}
	afterInvalid, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterInvalid) != string(data) {
		t.Fatalf("invalid save changed the file")
	}
}

func TestProvidersPutConnectionFailed(t *testing.T) {
	raw := providerTestConfig
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	const badKey = "sk-bad-provider-key"
	const goodKey = "sk-good-provider-key"
	var seen []ProviderTest
	s, cookie, csrf, logs := newProviderTestServer(t, store, func(_ context.Context, test ProviderTest) (TestResult, error) {
		seen = append(seen, test)
		if test.APIKey.Reveal() == badKey {
			return TestResult{OK: false, Status: 401, Message: "401 Unauthorized from api.openai.com"}, nil
		}
		return TestResult{OK: true, LatencyMS: 5}, nil
	})

	badBody := `{"kind":"openai_compat","base_url":"https://api.openai.com/v1","expected_version":"` + shaHex([]byte(raw)) + `","api_key":"` + badKey + `"}`
	bad := serveProvider(t, s, http.MethodPut, "/v1/providers/openai", badBody, cookie, csrf)
	if bad.code != http.StatusUnprocessableEntity {
		t.Fatalf("bad key status = %d, body = %q", bad.code, bad.body)
	}
	if envelope := decodeError(t, bad); envelope.Error.Code != "connection_failed" {
		t.Fatalf("bad key error code = %q", envelope.Error.Code)
	}
	if len(seen) != 1 {
		t.Fatalf("tester calls = %d, want 1", len(seen))
	}
	if seen[0].APIKey.Reveal() != badKey {
		t.Fatalf("tester did not receive the inline key")
	}
	if seen[0].Model != "gpt-5-mini" {
		t.Fatalf("test model = %q, want the agent model", seen[0].Model)
	}
	configBytes, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(configBytes) != raw {
		t.Fatalf("failed save changed config.toml:\n%s", configBytes)
	}
	if _, err := os.Stat(filepath.Join(store.home, "secrets.env")); !os.IsNotExist(err) {
		t.Fatalf("failed save wrote secrets.env: %v", err)
	}
	if strings.Contains(string(bad.body), badKey) || strings.Contains(logs.String(), badKey) {
		t.Fatalf("failed save leaked the inline key: body=%q logs=%q", bad.body, logs.String())
	}

	goodBody := `{"kind":"openai_compat","base_url":"https://api.openai.com/v1","expected_version":"` + shaHex([]byte(raw)) + `","api_key":"` + goodKey + `"}`
	good := serveProvider(t, s, http.MethodPut, "/v1/providers/openai", goodBody, cookie, csrf)
	if good.code != http.StatusOK {
		t.Fatalf("good key status = %d, body = %q", good.code, good.body)
	}
	configBytes, err = os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(configBytes), goodKey) {
		t.Fatalf("config.toml contains the inline key:\n%s", configBytes)
	}
	secretBytes, err := os.ReadFile(filepath.Join(store.home, "secrets.env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(secretBytes), "OPENAI_API_KEY="+goodKey) {
		t.Fatalf("secrets.env does not hold the key: %q", secretBytes)
	}
	if info, err := os.Stat(filepath.Join(store.home, "secrets.env")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("secrets.env mode = %v, err = %v", info, err)
	}
	if strings.Contains(string(good.body), goodKey) || strings.Contains(logs.String(), goodKey) {
		t.Fatalf("save leaked the inline key: body=%q logs=%q", good.body, logs.String())
	}

	list := serveProvider(t, s, http.MethodGet, "/v1/providers", "", cookie, csrf)
	if list.code != http.StatusOK {
		t.Fatalf("list status = %d, body = %q", list.code, list.body)
	}
	var listed listProvidersResponse
	list.json(t, &listed)
	if len(listed.Providers) != 1 || listed.Providers[0].KeyState != "set" {
		t.Fatalf("listed providers = %+v", listed.Providers)
	}
}

func TestProvidersDelete(t *testing.T) {
	raw := `[providers.openai]
kind = "openai_compat"
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"

[providers.spare]
kind = "openai_compat"
base_url = "https://spare.example.com/v1"
api_key_env = "SPARE_API_KEY"

[agents.coder]
provider = "openai"
model = "gpt-5-mini"
`
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	s, cookie, csrf, _ := newProviderTestServer(t, store, nil)

	usedBody := `{"expected_version":"` + shaHex([]byte(raw)) + `"}`
	used := serveProvider(t, s, http.MethodDelete, "/v1/providers/openai", usedBody, cookie, csrf)
	if used.code != http.StatusConflict {
		t.Fatalf("used delete status = %d, body = %q", used.code, used.body)
	}
	if envelope := decodeError(t, used); envelope.Error.Code != "in_use" || envelope.Error.Message != "used by agents: coder" {
		t.Fatalf("used delete error = %+v", envelope.Error)
	}
	unchanged, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != raw {
		t.Fatalf("refused delete changed the file")
	}

	deleted := serveProvider(t, s, http.MethodDelete, "/v1/providers/spare", usedBody, cookie, csrf)
	if deleted.code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %q", deleted.code, deleted.body)
	}
	var saved providerSaveResponse
	deleted.json(t, &saved)
	if saved.Name != "spare" || !saved.RestartRequired {
		t.Fatalf("delete response = %+v", saved)
	}
	after, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "[providers.spare]") {
		t.Fatalf("delete kept the provider table:\n%s", after)
	}
	if !strings.Contains(string(after), "[providers.openai]") || !strings.Contains(string(after), "[agents.coder]") {
		t.Fatalf("delete removed more than its table:\n%s", after)
	}

	stale := serveProvider(t, s, http.MethodDelete, "/v1/providers/openai", usedBody, cookie, csrf)
	if stale.code != http.StatusConflict {
		t.Fatalf("stale delete status = %d, body = %q", stale.code, stale.body)
	}
	if envelope := decodeError(t, stale); envelope.Error.Code != "stale_version" {
		t.Fatalf("stale delete error code = %q", envelope.Error.Code)
	}
}

func TestProvidersPutHeadersMask(t *testing.T) {
	raw := `[providers.openai]
kind = "openai_compat"
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"
headers = { Authorization = "Bearer stored-token", X-Org = "old" }

[agents.coder]
provider = "openai"
model = "gpt-5-mini"
`
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	s, cookie, csrf, _ := newProviderTestServer(t, store, nil)

	body := `{"kind":"openai_compat","base_url":"https://api.openai.com/v1","headers":{"Authorization":"***","X-Org":"new"},"expected_version":"` + shaHex([]byte(raw)) + `"}`
	response := serveProvider(t, s, http.MethodPut, "/v1/providers/openai", body, cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `Authorization = 'Bearer stored-token'`) {
		t.Fatalf("stored header value was not kept:\n%s", data)
	}
	if !strings.Contains(string(data), `X-Org = 'new'`) {
		t.Fatalf("changed header value was not saved:\n%s", data)
	}

	missing := `{"kind":"openai_compat","base_url":"https://api.openai.com/v1","headers":{"X-New":"***"},"expected_version":"` + shaHex(data) + `"}`
	missingResponse := serveProvider(t, s, http.MethodPut, "/v1/providers/openai", missing, cookie, csrf)
	if missingResponse.code != http.StatusUnprocessableEntity {
		t.Fatalf("missing mask status = %d, body = %q", missingResponse.code, missingResponse.body)
	}
	if !strings.Contains(string(missingResponse.body), "X-New") {
		t.Fatalf("missing mask does not name the header: %q", missingResponse.body)
	}
}

// API-RTR-006: a connection test without a model uses the model an agent or
// combo already uses with the named provider, else the kind's test model
// (internal/models.TestModel), else the base URL's catalog; a typed model is
// kept, and a custom provider with no known model must name one.
func TestProviderTestDefaultModel(t *testing.T) {
	raw := `[providers.claude]
kind = "anthropic"

[agents.coder]
model = "claude/claude-sonnet-5-5"
`
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	var seen []string
	s, cookie, csrf, _ := newProviderTestServer(t, store, func(_ context.Context, test ProviderTest) (TestResult, error) {
		seen = append(seen, test.Model)
		return TestResult{OK: true}, nil
	})
	cases := []struct {
		body string
		want string
	}{
		{`{"kind":"anthropic","api_key":"sk-x"}`, models.TestModel("anthropic")},
		{`{"kind":"deepseek","api_key":"sk-x","model":""}`, models.TestModel("deepseek")},
		{`{"kind":"openai","api_key":"sk-x"}`, models.TestModel("openai")},
		{`{"kind":"openrouter","api_key":"sk-x"}`, models.TestModel("openrouter")},
		{`{"name":"claude","kind":"anthropic","api_key":"sk-x"}`, "claude-sonnet-5-5"},
		{`{"name":"other","kind":"anthropic","api_key":"sk-x"}`, models.TestModel("anthropic")},
		{`{"kind":"anthropic","api_key":"sk-x","model":"  claude-opus-5 "}`, "claude-opus-5"},
	}
	for _, tc := range cases {
		seen = nil
		response := serveProvider(t, s, http.MethodPost, "/v1/providers/test", tc.body, cookie, csrf)
		if response.code != http.StatusOK || len(seen) != 1 || seen[0] != tc.want || tc.want == "" {
			t.Fatalf("%s = %d %s, tested %v, want %q", tc.body, response.code, response.body, seen, tc.want)
		}
	}
	unknown := serveProvider(t, s, http.MethodPost, "/v1/providers/test",
		`{"kind":"openai_compat","base_url":"https://llm.example.com/v1","api_key":"sk-x"}`, cookie, csrf)
	if unknown.code != http.StatusBadRequest || !strings.Contains(string(unknown.body), "enter a test model") {
		t.Fatalf("custom without model = %d %s", unknown.code, unknown.body)
	}
}

// API-RTR-006 revision 2: PUT accepts disabled, GET reports it, and a PUT
// without the key keeps the stored value.
func TestProvidersPutDisabledRoundTrip(t *testing.T) {
	store := newTestConfigStore(t, providerTestConfig, 0o600, ConfigSettings{})
	s, cookie, csrf, _ := newProviderTestServer(t, store, nil)
	list := func() providerJSON {
		t.Helper()
		response := serveProvider(t, s, http.MethodGet, "/v1/providers", "", cookie, csrf)
		if response.code != http.StatusOK {
			t.Fatalf("list status = %d, body = %q", response.code, response.body)
		}
		var body listProvidersResponse
		response.json(t, &body)
		if len(body.Providers) != 1 {
			t.Fatalf("providers = %+v", body.Providers)
		}
		return body.Providers[0]
	}
	put := func(body string) {
		t.Helper()
		data, err := os.ReadFile(store.path)
		if err != nil {
			t.Fatal(err)
		}
		full := `{"kind":"openai_compat","base_url":"https://api.openai.com/v1"` + body + `,"expected_version":"` + shaHex(data) + `"}`
		response := serveProvider(t, s, http.MethodPut, "/v1/providers/openai", full, cookie, csrf)
		if response.code != http.StatusOK {
			t.Fatalf("put status = %d, body = %q", response.code, response.body)
		}
	}
	if list().Disabled {
		t.Fatal("a new provider is disabled")
	}
	put(`,"disabled":true`)
	data, _ := os.ReadFile(store.path)
	if !strings.Contains(string(data), "disabled = true\n") {
		t.Fatalf("config does not hold disabled = true:\n%s", data)
	}
	if !list().Disabled {
		t.Fatal("GET does not report disabled")
	}
	put(``)
	if !list().Disabled {
		t.Fatal("a PUT without disabled turned the provider back on")
	}
	put(`,"disabled":false`)
	data, _ = os.ReadFile(store.path)
	if strings.Contains(string(data), "disabled") || list().Disabled {
		t.Fatalf("disabled = false was not saved:\n%s", data)
	}

	data, _ = os.ReadFile(store.path)
	bad := `{"kind":"openai_compat","base_url":"https://api.openai.com/v1","disabled":"yes","expected_version":"` + shaHex(data) + `"}`
	if response := serveProvider(t, s, http.MethodPut, "/v1/providers/openai", bad, cookie, csrf); response.code == http.StatusOK {
		t.Fatalf("a string disabled was accepted: %q", response.body)
	}
}

// A save of a compat preset tests the host's known model; a custom server
// with no known model is tested with a guess, and a 404 for that guess still
// saves the key (the server took it), while a 401 does not.
func TestProvidersPutTestModelForCompatHosts(t *testing.T) {
	raw := "[web]\n"
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	var seen []ProviderTest
	s, cookie, csrf, _ := newProviderTestServer(t, store, func(_ context.Context, test ProviderTest) (TestResult, error) {
		seen = append(seen, test)
		switch test.APIKey.Reveal() {
		case "sk-unknown-model":
			return TestResult{OK: false, Status: 404, Message: "model not found"}, nil
		case "sk-bad":
			return TestResult{OK: false, Status: 401, Message: "bad key"}, nil
		}
		return TestResult{OK: true, LatencyMS: 3}, nil
	})
	put := func(name, base, key string) int {
		t.Helper()
		data, err := os.ReadFile(store.path)
		if err != nil {
			t.Fatal(err)
		}
		body := `{"kind":"openai_compat","base_url":"` + base + `","api_key":"` + key + `","expected_version":"` + shaHex(data) + `"}`
		response := serveProvider(t, s, http.MethodPut, "/v1/providers/"+name, body, cookie, csrf)
		if response.code != http.StatusOK {
			t.Logf("%s: %s", name, response.body)
		}
		return response.code
	}
	if code := put("groq", "https://api.groq.com/openai/v1", "sk-good"); code != http.StatusOK {
		t.Fatalf("groq save status = %d", code)
	}
	if got := seen[len(seen)-1].Model; got != "llama-3.1-8b-instant" {
		t.Fatalf("groq test model = %q", got)
	}
	if code := put("local", "http://127.0.0.1:9/v1", "sk-unknown-model"); code != http.StatusOK {
		t.Fatalf("a 404 for a guessed model refused the save: %d", code)
	}
	if code := put("local-2", "http://127.0.0.1:9/v1", "sk-bad"); code != http.StatusUnprocessableEntity {
		t.Fatalf("a 401 saved the key: %d", code)
	}
}
