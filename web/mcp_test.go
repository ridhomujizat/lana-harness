package web

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
)

// testMCPBackend adds the MCP port to the in-memory backend.
type testMCPBackend struct {
	*fakeWebBackend
	servers  []MCPServerRow
	restart  func(ctx context.Context, name string) MCPState
	test     func(ctx context.Context, def MCPDefinition) MCPTestResult
	restarts []string
	tests    []MCPDefinition
}

func (b *testMCPBackend) MCPServers() []MCPServerRow { return b.servers }

func (b *testMCPBackend) MCPRestart(ctx context.Context, name string) MCPState {
	b.restarts = append(b.restarts, name)
	if b.restart == nil {
		return MCPState{Name: name, State: "stopped"}
	}
	return b.restart(ctx, name)
}

func (b *testMCPBackend) MCPTest(ctx context.Context, def MCPDefinition) MCPTestResult {
	b.tests = append(b.tests, def)
	if b.test == nil {
		return MCPTestResult{OK: true}
	}
	return b.test(ctx, def)
}

func newMCPTestServer(t *testing.T, store *testConfigStore, backend *testMCPBackend) (*Server, *http.Cookie, string, *bytes.Buffer) {
	t.Helper()
	s, cookie, csrf := newConfigTestServer(t, store)
	if backend != nil {
		s.SetBackend(backend)
	}
	var logs bytes.Buffer
	s.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return s, cookie, csrf, &logs
}

