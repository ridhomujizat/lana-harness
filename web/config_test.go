package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// testConfigStore is the ConfigStore used by the web handler tests. It reads
// and writes a real file and runs the real config.Check.
type testConfigStore struct {
	home     string
	path     string
	loaded   string
	settings ConfigSettings
}

func newTestConfigStore(t *testing.T, raw string, mode os.FileMode, settings ConfigSettings) *testConfigStore {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, "config.toml")
	if err := os.WriteFile(path, []byte(raw), mode); err != nil {
		t.Fatal(err)
	}
	return &testConfigStore{home: home, path: path, loaded: shaHex([]byte(raw)), settings: settings}
}

func (s *testConfigStore) ConfigFile(ctx context.Context) (ConfigFile, error) {
	if err := ctx.Err(); err != nil {
		return ConfigFile{}, err
	}
	file := ConfigFile{Path: s.path}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return file, nil
	}
	if err != nil {
		return ConfigFile{}, err
	}
	file.Raw = data
	file.Version = shaHex(data)
	file.Exists = true
	return file, nil
}

func (s *testConfigStore) ConfigCheck(ctx context.Context, candidate []byte) ([]ConfigIssue, error) {
	_, err := config.Check(ctx, s.home, candidate)
	if err == nil {
		return nil, nil
	}
	var errs config.Errors
	if !errors.As(err, &errs) {
		return nil, err
	}
	issues := make([]ConfigIssue, 0, len(errs))
	for _, item := range errs {
		issues = append(issues, ConfigIssue{File: item.File, Key: item.Key, Message: item.Msg})
	}
	return issues, nil
}

func (s *testConfigStore) ConfigWrite(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return webfiles.AtomicWrite(s.path, data)
}

func (s *testConfigStore) ConfigLoadedVersion() string { return s.loaded }

func (s *testConfigStore) ConfigSettings(context.Context) (ConfigSettings, error) {
	return s.settings, nil
}

func shaHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func newConfigTestServer(t *testing.T, store *testConfigStore) (*Server, *http.Cookie, string) {
	t.Helper()
	token := authTokenFor(t, store.home)
	s := newAuthServer(t, store.home, io.Discard)
	s.SetConfigStore(store)
	cookie, csrf := loginAndSession(t, s, token)
	return s, cookie, csrf
}

func serveConfig(t *testing.T, s *Server, method, body string, cookie *http.Cookie, csrf string) *httptestResponse {
	t.Helper()
	req := authRequest(method, "/v1/config", body)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set(csrfHeaderName, csrf)
	}
	rr := serveAuth(s, req)
	return &httptestResponse{code: rr.Code, body: rr.Body.Bytes()}
}

type httptestResponse struct {
	code int
	body []byte
}

func (r *httptestResponse) json(t *testing.T, dst any) {
	t.Helper()
	if err := json.Unmarshal(r.body, dst); err != nil {
		t.Fatalf("decode %q: %v", r.body, err)
	}
}

func decodeError(t *testing.T, response *httptestResponse) errorEnvelope {
	t.Helper()
	var envelope errorEnvelope
	response.json(t, &envelope)
	return envelope
}

const configMaskRaw = `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.coder]
provider = "mock"
model = "coder"

[mcp.remote]
url = "https://example.com/mcp"

[mcp.remote.headers]
Authorization = "Bearer x"
`

// TestConfigMaskHidesMCPSecrets covers AC-WEB-022: the structured view and the
// raw text show "***", the validation status is present, and the value never
// appears in the response.
func TestConfigMaskHidesMCPSecrets(t *testing.T) {
	settings := ConfigSettings{
		Providers: map[string]ConfigProvider{},
		Agents:    map[string]ConfigAgent{},
		Routes:    []ConfigRoute{},
		Channels:  map[string]map[string]any{},
		MCP: map[string]ConfigMCPServer{
			"remote": {URL: "https://example.com/mcp", Headers: map[string]string{"Authorization": "***"}},
		},
		Modules: []ConfigModule{},
	}
	store := newTestConfigStore(t, configMaskRaw, 0o600, settings)
	s, cookie, _ := newConfigTestServer(t, store)

	response := serveConfig(t, s, http.MethodGet, "", cookie, "")
	if response.code != http.StatusOK {
		t.Fatalf("GET /v1/config status = %d, body = %q", response.code, response.body)
	}
	var got getConfigResponse
	response.json(t, &got)
	if got.Validation.OK != true {
		t.Fatalf("validation = %+v", got.Validation)
	}
	if got.Settings.MCP["remote"].Headers["Authorization"] != "***" {
		t.Fatalf("structured headers = %v", got.Settings.MCP["remote"].Headers)
	}
	if !strings.Contains(got.Raw, `"***"`) {
		t.Fatalf("raw text is not masked:\n%s", got.Raw)
	}
	if strings.Contains(string(response.body), "Bearer x") {
		t.Fatalf("response leaked the secret: %s", response.body)
	}
	if got.Version != store.loaded || got.LoadedVersion != store.loaded || got.RestartRequired {
		t.Fatalf("versions: version=%q loaded=%q restart=%v", got.Version, got.LoadedVersion, got.RestartRequired)
	}
}

