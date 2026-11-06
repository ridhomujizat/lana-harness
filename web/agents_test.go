package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
	"github.com/ridhomujizat/lana-harness/internal/id"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

const t016AgentsConfig = `version = 1

[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.coder]
provider = "mock"
model = "coder"
tools = ["read_file", "bash"]
permission = "ask"
workspace = "/tmp/coder"
description = "Old description"

[[routes]]
channel = "cli"
agent = "coder"
`

func newT016AgentsServer(t *testing.T, raw string) (*Server, *testConfigStore, *http.Cookie, string) {
	t.Helper()
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	token := authTokenFor(t, store.home)
	s := newAuthServer(t, store.home, io.Discard)
	s.SetConfigStore(store)
	cookie, csrf := loginAndSession(t, s, token)
	return s, store, cookie, csrf
}

func t016AgentsList(t *testing.T, s *Server, cookie *http.Cookie) listAgentsResponse {
	t.Helper()
	rr := serveAuth(s, settingsRequest(http.MethodGet, "/v1/agents", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/agents status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var list listAgentsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("GET /v1/agents body %q: %v", rr.Body.String(), err)
	}
	return list
}

func t016FindAgentRow(t *testing.T, list listAgentsResponse, name string) agentJSON {
	t.Helper()
	for _, row := range list.Agents {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("agent %q is not in %+v", name, list.Agents)
	return agentJSON{}
}

func t016ReadAgentTables(t *testing.T, path string) map[string]map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := parseAgentTables(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return tables
}

// TestAgentsCreateDuplicateAndNameCoversACWEB048 covers AC-WEB-048: create
// writes the table and AGENT.md, a bad name is 422, duplicate copies only the
// table and AGENT.md, and an existing name is 409 invalid_state.
func TestAgentsCreateDuplicateAndNameCoversACWEB048(t *testing.T) {
	s, store, cookie, csrf := newT016AgentsServer(t, t016AgentsConfig)

	coder := agenthome.Paths(store.home, "coder")
	if err := os.MkdirAll(coder.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	coderPersona := []byte("You are the coder.\n")
	if err := os.WriteFile(coder.Agent, coderPersona, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(coder.Memory, []byte("old memory\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create reviewer from the worker template.
	rr := serveAuth(s, settingsRequest(http.MethodPost, "/v1/agents",
		`{"name":"reviewer","template":"worker","workspace":"/tmp/reviewer","permission":"trust","description":"Reviews changes"}`, cookie, csrf))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var created agentCreatedResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Name != "reviewer" || !created.RestartRequired {
		t.Fatalf("create response = %+v", created)
	}

	tables := t016ReadAgentTables(t, store.path)
	reviewer, ok := tables["reviewer"]
	if !ok {
		t.Fatalf("config has no agents.reviewer: %+v", tables)
	}
	if reviewer["permission"] != "trust" || reviewer["workspace"] != "/tmp/reviewer" || reviewer["description"] != "Reviews changes" {
		t.Fatalf("agents.reviewer = %+v", reviewer)
	}
	reviewerPath := agenthome.Paths(store.home, "reviewer")
	persona, err := os.ReadFile(reviewerPath.Agent)
	if err != nil {
		t.Fatalf("AGENT.md: %v", err)
	}
	if len(persona) == 0 {
		t.Fatal("AGENT.md is empty")
	}
	if _, err := os.Stat(reviewerPath.Memory); !os.IsNotExist(err) {
		t.Fatalf("create wrote MEMORY.md: %v", err)
	}

	list := t016AgentsList(t, s, cookie)
	row := t016FindAgentRow(t, list, "reviewer")
	if row.Description != "Reviews changes" || row.Provider != "mock" || row.Permission != "trust" || row.Workspace != "/tmp/reviewer" {
		t.Fatalf("reviewer list row = %+v", row)
	}
	if row.Default {
		t.Fatal("reviewer must not be the default agent")
	}

	// A name outside [a-z0-9-]+ is 422 validation_failed.
	rr = serveAuth(s, settingsRequest(http.MethodPost, "/v1/agents", `{"name":"Reviewer!","template":"assistant"}`, cookie, csrf))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad name status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if envelope := decodeError(t, &httptestResponse{code: rr.Code, body: rr.Body.Bytes()}); envelope.Error.Code != "validation_failed" {
		t.Fatalf("bad name error = %+v", envelope.Error)
	}

	// Duplicate coder as coder-2: same table, same AGENT.md, no MEMORY.md.
	rr = serveAuth(s, settingsRequest(http.MethodPost, "/v1/agents/coder/duplicate", `{"name":"coder-2"}`, cookie, csrf))
	if rr.Code != http.StatusCreated {
		t.Fatalf("duplicate status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var duplicated agentCreatedResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &duplicated); err != nil {
		t.Fatal(err)
	}
	if duplicated.Name != "coder-2" {
		t.Fatalf("duplicate response = %+v", duplicated)
	}
	tables = t016ReadAgentTables(t, store.path)
	if !reflect.DeepEqual(tables["coder"], tables["coder-2"]) {
		t.Fatalf("duplicate table = %+v, want %+v", tables["coder-2"], tables["coder"])
	}
	coder2 := agenthome.Paths(store.home, "coder-2")
	copied, err := os.ReadFile(coder2.Agent)
	if err != nil {
		t.Fatalf("duplicate AGENT.md: %v", err)
	}
	if !bytes.Equal(copied, coderPersona) {
		t.Fatalf("duplicate AGENT.md = %q, want %q", copied, coderPersona)
	}
	if _, err := os.Stat(coder2.Memory); !os.IsNotExist(err) {
		t.Fatalf("duplicate wrote MEMORY.md: %v", err)
	}

	// Existing names are 409 invalid_state for both create and duplicate.
	rr = serveAuth(s, settingsRequest(http.MethodPost, "/v1/agents", `{"name":"coder","template":"assistant"}`, cookie, csrf))
	if rr.Code != http.StatusConflict {
		t.Fatalf("create existing status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if envelope := decodeError(t, &httptestResponse{code: rr.Code, body: rr.Body.Bytes()}); envelope.Error.Code != "invalid_state" {
		t.Fatalf("create existing error = %+v", envelope.Error)
	}
	rr = serveAuth(s, settingsRequest(http.MethodPost, "/v1/agents/coder/duplicate", `{"name":"coder"}`, cookie, csrf))
	if rr.Code != http.StatusConflict {
		t.Fatalf("duplicate existing status = %d, body = %q", rr.Code, rr.Body.String())
	}

	// Unknown source agents are 404 not_found.
	rr = serveAuth(s, settingsRequest(http.MethodPost, "/v1/agents/missing/duplicate", `{"name":"copy"}`, cookie, csrf))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("duplicate missing status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

// TestAgentsEditAndListCoversACWEB050 covers the config part of AC-WEB-050: the
// edit form writes an agent table subset and the list exposes the rev 2 fields.
func TestAgentsEditAndListCoversACWEB050(t *testing.T) {
	s, store, cookie, csrf := newT016AgentsServer(t, t016AgentsConfig)

	backend := newFakeWebBackend()
	backend.addSession(core.SessionInfo{ID: id.New(), Channel: "web", Address: "owner", Agent: "coder", UpdatedAt: 5000})
	s.SetBackend(backend)

	list := t016AgentsList(t, s, cookie)
	row := t016FindAgentRow(t, list, "coder")
	if !row.Default || row.Provider != "mock" || row.Permission != "ask" || row.Workspace != "/tmp/coder" {
		t.Fatalf("initial coder row = %+v", row)
	}
	if row.Description != "Old description" || row.ToolsCount != 2 || row.SessionsCount != 1 {
		t.Fatalf("initial coder fields = %+v", row)
	}
	if row.LastActiveAt == nil || *row.LastActiveAt != 5000 {
		t.Fatalf("last_active_at = %v, want 5000", row.LastActiveAt)
	}
	if row.DisabledTools == nil || len(row.DisabledTools) != 0 {
		t.Fatalf("disabled_tools = %+v, want an empty array", row.DisabledTools)
	}

	payload, err := json.Marshal(map[string]any{
		"expected_version": shaHex([]byte(t016AgentsConfig)),
		"description":      "Builds and fixes things",
		"disabled_tools":   []string{"bash"},
		"workspace":        "/tmp/coder-2",
		"permission":       "trust",
		"max_steps":        7,
		"rules":            map[string]any{"allow": []string{"read_file"}, "deny": []string{"bash"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/agents/coder", string(payload), cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("save status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var saved putAgentResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Name != "coder" || saved.Version == "" || !saved.RestartRequired {
		t.Fatalf("save response = %+v", saved)
	}

	raw, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version != webfiles.ConfigVersion(raw) {
		t.Fatalf("version = %q, want hash of the saved file", saved.Version)
	}
	tables := t016ReadAgentTables(t, store.path)
	record := agentRecordFromMap(tables["coder"])
	if record.Description != "Builds and fixes things" || record.Workspace != "/tmp/coder-2" || record.Permission != "trust" {
		t.Fatalf("saved record = %+v", record)
	}
	if record.MaxSteps != 7 || !reflect.DeepEqual(record.DisabledTools, []string{"bash"}) {
		t.Fatalf("saved limits = %+v", record)
	}
	if !reflect.DeepEqual(record.Rules.Allow, []string{"read_file"}) || !reflect.DeepEqual(record.Rules.Deny, []string{"bash"}) {
		t.Fatalf("saved rules = %+v", record.Rules)
	}
	if !reflect.DeepEqual(record.Tools, []string{"read_file", "bash"}) || record.Model != "coder" {
		t.Fatalf("unspecified fields were not preserved: %+v", record)
	}

	list = t016AgentsList(t, s, cookie)
	row = t016FindAgentRow(t, list, "coder")
	if row.Description != "Builds and fixes things" || row.Workspace != "/tmp/coder-2" || row.Permission != "trust" {
		t.Fatalf("saved coder row = %+v", row)
	}
	if !reflect.DeepEqual(row.DisabledTools, []string{"bash"}) || row.ToolsCount != 2 {
		t.Fatalf("saved coder list fields = %+v", row)
	}

	// The old version is stale after the save.
	stale, err := json.Marshal(map[string]any{
		"expected_version": shaHex([]byte(t016AgentsConfig)),
		"description":      "stale",
	})
	if err != nil {
		t.Fatal(err)
	}
	rr = serveAuth(s, settingsRequest(http.MethodPut, "/v1/agents/coder", string(stale), cookie, csrf))
	if rr.Code != http.StatusConflict {
		t.Fatalf("stale status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if envelope := decodeError(t, &httptestResponse{code: rr.Code, body: rr.Body.Bytes()}); envelope.Error.Code != "stale_version" {
		t.Fatalf("stale error = %+v", envelope.Error)
	}

	// A description over 120 characters names agents.coder.description.
	tooLong, err := json.Marshal(map[string]any{
		"expected_version": webfiles.ConfigVersion(raw),
		"description":      strings.Repeat("x", 121),
	})
	if err != nil {
		t.Fatal(err)
	}
	rr = serveAuth(s, settingsRequest(http.MethodPut, "/v1/agents/coder", string(tooLong), cookie, csrf))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("long description status = %d, body = %q", rr.Code, rr.Body.String())
	}
	envelope := decodeError(t, &httptestResponse{code: rr.Code, body: rr.Body.Bytes()})
	if envelope.Error.Code != "validation_failed" || len(envelope.Error.Details) == 0 {
		t.Fatalf("long description error = %+v", envelope.Error)
	}
	if key, _ := envelope.Error.Details[0]["key"].(string); key != "agents.coder.description" {
		t.Fatalf("detail key = %q, want agents.coder.description", key)
	}

	// Unknown keys are 422 validation_failed.
	unknown, err := json.Marshal(map[string]any{
		"expected_version": webfiles.ConfigVersion(raw),
		"bogus":            true,
	})
	if err != nil {
		t.Fatal(err)
	}
	rr = serveAuth(s, settingsRequest(http.MethodPut, "/v1/agents/coder", string(unknown), cookie, csrf))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown key status = %d, body = %q", rr.Code, rr.Body.String())
	}

	// An unknown agent is 404 not_found.
	missing, err := json.Marshal(map[string]any{
		"expected_version": webfiles.ConfigVersion(raw),
		"description":      "missing",
	})
	if err != nil {
		t.Fatal(err)
	}
	rr = serveAuth(s, settingsRequest(http.MethodPut, "/v1/agents/missing", string(missing), cookie, csrf))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing agent status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

// TestAgentsDefaultFollowsCLIRoute covers API-WEB-012: the default row is the
// agent of the first cli route, then the agent named default, then no row.
func TestAgentsDefaultFollowsCLIRoute(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "first cli route wins",
			raw: `version = 1

[agents.alpha]
model = "a"

[agents.beta]
model = "b"

[agents.gamma]
model = "c"

[[routes]]
channel = "library"
agent = "alpha"

[[routes]]
channel = "cli"
agent = "beta"

[[routes]]
channel = "cli"
agent = "gamma"
`,
			want: "beta",
		},
		{
			name: "default agent without cli route",
			raw: `version = 1

[agents.default]
model = "a"

[agents.other]
model = "b"

[[routes]]
channel = "telegram"
agent = "other"
`,
			want: "default",
		},
		{
			name: "no cli route and no default agent",
			raw: `version = 1

[agents.alpha]
model = "a"

[agents.beta]
model = "b"
`,
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, cookie, _ := newT016AgentsServer(t, tc.raw)
			list := t016AgentsList(t, s, cookie)
			defaults := []string{}
			for _, row := range list.Agents {
				if row.Default {
					defaults = append(defaults, row.Name)
				}
			}
			if tc.want == "" {
				if len(defaults) != 0 {
					t.Fatalf("default rows = %v, want none", defaults)
				}
				return
			}
			if !reflect.DeepEqual(defaults, []string{tc.want}) {
				t.Fatalf("default rows = %v, want [%s]", defaults, tc.want)
			}
		})
	}
}

// TestAgentsDefaultNeverTeam covers API-TEAM-006: a team row is never default,
// even when the cli route names it or it is called default.
func TestAgentsDefaultNeverTeam(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		team string
	}{
		{
			name: "cli route points at a team",
			raw: `version = 1

[agents.lead]
model = "m"

[[routes]]
channel = "cli"
agent = "squad"
`,
			team: "squad",
		},
		{
			name: "team named default is not the fallback",
			raw: `version = 1

[agents.lead]
model = "m"
`,
			team: "default",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, cookie, _ := newT016AgentsServer(t, tc.raw)
			backend := newFakeWebBackend()
			backend.agents = []Agent{{Name: tc.team, Kind: "team", Lead: "lead", Members: []string{"lead"}, Policy: "ask"}}
			s.SetBackend(backend)

			list := t016AgentsList(t, s, cookie)
			team := t016FindAgentRow(t, list, tc.team)
			if team.Default {
				t.Fatalf("team row %s is default", tc.team)
			}
			if team.ToolsCount != 0 {
				t.Fatalf("team row tools_count = %d, want 0", team.ToolsCount)
			}
			for _, row := range list.Agents {
				if row.Default {
					t.Fatalf("row %s is default, want none", row.Name)
				}
			}
		})
	}
}

// TestAgentsToolsCountUsesCatalog covers REQ-WEB-047: tools_count counts the
// enabled catalog entries, even when the agent has no tools key.
func TestAgentsToolsCountUsesCatalog(t *testing.T) {
	raw := `version = 1

[agents.coder]
model = "m"
`
	s, _, cookie, _ := newT016AgentsServer(t, raw)
	backend := &testToolCatalogBackend{
		fakeWebBackend: newFakeWebBackend(),
		catalog: func(agent string) ([]ToolGroup, error) {
			if agent != "coder" {
				return nil, ErrUnknownAgent
			}
			return []ToolGroup{
				{Tools: []ToolEntry{
					{Name: "read_file", Enabled: true},
					{Name: "write_file", Enabled: true},
				}},
				{Tools: []ToolEntry{{Name: "bash", Enabled: false}}},
				{Tools: []ToolEntry{{Name: "web_fetch", Enabled: true}}},
			}, nil
		},
	}
	s.SetBackend(backend)

	list := t016AgentsList(t, s, cookie)
	row := t016FindAgentRow(t, list, "coder")
	if row.ToolsCount != 3 {
		t.Fatalf("tools_count = %d, want 3 enabled catalog entries", row.ToolsCount)
	}
}

// TestAgentsToolsCountFallsBack covers REQ-WEB-047: an unavailable catalog falls
// back to the configured tools list.
func TestAgentsToolsCountFallsBack(t *testing.T) {
	raw := `version = 1

[agents.coder]
model = "m"
tools = ["read_file", "bash"]
`
	cases := []struct {
		name    string
		backend Backend
	}{
		{
			name: "catalog error",
			backend: &testToolCatalogBackend{
				fakeWebBackend: newFakeWebBackend(),
				catalog: func(string) ([]ToolGroup, error) {
					return nil, errors.New("boom")
				},
			},
		},
		{
			name:    "no catalog port",
			backend: newFakeWebBackend(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, cookie, _ := newT016AgentsServer(t, raw)
			s.SetBackend(tc.backend)
			list := t016AgentsList(t, s, cookie)
			row := t016FindAgentRow(t, list, "coder")
			if row.ToolsCount != 2 {
				t.Fatalf("tools_count = %d, want 2 configured tools", row.ToolsCount)
			}
		})
	}
}

// TestAgentsMaxStepsRange covers REQ-LTC-040: the agent save accepts any
// max_steps >= 0 (0 or absent means no limit) with no upper bound, and rejects
// a negative value.
func TestAgentsMaxStepsRange(t *testing.T) {
	s, store, cookie, csrf := newT016AgentsServer(t, t016AgentsConfig)

	save := func(t *testing.T, steps int) (int, []byte) {
		t.Helper()
		raw, err := os.ReadFile(store.path)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(map[string]any{
			"expected_version": webfiles.ConfigVersion(raw),
			"max_steps":        steps,
		})
		if err != nil {
			t.Fatal(err)
		}
		rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/agents/coder", string(payload), cookie, csrf))
		return rr.Code, rr.Body.Bytes()
	}

	for _, steps := range []int{0, 1, 300} {
		code, body := save(t, steps)
		if code != http.StatusOK {
			t.Fatalf("max_steps=%d status = %d, body = %q", steps, code, body)
		}
		tables := t016ReadAgentTables(t, store.path)
		if got := agentRecordFromMap(tables["coder"]).MaxSteps; got != steps {
			t.Fatalf("saved max_steps = %d, want %d", got, steps)
		}
	}

	code, body := save(t, -1)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("max_steps=-1 status = %d, body = %q", code, body)
	}
	envelope := decodeError(t, &httptestResponse{code: code, body: body})
	if envelope.Error.Code != "validation_failed" || len(envelope.Error.Details) == 0 {
		t.Fatalf("max_steps=-1 error = %+v", envelope.Error)
	}
	if message, _ := envelope.Error.Details[0]["message"].(string); message != "max_steps must be 0 or greater" {
		t.Fatalf("max_steps=-1 detail = %+v", envelope.Error.Details[0])
	}
}

// TestAgentsMemoryCaptureRoundTrip covers the memory_capture field of
// REQ-MEM-010: an absent key reads as true, and both values round-trip through
// the agent save.
func TestAgentsMemoryCaptureRoundTrip(t *testing.T) {
	s, store, cookie, csrf := newT016AgentsServer(t, t016AgentsConfig)

	list := t016AgentsList(t, s, cookie)
	if row := t016FindAgentRow(t, list, "coder"); !row.MemoryCapture {
		t.Fatalf("absent memory_capture = %v, want true", row.MemoryCapture)
	}

	save := func(t *testing.T, value bool) {
		t.Helper()
		raw, err := os.ReadFile(store.path)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(map[string]any{
			"expected_version": webfiles.ConfigVersion(raw),
			"memory_capture":   value,
		})
		if err != nil {
			t.Fatal(err)
		}
		rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/agents/coder", string(payload), cookie, csrf))
		if rr.Code != http.StatusOK {
			t.Fatalf("save memory_capture=%v status = %d, body = %q", value, rr.Code, rr.Body.String())
		}
		tables := t016ReadAgentTables(t, store.path)
		if got := tables["coder"]["memory_capture"]; got != value {
			t.Fatalf("saved memory_capture = %v (%T), want %v", got, got, value)
		}
		list := t016AgentsList(t, s, cookie)
		if row := t016FindAgentRow(t, list, "coder"); row.MemoryCapture != value {
			t.Fatalf("listed memory_capture = %v, want %v", row.MemoryCapture, value)
		}
	}

	save(t, false)
	save(t, true)
}

// TestAgentsWorkspaceOnlyRoundTrip covers the workspace_only field: an absent
// key reads as false, a true value round-trips through the agent save, and a
// false save removes the key.
func TestAgentsWorkspaceOnlyRoundTrip(t *testing.T) {
	s, store, cookie, csrf := newT016AgentsServer(t, t016AgentsConfig)

	list := t016AgentsList(t, s, cookie)
	if row := t016FindAgentRow(t, list, "coder"); row.WorkspaceOnly {
		t.Fatalf("absent workspace_only = %v, want false", row.WorkspaceOnly)
	}

	save := func(t *testing.T, value bool) {
		t.Helper()
		raw, err := os.ReadFile(store.path)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(map[string]any{
			"expected_version": webfiles.ConfigVersion(raw),
			"workspace_only":   value,
		})
		if err != nil {
			t.Fatal(err)
		}
		rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/agents/coder", string(payload), cookie, csrf))
		if rr.Code != http.StatusOK {
			t.Fatalf("save workspace_only=%v status = %d, body = %q", value, rr.Code, rr.Body.String())
		}
		list := t016AgentsList(t, s, cookie)
		if row := t016FindAgentRow(t, list, "coder"); row.WorkspaceOnly != value {
			t.Fatalf("listed workspace_only = %v, want %v", row.WorkspaceOnly, value)
		}
	}

	save(t, true)
	save(t, false)
	if got := t016ReadAgentTables(t, store.path)["coder"]["workspace_only"]; got != nil {
		t.Fatalf("workspace_only key after false save = %v, want removed", got)
	}
}

// TestAgentsReasoningEffort covers REQ-MDL-001 and REQ-MDL-020: the effort
// round-trips through the agent API and the live config, and an unknown value
// is rejected like the other agent fields.
func TestAgentsReasoningEffort(t *testing.T) {
	s, store, cookie, csrf := newT016AgentsServer(t, t016AgentsConfig)

	save := func(t *testing.T, value string) (int, []byte) {
		t.Helper()
		raw, err := os.ReadFile(store.path)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(map[string]any{
			"expected_version": webfiles.ConfigVersion(raw),
			"reasoning_effort": value,
		})
		if err != nil {
			t.Fatal(err)
		}
		rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/agents/coder", string(payload), cookie, csrf))
		return rr.Code, rr.Body.Bytes()
	}

	for _, value := range []string{"off", "low", "high", "max", ""} {
		code, body := save(t, value)
		if code != http.StatusOK {
			t.Fatalf("reasoning_effort=%q status = %d, body = %q", value, code, body)
		}
		list := t016AgentsList(t, s, cookie)
		row := t016FindAgentRow(t, list, "coder")
		if row.ReasoningEffort != value {
			t.Fatalf("read reasoning_effort = %q, want %q", row.ReasoningEffort, value)
		}
		tables := t016ReadAgentTables(t, store.path)
		if got := agentRecordFromMap(tables["coder"]).ReasoningEffort; got != value {
			t.Fatalf("saved reasoning_effort = %q, want %q", got, value)
		}
	}

	code, body := save(t, "turbo")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid reasoning_effort status = %d, body = %q", code, body)
	}
	envelope := decodeError(t, &httptestResponse{code: code, body: body})
	if envelope.Error.Code != "validation_failed" || len(envelope.Error.Details) == 0 {
		t.Fatalf("invalid reasoning_effort error = %+v", envelope.Error)
	}
	if key, _ := envelope.Error.Details[0]["key"].(string); key != "agents.coder.reasoning_effort" {
		t.Fatalf("detail key = %q, want agents.coder.reasoning_effort", key)
	}
}

// TestAgentsVisionFlag covers API-ATT-005: PUT accepts a vision boolean and the
// list returns it.
func TestAgentsVisionFlag(t *testing.T) {
	s, store, cookie, csrf := newT016AgentsServer(t, t016AgentsConfig)

	save := func(t *testing.T, vision bool) {
		t.Helper()
		raw, err := os.ReadFile(store.path)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(map[string]any{
			"expected_version": webfiles.ConfigVersion(raw),
			"vision":           vision,
		})
		if err != nil {
			t.Fatal(err)
		}
		rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/agents/coder", string(payload), cookie, csrf))
		if rr.Code != http.StatusOK {
			t.Fatalf("save status = %d, body = %q", rr.Code, rr.Body.String())
		}
	}

	for _, want := range []bool{true, false} {
		save(t, want)
		record := agentRecordFromMap(t016ReadAgentTables(t, store.path)["coder"])
		if record.Vision == nil || *record.Vision != want {
			t.Fatalf("saved vision = %v, want %v", record.Vision, want)
		}
		row := t016FindAgentRow(t, t016AgentsList(t, s, cookie), "coder")
		if row.Vision == nil || *row.Vision != want {
			t.Fatalf("listed vision = %v, want %v", row.Vision, want)
		}
	}
}

// TestAgentsTitleAndColor covers REQ-ROOM-009 and API-ROOM-007: title and color
// round-trip through the agent save, an empty value removes the key, the list
// resolves a derived color, and an invalid value is a field error with the
// config unchanged.
func TestAgentsTitleAndColor(t *testing.T) {
	s, store, cookie, csrf := newT016AgentsServer(t, t016AgentsConfig)

	derived := core.ResolveAgentColor("coder", "")
	row := t016FindAgentRow(t, t016AgentsList(t, s, cookie), "coder")
	if row.Title != "" || row.Color != derived || row.Color == "" {
		t.Fatalf("initial title/color = %q/%q, want \"\"/%q", row.Title, row.Color, derived)
	}

	save := func(t *testing.T, body map[string]any) (int, []byte) {
		t.Helper()
		raw, err := os.ReadFile(store.path)
		if err != nil {
			t.Fatal(err)
		}
		body["expected_version"] = webfiles.ConfigVersion(raw)
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/agents/coder", string(payload), cookie, csrf))
		return rr.Code, rr.Body.Bytes()
	}

	code, body := save(t, map[string]any{"title": "Finance", "color": "success"})
	if code != http.StatusOK {
		t.Fatalf("save status = %d, body = %q", code, body)
	}
	record := agentRecordFromMap(t016ReadAgentTables(t, store.path)["coder"])
	if record.Title != "Finance" || record.Color != "success" {
		t.Fatalf("saved title/color = %q/%q", record.Title, record.Color)
	}
	row = t016FindAgentRow(t, t016AgentsList(t, s, cookie), "coder")
	if row.Title != "Finance" || row.Color != "success" {
		t.Fatalf("listed title/color = %q/%q", row.Title, row.Color)
	}

	// An empty string removes the key.
	code, body = save(t, map[string]any{"title": "", "color": ""})
	if code != http.StatusOK {
		t.Fatalf("clear status = %d, body = %q", code, body)
	}
	table := t016ReadAgentTables(t, store.path)["coder"]
	if _, ok := table["title"]; ok {
		t.Fatalf("title key still present: %+v", table)
	}
	if _, ok := table["color"]; ok {
		t.Fatalf("color key still present: %+v", table)
	}
	row = t016FindAgentRow(t, t016AgentsList(t, s, cookie), "coder")
	if row.Title != "" || row.Color != derived {
		t.Fatalf("cleared title/color = %q/%q", row.Title, row.Color)
	}

	// Invalid values are field errors and leave the config unchanged.
	before, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	code, body = save(t, map[string]any{"color": "green"})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid color status = %d, body = %q", code, body)
	}
	envelope := decodeError(t, &httptestResponse{code: code, body: body})
	if envelope.Error.Code != "validation_failed" || len(envelope.Error.Details) == 0 {
		t.Fatalf("invalid color error = %+v", envelope.Error)
	}
	if key, _ := envelope.Error.Details[0]["key"].(string); key != "agents.coder.color" {
		t.Fatalf("invalid color key = %q", key)
	}
	code, body = save(t, map[string]any{"title": strings.Repeat("x", 61)})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("long title status = %d, body = %q", code, body)
	}
	envelope = decodeError(t, &httptestResponse{code: code, body: body})
	if envelope.Error.Code != "validation_failed" || len(envelope.Error.Details) == 0 {
		t.Fatalf("long title error = %+v", envelope.Error)
	}
	if key, _ := envelope.Error.Details[0]["key"].(string); key != "agents.coder.title" {
		t.Fatalf("long title key = %q", key)
	}
	after, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("invalid title/color changed the config")
	}
}

// TestAgentsTeamRowTitleAndColor covers API-ROOM-007: a team row shows the team
// description as its title and the brand color.
func TestAgentsTeamRowTitleAndColor(t *testing.T) {
	raw := `version = 1

[agents.lead]
model = "m"
`
	s, _, cookie, _ := newT016AgentsServer(t, raw)
	backend := newFakeWebBackend()
	backend.agents = []Agent{{Name: "squad", Kind: "team", Lead: "lead", Members: []string{"lead"}, Policy: "ask", Description: "Career plan"}}
	s.SetBackend(backend)

	team := t016FindAgentRow(t, t016AgentsList(t, s, cookie), "squad")
	if team.Title != "Career plan" || team.Color != "brand" {
		t.Fatalf("team title/color = %q/%q", team.Title, team.Color)
	}
}
