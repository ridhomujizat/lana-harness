package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
)

// testLiveConfigStore is a ConfigStore that also implements LiveConfigStore.
type testLiveConfigStore struct {
	*testConfigStore
	applied  [][]byte
	applyErr error
	// advance moves the loaded version to each applied candidate, like the
	// daemon when no restart-only change is pending.
	advance bool
}

func (s *testLiveConfigStore) ApplyLive(ctx context.Context, candidate []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.applyErr != nil {
		return s.applyErr
	}
	s.applied = append(s.applied, append([]byte(nil), candidate...))
	if s.advance {
		s.loaded = shaHex(candidate)
	}
	return nil
}

func newLiveConfigTestServer(t *testing.T, store *testLiveConfigStore) (*Server, *http.Cookie, string) {
	t.Helper()
	token := authTokenFor(t, store.home)
	s := newAuthServer(t, store.home, io.Discard)
	s.SetConfigStore(store)
	cookie, csrf := loginAndSession(t, s, token)
	return s, cookie, csrf
}

const liveAgentConfig = `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.coder]
provider = "mock"
model = "old-model"
`

// TestAgentSaveRestartRequiredLive covers the handler side of AC-TFB-006: an
// applied agent save answers restart_required false.
func TestAgentSaveRestartRequiredLive(t *testing.T) {
	raw := liveAgentConfig
	store := &testLiveConfigStore{testConfigStore: newTestConfigStore(t, raw, 0o600, ConfigSettings{})}
	s, cookie, csrf := newLiveConfigTestServer(t, store)

	body := `{"model":"new-model","expected_version":"` + shaHex([]byte(raw)) + `"}`
	response := serveMCP(t, s, http.MethodPut, "/v1/agents/coder", body, cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	var saved putAgentResponse
	response.json(t, &saved)
	if saved.RestartRequired {
		t.Fatal("restart_required = true after a live apply")
	}
	if len(store.applied) != 1 || !strings.Contains(string(store.applied[0]), `model = 'new-model'`) {
		t.Fatalf("ApplyLive calls = %q", store.applied)
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `model = 'new-model'`) {
		t.Fatalf("config.toml was not written:\n%s", data)
	}
}

// TestAgentSaveApplyFailure covers API-TFB-006: the write stays and the
// response asks for a restart when the live apply fails.
func TestAgentSaveApplyFailure(t *testing.T) {
	raw := liveAgentConfig
	store := &testLiveConfigStore{
		testConfigStore: newTestConfigStore(t, raw, 0o600, ConfigSettings{}),
		applyErr:        errors.New("apply failed"),
	}
	s, cookie, csrf := newLiveConfigTestServer(t, store)
	var logs bytes.Buffer
	s.logger = slog.New(slog.NewTextHandler(&logs, nil))

	body := `{"model":"new-model","expected_version":"` + shaHex([]byte(raw)) + `"}`
	response := serveMCP(t, s, http.MethodPut, "/v1/agents/coder", body, cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	var saved putAgentResponse
	response.json(t, &saved)
	if !saved.RestartRequired {
		t.Fatal("restart_required = false after a failed apply")
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `model = 'new-model'`) {
		t.Fatalf("config.toml was not written:\n%s", data)
	}
	if !strings.Contains(logs.String(), "apply live config") {
		t.Fatalf("apply failure was not logged: %q", logs.String())
	}
}

const liveMCPConfig = `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[mcp.docs]
command = "docs-server"
`

// TestMCPSaveRestartRequiredLive covers API-TFB-006 for an MCP definition save.
func TestMCPSaveRestartRequiredLive(t *testing.T) {
	raw := liveMCPConfig
	store := &testLiveConfigStore{testConfigStore: newTestConfigStore(t, raw, 0o600, ConfigSettings{})}
	s, cookie, csrf := newLiveConfigTestServer(t, store)

	body := `{"type":"command","command":"docs-server","args":["--v"],"expected_version":"` + shaHex([]byte(raw)) + `"}`
	response := serveMCP(t, s, http.MethodPut, "/v1/mcp/docs", body, cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	var saved mcpSaveResponse
	response.json(t, &saved)
	if saved.RestartRequired {
		t.Fatal("restart_required = true after a live MCP apply")
	}
	if len(store.applied) != 1 {
		t.Fatalf("ApplyLive calls = %d, want 1", len(store.applied))
	}
}

const liveTeamConfig = `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.coder]
provider = "mock"
model = "m"

[agents.writer]
provider = "mock"
model = "m"
`

// TestTeamSaveRestartRequiredLive covers API-TFB-006 for a team save.
func TestTeamSaveRestartRequiredLive(t *testing.T) {
	raw := liveTeamConfig
	store := &testLiveConfigStore{testConfigStore: newTestConfigStore(t, raw, 0o600, ConfigSettings{})}
	s, cookie, csrf := newLiveConfigTestServer(t, store)

	body := `{"name":"squad","lead":"coder","members":["writer"],"expected_version":"` + shaHex([]byte(raw)) + `"}`
	response := serveMCP(t, s, http.MethodPost, "/v1/teams", body, cookie, csrf)
	if response.code != http.StatusCreated {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	var created teamCreatedResponse
	response.json(t, &created)
	if created.RestartRequired {
		t.Fatal("restart_required = true after a live team apply")
	}
	if len(store.applied) != 1 {
		t.Fatalf("ApplyLive calls = %d, want 1", len(store.applied))
	}
}

// TestProviderSaveAppliesLive covers the web side of AC-RTR-011 (which
// supersedes AC-TFB-015): a provider save reaches ApplyLive and answers
// restart_required false once the store has loaded the written version.
func TestProviderSaveAppliesLive(t *testing.T) {
	raw := liveAgentConfig
	store := &testLiveConfigStore{testConfigStore: newTestConfigStore(t, raw, 0o600, ConfigSettings{}), advance: true}
	s, cookie, csrf := newLiveConfigTestServer(t, store)

	body := `{"kind":"openai_compat","base_url":"https://example.com/v2","expected_version":"` + shaHex([]byte(raw)) + `"}`
	response := serveMCP(t, s, http.MethodPut, "/v1/providers/mock", body, cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	var saved providerSaveResponse
	response.json(t, &saved)
	if saved.RestartRequired {
		t.Fatal("restart_required = true for a live provider save")
	}
	if len(store.applied) != 1 || !strings.Contains(string(store.applied[0]), "https://example.com/v2") {
		t.Fatalf("ApplyLive calls = %q", store.applied)
	}

	// A store that keeps an older loaded version (another restart-only change
	// is pending) still reports restart_required.
	store.advance = false
	store.loaded = "pending-restart"
	body = `{"kind":"openai_compat","base_url":"https://example.com/v3","expected_version":"` + saved.Version + `"}`
	response = serveMCP(t, s, http.MethodPut, "/v1/providers/mock", body, cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	response.json(t, &saved)
	if !saved.RestartRequired {
		t.Fatal("restart_required = false while another change is pending")
	}
}