// TestConfigSaveInvalidCandidateKeepsFile covers AC-WEB-023.
func TestConfigSaveInvalidCandidateKeepsFile(t *testing.T) {
	raw := `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.coder]
provider = "mock"
model = "coder"
`
	store := newTestConfigStore(t, raw, 0o640, ConfigSettings{})
	s, cookie, csrf := newConfigTestServer(t, store)
	before, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}

	invalid := strings.Replace(raw, `provider = "mock"`, `provider = "nope"`, 1)
	body := mustJSON(t, map[string]string{"raw": invalid, "expected_version": store.loaded})
	response := serveConfig(t, s, http.MethodPut, body, cookie, csrf)
	if response.code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid PUT status = %d, body = %q", response.code, response.body)
	}
	envelope := decodeError(t, response)
	if envelope.Error.Code != "validation_failed" {
		t.Fatalf("error code = %q, body = %q", envelope.Error.Code, response.body)
	}
	found := false
	for _, detail := range envelope.Error.Details {
		if detail["key"] == "agents.coder.provider" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no agents.coder.provider detail: %s", response.body)
	}
	assertFile(t, store.path, before, 0o640)

	valid := strings.Replace(raw, `model = "coder"`, `model = "coder-2"`, 1)
	body = mustJSON(t, map[string]string{"raw": valid, "expected_version": store.loaded})
	response = serveConfig(t, s, http.MethodPut, body, cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("valid PUT status = %d, body = %q", response.code, response.body)
	}
	assertFile(t, store.path, []byte(valid), 0o640)
}

// TestConfigSaveStaleVersion covers AC-WEB-024.
func TestConfigSaveStaleVersion(t *testing.T) {
	raw := `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.coder]
provider = "mock"
model = "coder"
`
	store := newTestConfigStore(t, raw, 0o644, ConfigSettings{})
	s, cookie, csrf := newConfigTestServer(t, store)

	saveA := strings.Replace(raw, `model = "coder"`, `model = "a"`, 1)
	response := serveConfig(t, s, http.MethodPut, mustJSON(t, map[string]string{"raw": saveA, "expected_version": store.loaded}), cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("save A status = %d, body = %q", response.code, response.body)
	}

	saveB := strings.Replace(raw, `model = "coder"`, `model = "b"`, 1)
	response = serveConfig(t, s, http.MethodPut, mustJSON(t, map[string]string{"raw": saveB, "expected_version": store.loaded}), cookie, csrf)
	if response.code != http.StatusConflict {
		t.Fatalf("save B status = %d, body = %q", response.code, response.body)
	}
	envelope := decodeError(t, response)
	if envelope.Error.Code != "stale_version" {
		t.Fatalf("error code = %q, body = %q", envelope.Error.Code, response.body)
	}
	assertFile(t, store.path, []byte(saveA), 0o644)
}

// TestConfigSaveUnmasksStoredValues covers the mask round trip and the refusal
// of a masked value for a key that does not exist on disk.
func TestConfigSaveUnmasksStoredValues(t *testing.T) {
	raw := `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.coder]
provider = "mock"
model = "coder"

[mcp.remote]
url = "https://example.com/mcp"

[mcp.remote.headers]
Authorization = "Bearer x"
`
	store := newTestConfigStore(t, raw, 0o640, ConfigSettings{})
	s, cookie, csrf := newConfigTestServer(t, store)

	masked := strings.Replace(raw, `Authorization = "Bearer x"`, `Authorization = "***"`, 1)
	masked = strings.Replace(masked, `model = "coder"`, `model = "coder-2"`, 1)
	response := serveConfig(t, s, http.MethodPut, mustJSON(t, map[string]string{"raw": masked, "expected_version": store.loaded}), cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("masked save status = %d, body = %q", response.code, response.body)
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Bearer x") {
		t.Fatalf("stored value was not restored:\n%s", data)
	}
	if strings.Contains(string(data), "***") {
		t.Fatalf("masked value was written:\n%s", data)
	}

	// A new masked key has no stored value to restore.
	version := shaHex(data)
	candidate := strings.Replace(string(data), "[mcp.remote.headers]\n", "[mcp.remote.headers]\nX-New = \"***\"\n", 1)
	if candidate == string(data) {
		t.Fatal("test did not add the new masked key")
	}
	response = serveConfig(t, s, http.MethodPut, mustJSON(t, map[string]string{"raw": candidate, "expected_version": version}), cookie, csrf)
	if response.code != http.StatusUnprocessableEntity {
		t.Fatalf("new masked key status = %d, body = %q", response.code, response.body)
	}
	envelope := decodeError(t, response)
	if envelope.Error.Code != "validation_failed" {
		t.Fatalf("error code = %q, body = %q", envelope.Error.Code, response.body)
	}
	found := false
	for _, detail := range envelope.Error.Details {
		if detail["key"] == "mcp.remote.headers.X-New" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no new-key detail: %s", response.body)
	}
}