func serveMCP(t *testing.T, s *Server, method, target, body string, cookie *http.Cookie, csrf string) *httptestResponse {
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

const mcpTestConfig = `[mcp.docs]
command = "docs-server"
args = ["--verbose", "--port", "0"]
env = { TOKEN = "docs-secret", LOG = "debug" }

[mcp.remote]
url = "https://example.com/mcp"

[mcp.remote.headers]
Authorization = "Bearer remote-secret"

[mcp.idle]
command = "idle-server"

[agents.coder]
provider = "mock"
model = "mock-model"
mcp = ["docs"]

[agents.writer]
provider = "mock"
model = "mock-model"
mcp = ["docs", "remote"]

[providers.mock]
kind = "openai_compat"
base_url = "https://example.com/v1"
`

func TestMCPList(t *testing.T) {
	raw := mcpTestConfig
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	backend := &testMCPBackend{
		fakeWebBackend: newFakeWebBackend(),
		servers: []MCPServerRow{
			{Name: "docs", State: "running", ToolsCount: 26, RunningForMS: 7200000},
			{Name: "remote", State: "unavailable", Error: "unauthorized"},
		},
	}
	s, cookie, csrf, _ := newMCPTestServer(t, store, backend)

	response := serveMCP(t, s, http.MethodGet, "/v1/mcp", "", cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	var body mcpListResponse
	response.json(t, &body)
	if body.Version != shaHex([]byte(raw)) {
		t.Fatalf("version = %q", body.Version)
	}
	if len(body.Servers) != 3 {
		t.Fatalf("servers = %d, want 3: %+v", len(body.Servers), body.Servers)
	}
	byName := map[string]MCPServerRow{}
	for _, row := range body.Servers {
		byName[row.Name] = row
	}

	docs := byName["docs"]
	if docs.Type != "command" || docs.Command != "docs-server" {
		t.Fatalf("docs definition = %+v", docs)
	}
	if strings.Join(docs.Args, " ") != "--verbose --port 0" {
		t.Fatalf("docs args = %v", docs.Args)
	}
	if strings.Join(docs.EnvKeys, ",") != "LOG,TOKEN" {
		t.Fatalf("docs env_keys = %v", docs.EnvKeys)
	}
	if len(docs.HeaderKeys) != 0 {
		t.Fatalf("docs header_keys = %v", docs.HeaderKeys)
	}
	if strings.Join(docs.Agents, ",") != "coder,writer" {
		t.Fatalf("docs agents = %v", docs.Agents)
	}
	if docs.State != "running" || docs.Status != "connected" {
		t.Fatalf("docs state/status = %q/%q", docs.State, docs.Status)
	}
	if docs.ToolsCount != 26 || docs.RunningForMS != 7200000 {
		t.Fatalf("docs tools/running = %d/%d", docs.ToolsCount, docs.RunningForMS)
	}

	remote := byName["remote"]
	if remote.Type != "url" || remote.URL != "https://example.com/mcp" {
		t.Fatalf("remote definition = %+v", remote)
	}
	if strings.Join(remote.HeaderKeys, ",") != "Authorization" {
		t.Fatalf("remote header_keys = %v", remote.HeaderKeys)
	}
	if len(remote.EnvKeys) != 0 {
		t.Fatalf("remote env_keys = %v", remote.EnvKeys)
	}
	if remote.State != "unavailable" || remote.Status != "unreachable" || remote.Error != "unauthorized" {
		t.Fatalf("remote state/status/error = %q/%q/%q", remote.State, remote.Status, remote.Error)
	}

	idle := byName["idle"]
	if idle.State != "stopped" || idle.Status != "idle" {
		t.Fatalf("idle state/status = %q/%q", idle.State, idle.Status)
	}
	if len(idle.Agents) != 0 {
		t.Fatalf("idle agents = %v", idle.Agents)
	}

	// Only key names are returned. Values never reach the response.
	for _, secret := range []string{"docs-secret", "remote-secret", "debug"} {
		if strings.Contains(string(response.body), secret) {
			t.Fatalf("MCP list leaked a value %q: %q", secret, response.body)
		}
	}
}

func TestMCPRestartFakeClient(t *testing.T) {
	raw := `[mcp.docs]
command = "docs-server"

[agents.coder]
provider = "mock"
model = "mock-model"
mcp = ["docs"]

[providers.mock]
kind = "openai_compat"
base_url = "https://example.com/v1"
`
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	backend := &testMCPBackend{
		fakeWebBackend: newFakeWebBackend(),
		restart: func(_ context.Context, name string) MCPState {
			return MCPState{Name: name, State: "running"}
		},
	}
	s, cookie, csrf, _ := newMCPTestServer(t, store, backend)

	response := serveMCP(t, s, http.MethodPost, "/v1/mcp/docs/restart", "", cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	var body MCPState
	response.json(t, &body)
	if body.Name != "docs" || body.State != "running" || body.Error != "" {
		t.Fatalf("restart response = %+v", body)
	}
	if len(backend.restarts) != 1 || backend.restarts[0] != "docs" {
		t.Fatalf("backend restarts = %v", backend.restarts)
	}

	// A failed restart is still a 200 with state and error.
	backend.restart = func(_ context.Context, name string) MCPState {
		return MCPState{Name: name, State: "unavailable", Error: "connect failed"}
	}
	failed := serveMCP(t, s, http.MethodPost, "/v1/mcp/docs/restart", "", cookie, csrf)
	if failed.code != http.StatusOK {
		t.Fatalf("failed restart status = %d, body = %q", failed.code, failed.body)
	}
	var failedBody MCPState
	failed.json(t, &failedBody)
	if failedBody.State != "unavailable" || failedBody.Error != "connect failed" {
		t.Fatalf("failed restart response = %+v", failedBody)
	}

	// A name that is not configured is 404 and never reaches the client.
	unknown := serveMCP(t, s, http.MethodPost, "/v1/mcp/ghost/restart", "", cookie, csrf)
	if unknown.code != http.StatusNotFound {
		t.Fatalf("unknown restart status = %d, body = %q", unknown.code, unknown.body)
	}
	if envelope := decodeError(t, unknown); envelope.Error.Code != "not_found" {
		t.Fatalf("unknown restart error code = %q", envelope.Error.Code)
	}
	if len(backend.restarts) != 2 {
		t.Fatalf("unknown restart reached the client: %v", backend.restarts)
	}
}

func TestMCPTestOKAndError(t *testing.T) {
	raw := `[mcp.docs]
command = "docs-server"
`
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	backend := &testMCPBackend{
		fakeWebBackend: newFakeWebBackend(),
		test: func(_ context.Context, def MCPDefinition) MCPTestResult {
			if def.Command == "definitely-not-a-command" {
				return MCPTestResult{Message: "exec: \"definitely-not-a-command\": executable file not found"}
			}
			return MCPTestResult{OK: true, ToolCount: 18}
		},
	}
	s, cookie, csrf, _ := newMCPTestServer(t, store, backend)

	goodBody := `{"type":"command","command":"docs-server","args":["--verbose"],"env":{"TOKEN":"inline-secret"}}`
	good := serveMCP(t, s, http.MethodPost, "/v1/mcp/test", goodBody, cookie, csrf)
	if good.code != http.StatusOK {
		t.Fatalf("good test status = %d, body = %q", good.code, good.body)
	}
	var goodResult mcpTestOKResponse
	good.json(t, &goodResult)
	if !goodResult.OK || goodResult.ToolCount != 18 {
		t.Fatalf("good test result = %+v", goodResult)
	}
	if len(backend.tests) != 1 || backend.tests[0].Env["TOKEN"] != "inline-secret" {
		t.Fatalf("test definition = %+v", backend.tests)
	}

	badBody := `{"type":"command","command":"definitely-not-a-command"}`
	bad := serveMCP(t, s, http.MethodPost, "/v1/mcp/test", badBody, cookie, csrf)
	if bad.code != http.StatusOK {
		t.Fatalf("bad test status = %d, body = %q", bad.code, bad.body)
	}
	var badResult map[string]any
	bad.json(t, &badResult)
	if badResult["ok"] != false {
		t.Fatalf("bad test result = %v", badResult)
	}
	if !strings.Contains(badResult["message"].(string), "executable file not found") {
		t.Fatalf("bad test message = %v", badResult["message"])
	}
	if _, ok := badResult["tool_count"]; ok {
		t.Fatalf("failed test has tool_count: %v", badResult)
	}

	// A test starts a temporary client. It never writes the definition.
	after, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != raw {
		t.Fatalf("MCP test changed config.toml:\n%s", after)
	}
	if strings.Contains(string(good.body)+string(bad.body), "inline-secret") {
		t.Fatalf("test response leaked an inline value")
	}
}

func TestMCPTestRejectsBadDefinition(t *testing.T) {
	raw := `[mcp.docs]
command = "docs-server"
`
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	backend := &testMCPBackend{fakeWebBackend: newFakeWebBackend()}
	s, cookie, csrf, _ := newMCPTestServer(t, store, backend)

	cases := []string{
		`{"type":"nope","command":"x"}`,
		`{"type":"command"}`,
		`{"type":"url","url":"ftp://example.com/mcp"}`,
		`{"type":"command","command":"x","url":"https://example.com/mcp"}`,
	}
	for _, body := range cases {
		response := serveMCP(t, s, http.MethodPost, "/v1/mcp/test", body, cookie, csrf)
		if response.code != http.StatusUnprocessableEntity {
			t.Fatalf("body %s status = %d, want 422", body, response.code)
		}
		if envelope := decodeError(t, response); envelope.Error.Code != "validation_failed" {
			t.Fatalf("body %s error code = %q", body, envelope.Error.Code)
		}
	}
	if len(backend.tests) != 0 {
		t.Fatalf("invalid definitions reached the client: %+v", backend.tests)
	}
}

func TestMCPPutMasksValues(t *testing.T) {
	raw := `[mcp.docs]
command = "docs-server"
env = { TOKEN = "stored-token", OLD = "old" }
`
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	s, cookie, csrf, _ := newMCPTestServer(t, store, nil)

	body := `{"type":"command","command":"docs-server","args":["--v"],"env":{"TOKEN":"***","OLD":"new"},"expected_version":"` + shaHex([]byte(raw)) + `"}`
	response := serveMCP(t, s, http.MethodPut, "/v1/mcp/docs", body, cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	var saved mcpSaveResponse
	response.json(t, &saved)
	if saved.Name != "docs" || !saved.RestartRequired {
		t.Fatalf("save response = %+v", saved)
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `TOKEN = 'stored-token'`) {
		t.Fatalf("stored env value was not kept:\n%s", data)
	}
	if !strings.Contains(string(data), `OLD = 'new'`) {
		t.Fatalf("new env value was not saved:\n%s", data)
	}
	if strings.Contains(string(response.body), "stored-token") {
		t.Fatalf("save response leaked an env value: %q", response.body)
	}

	// A mask with no stored value is refused.
	missing := `{"type":"command","command":"docs-server","env":{"NEW":"***"},"expected_version":"` + shaHex(data) + `"}`
	missingResponse := serveMCP(t, s, http.MethodPut, "/v1/mcp/docs", missing, cookie, csrf)
	if missingResponse.code != http.StatusUnprocessableEntity {
		t.Fatalf("missing mask status = %d, body = %q", missingResponse.code, missingResponse.body)
	}
	if !strings.Contains(string(missingResponse.body), "mcp.docs.env.NEW") {
		t.Fatalf("missing mask does not name the key: %q", missingResponse.body)
	}

	// A bad URL is refused and names the key.
	badURL := `{"type":"url","url":"ftp://example.com/mcp","expected_version":"` + shaHex(data) + `"}`
	badResponse := serveMCP(t, s, http.MethodPut, "/v1/mcp/docs", badURL, cookie, csrf)
	if badResponse.code != http.StatusUnprocessableEntity {
		t.Fatalf("bad url status = %d, body = %q", badResponse.code, badResponse.body)
	}
	if !strings.Contains(string(badResponse.body), "mcp.docs.url") {
		t.Fatalf("bad url does not name the key: %q", badResponse.body)
	}
}

func TestMCPDeleteRefusedWhenAgentListsServer(t *testing.T) {
	raw := `[mcp.docs]
command = "docs-server"

[mcp.spare]
command = "spare-server"

[agents.coder]
provider = "mock"
model = "mock-model"
mcp = ["docs"]

[providers.mock]
kind = "openai_compat"
base_url = "https://example.com/v1"
`
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	s, cookie, csrf, _ := newMCPTestServer(t, store, nil)

	body := `{"expected_version":"` + shaHex([]byte(raw)) + `"}`
	used := serveMCP(t, s, http.MethodDelete, "/v1/mcp/docs", body, cookie, csrf)
	if used.code != http.StatusUnprocessableEntity {
		t.Fatalf("used delete status = %d, body = %q", used.code, used.body)
	}
	if envelope := decodeError(t, used); envelope.Error.Code != "validation_failed" {
		t.Fatalf("used delete error code = %q", envelope.Error.Code)
	}
	if !strings.Contains(string(used.body), "agents.coder.mcp") {
		t.Fatalf("used delete does not list the agent: %q", used.body)
	}
	unchanged, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != raw {
		t.Fatalf("refused delete changed the file")
	}

	deleted := serveMCP(t, s, http.MethodDelete, "/v1/mcp/spare", body, cookie, csrf)
	if deleted.code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %q", deleted.code, deleted.body)
	}
	var saved mcpSaveResponse
	deleted.json(t, &saved)
	if saved.Name != "spare" || !saved.RestartRequired {
		t.Fatalf("delete response = %+v", saved)
	}
	after, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "[mcp.spare]") {
		t.Fatalf("delete kept the server table:\n%s", after)
	}
	if !strings.Contains(string(after), "[mcp.docs]") || !strings.Contains(string(after), "[agents.coder]") {
		t.Fatalf("delete removed more than its table:\n%s", after)
	}

	unknownBody := `{"expected_version":"` + shaHex(after) + `"}`
	unknown := serveMCP(t, s, http.MethodDelete, "/v1/mcp/ghost", unknownBody, cookie, csrf)
	if unknown.code != http.StatusNotFound {
		t.Fatalf("unknown delete status = %d, body = %q", unknown.code, unknown.body)
	}
}

func TestMCPPutStaleVersion(t *testing.T) {
	raw := `[mcp.docs]
command = "docs-server"
`
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	s, cookie, csrf, _ := newMCPTestServer(t, store, nil)

	body := `{"type":"command","command":"docs-server","expected_version":"stale"}`
	response := serveMCP(t, s, http.MethodPut, "/v1/mcp/docs", body, cookie, csrf)
	if response.code != http.StatusConflict {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	if envelope := decodeError(t, response); envelope.Error.Code != "stale_version" {
		t.Fatalf("error code = %q", envelope.Error.Code)
	}
	after, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != raw {
		t.Fatalf("stale save changed the file")
	}
}