// TestConfigSaveRestartRequired covers the restart flag and the loaded version
// staying at the daemon start value.
func TestConfigSaveRestartRequired(t *testing.T) {
	raw := `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.coder]
provider = "mock"
model = "coder"
`
	store := newTestConfigStore(t, raw, 0o644, ConfigSettings{})
	s, cookie, csrf := newConfigTestServer(t, store)

	changed := strings.Replace(raw, `model = "coder"`, `model = "coder-2"`, 1)
	response := serveConfig(t, s, http.MethodPut, mustJSON(t, map[string]string{"raw": changed, "expected_version": store.loaded}), cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %q", response.code, response.body)
	}
	var saved putConfigResponse
	response.json(t, &saved)
	if !saved.RestartRequired || saved.Version == store.loaded {
		t.Fatalf("save response = %+v", saved)
	}

	response = serveConfig(t, s, http.MethodGet, "", cookie, "")
	var got getConfigResponse
	response.json(t, &got)
	if !got.RestartRequired || got.LoadedVersion != store.loaded || got.Version != saved.Version {
		t.Fatalf("GET after save = version %q loaded %q restart %v", got.Version, got.LoadedVersion, got.RestartRequired)
	}
}

func TestConfigSaveInvalidRequest(t *testing.T) {
	store := newTestConfigStore(t, "[agents.x]\nmodel = \"m\"\n", 0o644, ConfigSettings{})
	s, cookie, csrf := newConfigTestServer(t, store)
	for _, body := range []string{"{not json", `{"raw":"","expected_version":"x"}`} {
		response := serveConfig(t, s, http.MethodPut, body, cookie, csrf)
		if response.code != http.StatusBadRequest {
			t.Fatalf("body %q status = %d, response = %q", body, response.code, response.body)
		}
		if code := decodeError(t, response).Error.Code; code != "invalid_request" {
			t.Fatalf("body %q error code = %q", body, code)
		}
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertFile(t *testing.T, path string, want []byte, mode os.FileMode) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(want) {
		t.Fatalf("file = %q, want %q", data, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != mode {
		t.Fatalf("mode = %04o, want %04o", got, mode)
	}
}

// TestConfigMaskKeepsCommentsAndInlineTables covers the raw-text editor with
// the inline env form and a dotted headers key.
func TestConfigMaskKeepsCommentsAndInlineTables(t *testing.T) {
	const raw = `# keep this comment
[mcp.local]
command = "srv"
env = { TOKEN = "literal-value", OTHER = "two" } # inline comment
headers.Authorization = "Bearer x"
`
	masked, err := maskConfigRaw([]byte(raw))
	if err != nil {
		t.Fatalf("maskConfigRaw: %v", err)
	}
	if strings.Contains(string(masked), "literal-value") || strings.Contains(string(masked), "Bearer x") {
		t.Fatalf("masked text leaked a value:\n%s", masked)
	}
	if !strings.Contains(string(masked), "# keep this comment") || !strings.Contains(string(masked), "# inline comment") {
		t.Fatalf("masked text lost a comment:\n%s", masked)
	}
	if got := strings.Count(string(masked), `"***"`); got != 3 {
		t.Fatalf("masked values = %d, want 3:\n%s", got, masked)
	}
	unmasked, issues, err := unmaskConfig([]byte(raw), masked, "config.toml")
	if err != nil {
		t.Fatalf("unmaskConfig: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("unmask issues = %+v", issues)
	}
	if !strings.Contains(string(unmasked), "literal-value") || !strings.Contains(string(unmasked), "Bearer x") {
		t.Fatalf("unmasked text lost a stored value:\n%s", unmasked)
	}
	if strings.Contains(string(unmasked), "***") {
		t.Fatalf("unmasked text still has a mask:\n%s", unmasked)
	}
}
