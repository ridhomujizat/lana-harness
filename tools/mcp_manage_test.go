package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/mcpconfig"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// mcpTestModule is a placeholder for the built-in modules the config base
// layer names. config.Check validates the module registry, and the tools test
// binary only links the modules under test.
type mcpTestModule struct{ name string }

func (m mcpTestModule) ID() string                                     { return m.name }
func (m mcpTestModule) Requires() []core.ServiceKey                    { return nil }
func (m mcpTestModule) Provides() []core.ServiceKey                    { return nil }
func (m mcpTestModule) Install(*core.Builder, core.ModuleConfig) error { return nil }
func (m mcpTestModule) Stop(context.Context) error                     { return nil }

func TestMain(m *testing.M) {
	for _, name := range []string{"llm", "mcp", "sqlite_store", "agent", "compaction"} {
		if !mcpModuleRegistered(name) {
			core.RegisterModule(name, func() core.Module { return mcpTestModule{name: name} })
		}
	}
	os.Exit(m.Run())
}

func mcpModuleRegistered(name string) bool {
	for _, registered := range core.ModuleNames() {
		if registered == name {
			return true
		}
	}
	return false
}

type fakeMCPPut struct {
	name string
	cfg  core.MCPConfig
}

type fakeAgentServers struct {
	agent   string
	servers []string
}

type fakeMCPAdmin struct {
	tools    int
	testErr  error
	status   map[string]core.MCPServerStatus
	tested   []core.MCPConfig
	puts     []fakeMCPPut
	removes  []string
	agentSet []fakeAgentServers
}

func (f *fakeMCPAdmin) TestServer(_ context.Context, cfg core.MCPConfig) (int, error) {
	f.tested = append(f.tested, cfg)
	return f.tools, f.testErr
}

func (f *fakeMCPAdmin) PutServer(name string, cfg core.MCPConfig) {
	f.puts = append(f.puts, fakeMCPPut{name: name, cfg: cfg})
}

func (f *fakeMCPAdmin) RemoveServer(name string) {
	f.removes = append(f.removes, name)
}

func (f *fakeMCPAdmin) SetAgentServers(agent string, servers []string) {
	f.agentSet = append(f.agentSet, fakeAgentServers{agent: agent, servers: append([]string(nil), servers...)})
}

func (f *fakeMCPAdmin) ServerStatus(name string) (string, int, string, bool) {
	status, ok := f.status[name]
	if !ok {
		return "", 0, "", false
	}
	return status.State, status.Tools, status.Reason, true
}

func writeMCPManageConfig(t *testing.T, home, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newMCPManageHome(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	writeMCPManageConfig(t, home, content)
	return home
}

func readMCPManageConfig(t *testing.T, home string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mcpManageDocument(t *testing.T, home string) mcpconfig.Document {
	t.Helper()
	doc, err := mcpconfig.Parse(readMCPManageConfig(t, home))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func callMCPManage(t *testing.T, tool core.Tool, tc core.ToolCtx, input string) core.ToolOutput {
	t.Helper()
	out, err := tool.Call(context.Background(), tc, json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mcpManageAudit(t *testing.T, home string) []webfiles.AuditEntry {
	t.Helper()
	page, err := webfiles.ReadAudit(webfiles.AuditPath(home), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	return page.Entries
}

const mcpManageBaseConfig = `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.lana]
provider = "mock"
model = "lana-model"

[agents.other]
provider = "mock"
model = "other-model"
`

// TestMCPManageListHidesValues covers AC-AMCP-003: list shows key names but
// never env or header values, resolved or raw.
func TestMCPManageListHidesValues(t *testing.T) {
	t.Setenv("AMCP_GITHUB_TOKEN", "ghp-super-secret")
	home := newMCPManageHome(t, `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[mcp.github]
command = "github-mcp"
env = { GITHUB_TOKEN = "${AMCP_GITHUB_TOKEN}" }

[mcp.remote]
url = "https://example.com/mcp"
headers = { Authorization = "Bearer header-secret" }

[agents.lana]
provider = "mock"
model = "lana-model"
mcp = ["github"]
`)
	tool := NewMCPManage()
	out := callMCPManage(t, tool, core.ToolCtx{Home: home, AgentName: "lana"}, `{"action":"list"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("list = %+v", out)
	}
	want := "github  stdio  github-mcp  attached  -  env=GITHUB_TOKEN  headers=-\n" +
		"remote  http  https://example.com/mcp  available  -  env=-  headers=Authorization"
	if out.Output != want {
		t.Fatalf("list output =\n%s\nwant\n%s", out.Output, want)
	}
	for _, secret := range []string{"ghp-super-secret", "Bearer header-secret", "${AMCP_GITHUB_TOKEN}"} {
		if strings.Contains(out.Output, secret) {
			t.Fatalf("list output leaks %q: %s", secret, out.Output)
		}
	}
}

// TestMCPManageListState covers AC-TFB-016 and AC-TFB-017: the state column
// shows the runtime state of every configured server, including one the calling
// agent has not attached.
func TestMCPManageListState(t *testing.T) {
	home := newMCPManageHome(t, `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[mcp.dbx]
command = "dbx"

[mcp.broken]
command = "broken"

[mcp.detached]
command = "detached"

[agents.lana]
provider = "mock"
model = "lana-model"
mcp = ["dbx", "broken"]
`)
	admin := &fakeMCPAdmin{status: map[string]core.MCPServerStatus{
		"dbx":      {Name: "dbx", State: "running", Tools: 25},
		"broken":   {Name: "broken", State: "unavailable", Reason: "spawn ENOENT"},
		"detached": {Name: "detached", State: "running", Tools: 3},
	}}
	tool := NewMCPManage()
	out := callMCPManage(t, tool, core.ToolCtx{Home: home, AgentName: "lana", MCPAdmin: admin}, `{"action":"list"}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("list = %+v", out)
	}
	lines := strings.Split(out.Output, "\n")
	if len(lines) != 3 {
		t.Fatalf("list lines = %q", out.Output)
	}
	byName := map[string]string{}
	for _, line := range lines {
		byName[strings.Fields(line)[0]] = line
	}
	if line := byName["dbx"]; !strings.Contains(line, "running (25 tools)") {
		t.Fatalf("dbx line = %q", line)
	}
	if line := byName["broken"]; !strings.Contains(line, "unavailable: spawn ENOENT") {
		t.Fatalf("broken line = %q", line)
	}
	if line := byName["detached"]; !strings.Contains(line, "running (3 tools)") {
		t.Fatalf("detached line = %q", line)
	}
}

// TestMCPManageListChildStatus covers the child case: the list action has no
// MCPAdmin but still reads the runtime state from the read-only source.
func TestMCPManageListChildStatus(t *testing.T) {
	home := newMCPManageHome(t, `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[mcp.dbx]
command = "dbx"

[agents.lana]
provider = "mock"
model = "lana-model"
mcp = ["dbx"]
`)
	status := func(name string) (string, int, string, bool) {
		if name != "dbx" {
			return "", 0, "", false
		}
		return "running", 25, "", true
	}
	tool := NewMCPManage()
	out := callMCPManage(t, tool, core.ToolCtx{Home: home, AgentName: "lana", MCPStatus: status}, `{"action":"list"}`)
	if out.Status != core.ToolStatusSuccess || !strings.Contains(out.Output, "running (25 tools)") {
		t.Fatalf("list = %+v", out)
	}
}

// TestMCPManageListNotLoaded covers a server the runtime does not know.
func TestMCPManageListNotLoaded(t *testing.T) {
	home := newMCPManageHome(t, `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[mcp.dbx]
command = "dbx"

[agents.lana]
provider = "mock"
model = "lana-model"
`)
	tool := NewMCPManage()
	out := callMCPManage(t, tool, core.ToolCtx{Home: home, AgentName: "lana", MCPAdmin: &fakeMCPAdmin{}}, `{"action":"list"}`)
	if out.Status != core.ToolStatusSuccess || !strings.Contains(out.Output, "not loaded") {
		t.Fatalf("list = %+v", out)
	}
}

// TestMCPManageAddNewServer covers AC-AMCP-004 and AC-AMCP-017.
func TestMCPManageAddNewServer(t *testing.T) {
	home := newMCPManageHome(t, mcpManageBaseConfig)
	admin := &fakeMCPAdmin{tools: 3}
	tc := core.ToolCtx{Home: home, AgentName: "lana", MCPAdmin: admin}
	tool := NewMCPManage()

	out := callMCPManage(t, tool, tc, `{"action":"add","name":"fs","command":"npx","args":["-y","server-fs","/notes"]}`)
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("add = %+v", out)
	}
	want := "added mcp server fs for agent lana (3 tools). Its tools are available from the next turn."
	if out.Output != want {
		t.Fatalf("add output = %q, want %q", out.Output, want)
	}

	doc := mcpManageDocument(t, home)
	def := mcpconfig.FromMap(doc.MCP["fs"])
	if def.Command != "npx" || strings.Join(def.Args, ",") != "-y,server-fs,/notes" {
		t.Fatalf("mcp.fs = %+v", def)
	}
	if got := strings.Join(mcpconfig.AgentList(doc, "lana"), ","); got != "fs" {
		t.Fatalf("lana mcp = %q", got)
	}
	if len(admin.tested) != 1 {
		t.Fatalf("TestServer calls = %d, want 1", len(admin.tested))
	}
	if len(admin.puts) != 1 || admin.puts[0].name != "fs" {
		t.Fatalf("PutServer calls = %+v", admin.puts)
	}
	if len(admin.agentSet) != 1 || admin.agentSet[0].agent != "lana" || strings.Join(admin.agentSet[0].servers, ",") != "fs" {
		t.Fatalf("SetAgentServers calls = %+v", admin.agentSet)
	}

	foundSave := false
	for _, entry := range mcpManageAudit(t, home) {
		if entry.Kind == "mcp.save" && entry.Target == "fs" {
			foundSave = true
		}
	}
	if !foundSave {
		t.Fatalf("audit entries lack mcp.save for fs: %+v", mcpManageAudit(t, home))
	}
}

// TestMCPManageTestFailureLeavesConfig covers AC-AMCP-005.
func TestMCPManageTestFailureLeavesConfig(t *testing.T) {
	home := newMCPManageHome(t, mcpManageBaseConfig)
	before := readMCPManageConfig(t, home)
	admin := &fakeMCPAdmin{testErr: errors.New("server did not start")}
	tc := core.ToolCtx{Home: home, AgentName: "lana", MCPAdmin: admin}
	tool := NewMCPManage()

	out := callMCPManage(t, tool, tc, `{"action":"add","name":"fs","command":"npx"}`)
	if out.Status != core.ToolStatusError || out.Output != "connection test failed: server did not start" {
		t.Fatalf("add = %+v", out)
	}
	if got := readMCPManageConfig(t, home); string(got) != string(before) {
		t.Fatalf("config.toml changed:\n%s", got)
	}
	if len(admin.puts) != 0 || len(admin.agentSet) != 0 {
		t.Fatalf("in-memory changes after failure: puts=%+v agents=%+v", admin.puts, admin.agentSet)
	}
}

// TestMCPManageRedactsResolvedValues keeps a resolved secret out of a
// connection-test error.
func TestMCPManageRedactsResolvedValues(t *testing.T) {
	t.Setenv("AMCP_RESOLVED_SECRET", "resolved-secret-value")
	home := newMCPManageHome(t, mcpManageBaseConfig)
	admin := &fakeMCPAdmin{testErr: errors.New("connect with resolved-secret-value failed")}
	tc := core.ToolCtx{Home: home, AgentName: "lana", MCPAdmin: admin}
	tool := NewMCPManage()

	out := callMCPManage(t, tool, tc, `{"action":"add","name":"gh","command":"gh-mcp","env":{"TOKEN":"${AMCP_RESOLVED_SECRET}"}}`)
	if out.Status != core.ToolStatusError || out.Output != "connection test failed: connect with *** failed" {
		t.Fatalf("add = %+v", out)
	}
	if strings.Contains(out.Output, "resolved-secret-value") {
		t.Fatalf("output leaks resolved secret: %s", out.Output)
	}
}

// TestMCPManageConflictWithOtherAgent covers AC-AMCP-006.
func TestMCPManageConflictWithOtherAgent(t *testing.T) {
	home := newMCPManageHome(t, `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[mcp.db]
command = "old-db"

[agents.lana]
provider = "mock"
model = "lana-model"
mcp = ["db"]

[agents.other]
provider = "mock"
model = "other-model"
mcp = ["db"]
`)
	before := readMCPManageConfig(t, home)
	admin := &fakeMCPAdmin{tools: 1}
	tc := core.ToolCtx{Home: home, AgentName: "lana", MCPAdmin: admin}
	tool := NewMCPManage()

	out := callMCPManage(t, tool, tc, `{"action":"add","name":"db","command":"new-db"}`)
	if out.Status != core.ToolStatusError || out.Output != "conflict: mcp server db is used by other" {
		t.Fatalf("add = %+v", out)
	}
	if got := readMCPManageConfig(t, home); string(got) != string(before) {
		t.Fatalf("config.toml changed:\n%s", got)
	}
	if len(admin.tested) != 0 {
		t.Fatalf("TestServer calls = %d, want 0", len(admin.tested))
	}
}

// TestMCPManageAttachOnly covers AC-AMCP-007.
func TestMCPManageAttachOnly(t *testing.T) {
	home := newMCPManageHome(t, `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[mcp.db]
command = "db-server"

[agents.lana]
provider = "mock"
model = "lana-model"
mcp = []

[agents.other]
provider = "mock"
model = "other-model"
mcp = ["db"]
`)
	admin := &fakeMCPAdmin{tools: 9}
	tc := core.ToolCtx{Home: home, AgentName: "lana", MCPAdmin: admin}
	tool := NewMCPManage()

	out := callMCPManage(t, tool, tc, `{"action":"add","name":"db"}`)
	want := "attached mcp server db to agent lana. Its tools are available from the next turn."
	if out.Status != core.ToolStatusSuccess || out.Output != want {
		t.Fatalf("add = %+v, want %q", out, want)
	}
	doc := mcpManageDocument(t, home)
	if got := strings.Join(mcpconfig.AgentList(doc, "lana"), ","); got != "db" {
		t.Fatalf("lana mcp = %q", got)
	}
	if got := mcpconfig.FromMap(doc.MCP["db"]).Command; got != "db-server" {
		t.Fatalf("mcp.db command = %q", got)
	}
	if len(admin.tested) != 0 {
		t.Fatalf("TestServer calls = %d, want 0", len(admin.tested))
	}
	if len(admin.puts) != 0 {
		t.Fatalf("PutServer calls = %+v, want none", admin.puts)
	}
	if len(admin.agentSet) != 1 || strings.Join(admin.agentSet[0].servers, ",") != "db" {
		t.Fatalf("SetAgentServers calls = %+v", admin.agentSet)
	}
	for _, entry := range mcpManageAudit(t, home) {
		if entry.Kind == "mcp.save" {
			t.Fatalf("attach-only wrote an mcp.save entry: %+v", entry)
		}
	}
}

// TestMCPManageAttachOnlyIdenticalDefinition covers the same-definition
// attach path: the table is not rewritten and no connection test runs.
func TestMCPManageAttachOnlyIdenticalDefinition(t *testing.T) {
	home := newMCPManageHome(t, `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[mcp.db]
command = "db-server"
args = ["--port", "0"]

[agents.lana]
provider = "mock"
model = "lana-model"
mcp = []
`)
	admin := &fakeMCPAdmin{tools: 4}
	tc := core.ToolCtx{Home: home, AgentName: "lana", MCPAdmin: admin}
	tool := NewMCPManage()

	out := callMCPManage(t, tool, tc, `{"action":"add","name":"db","command":"db-server","args":["--port","0"]}`)
	want := "attached mcp server db to agent lana. Its tools are available from the next turn."
	if out.Status != core.ToolStatusSuccess || out.Output != want {
		t.Fatalf("add = %+v, want %q", out, want)
	}
	if len(admin.tested) != 0 || len(admin.puts) != 0 {
		t.Fatalf("attach-only reached the admin: tested=%d puts=%d", len(admin.tested), len(admin.puts))
	}
	if len(admin.agentSet) != 1 || strings.Join(admin.agentSet[0].servers, ",") != "db" {
		t.Fatalf("SetAgentServers calls = %+v", admin.agentSet)
	}

	before := readMCPManageConfig(t, home)
	out = callMCPManage(t, tool, tc, `{"action":"add","name":"db","command":"db-server","args":["--port","0"]}`)
	if out.Status != core.ToolStatusSuccess || out.Output != "mcp server db is already attached to agent lana" {
		t.Fatalf("already attached = %+v", out)
	}
	if got := readMCPManageConfig(t, home); string(got) != string(before) {
		t.Fatalf("already attached changed config.toml:\n%s", got)
	}
	if len(admin.tested) != 0 {
		t.Fatalf("TestServer calls = %d, want 0", len(admin.tested))
	}
}

// TestMCPManageRemoveDeletesUnusedTable covers AC-AMCP-008.
func TestMCPManageRemoveDeletesUnusedTable(t *testing.T) {
	home := newMCPManageHome(t, `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[mcp.fs]
command = "fs-server"

[agents.lana]
provider = "mock"
model = "lana-model"
mcp = ["fs"]

[agents.other]
provider = "mock"
model = "other-model"
mcp = []
`)
	admin := &fakeMCPAdmin{}
	tc := core.ToolCtx{Home: home, AgentName: "lana", MCPAdmin: admin}
	tool := NewMCPManage()

	out := callMCPManage(t, tool, tc, `{"action":"remove","name":"fs"}`)
	want := "removed mcp server fs from agent lana; deleted [mcp.fs]"
	if out.Status != core.ToolStatusSuccess || out.Output != want {
		t.Fatalf("remove = %+v, want %q", out, want)
	}
	doc := mcpManageDocument(t, home)
	if _, ok := doc.MCP["fs"]; ok {
		t.Fatal("mcp.fs table still exists")
	}
	if got := strings.Join(mcpconfig.AgentList(doc, "lana"), ","); got != "" {
		t.Fatalf("lana mcp = %q", got)
	}
	if len(admin.removes) != 1 || admin.removes[0] != "fs" {
		t.Fatalf("RemoveServer calls = %+v", admin.removes)
	}
	if len(admin.agentSet) != 1 || len(admin.agentSet[0].servers) != 0 {
		t.Fatalf("SetAgentServers calls = %+v", admin.agentSet)
	}
	foundDelete := false
	for _, entry := range mcpManageAudit(t, home) {
		if entry.Kind == "mcp.delete" && entry.Target == "fs" {
			foundDelete = true
		}
	}
	if !foundDelete {
		t.Fatalf("audit entries lack mcp.delete for fs: %+v", mcpManageAudit(t, home))
	}
}

// TestMCPManageRemoveKeepsTableUsedByOther covers AC-AMCP-009.
func TestMCPManageRemoveKeepsTableUsedByOther(t *testing.T) {
	home := newMCPManageHome(t, `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[mcp.fs]
command = "fs-server"

[agents.lana]
provider = "mock"
model = "lana-model"
mcp = ["fs"]

[agents.other]
provider = "mock"
model = "other-model"
mcp = ["fs"]
`)
	admin := &fakeMCPAdmin{}
	tc := core.ToolCtx{Home: home, AgentName: "lana", MCPAdmin: admin}
	tool := NewMCPManage()

	out := callMCPManage(t, tool, tc, `{"action":"remove","name":"fs"}`)
	if out.Status != core.ToolStatusSuccess || out.Output != "removed mcp server fs from agent lana" {
		t.Fatalf("remove = %+v", out)
	}
	doc := mcpManageDocument(t, home)
	if _, ok := doc.MCP["fs"]; !ok {
		t.Fatal("mcp.fs table was deleted while another agent uses it")
	}
	if got := strings.Join(mcpconfig.AgentList(doc, "lana"), ","); got != "" {
		t.Fatalf("lana mcp = %q", got)
	}
	if got := strings.Join(mcpconfig.AgentList(doc, "other"), ","); got != "fs" {
		t.Fatalf("other mcp = %q", got)
	}
	if len(admin.removes) != 0 {
		t.Fatalf("RemoveServer calls = %+v, want none", admin.removes)
	}
	for _, entry := range mcpManageAudit(t, home) {
		if entry.Kind == "mcp.delete" {
			t.Fatalf("audit wrote mcp.delete while the table stays: %+v", entry)
		}
	}
}

// TestMCPManageMissingSecret covers AC-AMCP-016.
func TestMCPManageMissingSecret(t *testing.T) {
	t.Setenv("AMCP_MISSING_SECRET", "")
	home := newMCPManageHome(t, mcpManageBaseConfig)
	before := readMCPManageConfig(t, home)
	admin := &fakeMCPAdmin{tools: 1}
	tc := core.ToolCtx{Home: home, AgentName: "lana", MCPAdmin: admin}
	tool := NewMCPManage()

	out := callMCPManage(t, tool, tc, `{"action":"add","name":"gh","command":"gh-mcp","env":{"TOKEN":"${AMCP_MISSING_SECRET}"}}`)
	want := "secret AMCP_MISSING_SECRET is not set; ask the owner to add it in Settings → Secrets"
	if out.Status != core.ToolStatusError || out.Output != want {
		t.Fatalf("add = %+v, want %q", out, want)
	}
	if got := readMCPManageConfig(t, home); string(got) != string(before) {
		t.Fatalf("config.toml changed:\n%s", got)
	}
	if len(admin.tested) != 0 {
		t.Fatalf("TestServer calls = %d, want 0", len(admin.tested))
	}
}

// TestMCPManageInvalidCandidateLeavesFile covers AC-AMCP-018.
func TestMCPManageInvalidCandidateLeavesFile(t *testing.T) {
	home := newMCPManageHome(t, `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.lana]
provider = "missing"
model = "lana-model"
`)
	before := readMCPManageConfig(t, home)
	admin := &fakeMCPAdmin{tools: 1}
	tc := core.ToolCtx{Home: home, AgentName: "lana", MCPAdmin: admin}
	tool := NewMCPManage()

	out := callMCPManage(t, tool, tc, `{"action":"add","name":"fs","command":"npx"}`)
	if out.Status != core.ToolStatusError || !strings.HasPrefix(out.Output, "config.toml is not valid: ") {
		t.Fatalf("add = %+v", out)
	}
	if got := readMCPManageConfig(t, home); string(got) != string(before) {
		t.Fatalf("config.toml changed:\n%s", got)
	}
	if len(admin.tested) != 0 {
		t.Fatalf("TestServer calls = %d, want 0", len(admin.tested))
	}
}

func TestMCPManageRisk(t *testing.T) {
	tool := NewMCPManage()
	tests := []struct {
		input string
		want  core.Risk
	}{
		{`{"action":"list"}`, core.RiskRead},
		{`{"action":"add","name":"fs","command":"npx"}`, core.RiskAdmin},
		{`{"action":"remove","name":"fs"}`, core.RiskAdmin},
		{`{"action":"LIST"}`, core.RiskAdmin},
		{`{}`, core.RiskAdmin},
		{`not json`, core.RiskAdmin},
	}
	for _, tt := range tests {
		if got := tool.Risk(json.RawMessage(tt.input)); got != tt.want {
			t.Fatalf("Risk(%s) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMCPManageInputValidation(t *testing.T) {
	home := newMCPManageHome(t, mcpManageBaseConfig)
	admin := &fakeMCPAdmin{tools: 1}
	tc := core.ToolCtx{Home: home, AgentName: "lana", MCPAdmin: admin}
	tool := NewMCPManage()

	longCommand := strings.Repeat("a", mcpCommandMax+1)
	badName := strings.Repeat("a", mcpNameMax+1)
	tooManyArgsList := make([]string, mcpArgsMax+1)
	for i := range tooManyArgsList {
		tooManyArgsList[i] = "a"
	}
	tooManyArgs, _ := json.Marshal(tooManyArgsList)
	tooManyEnv := map[string]string{}
	for i := 0; i < mcpEntriesMax+1; i++ {
		tooManyEnv[fmt.Sprintf("KEY_%d", i)] = "v"
	}
	tooManyEnvJSON, _ := json.Marshal(tooManyEnv)
	tooManyHeaders := map[string]string{}
	for i := 0; i < mcpEntriesMax+1; i++ {
		tooManyHeaders[fmt.Sprintf("X-Key-%d", i)] = "v"
	}
	tooManyHeadersJSON, _ := json.Marshal(tooManyHeaders)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"unknown field", `{"action":"list","bogus":true}`, "invalid input: unknown field bogus"},
		{"unknown action", `{"action":"rename","name":"x"}`, "invalid input: "},
		{"missing name", `{"action":"add","command":"npx"}`, "invalid input: "},
		{"bad name", `{"action":"add","name":"Bad_Name","command":"npx"}`, "invalid input: "},
		{"long name", fmt.Sprintf(`{"action":"add","name":%q,"command":"npx"}`, badName), "invalid input: "},
		{"command and url", `{"action":"add","name":"x","command":"npx","url":"https://example.com/mcp"}`, "invalid input: "},
		{"args without command", `{"action":"add","name":"x","args":["-y"]}`, "invalid input: "},
		{"env without command", `{"action":"add","name":"x","env":{"TOKEN":"v"}}`, "invalid input: "},
		{"headers without url", `{"action":"add","name":"x","headers":{"Authorization":"v"}}`, "invalid input: "},
		{"bad url scheme", `{"action":"add","name":"x","url":"ftp://example.com/mcp"}`, "invalid input: "},
		{"url without host", `{"action":"add","name":"x","url":"https://"}`, "invalid input: "},
		{"long command", fmt.Sprintf(`{"action":"add","name":"x","command":%q}`, longCommand), "invalid input: "},
		{"too many args", fmt.Sprintf(`{"action":"add","name":"x","command":"npx","args":%s}`, tooManyArgs), "invalid input: "},
		{"too many env", fmt.Sprintf(`{"action":"add","name":"x","command":"npx","env":%s}`, tooManyEnvJSON), "invalid input: "},
		{"too many headers", fmt.Sprintf(`{"action":"add","name":"x","url":"https://example.com/mcp","headers":%s}`, tooManyHeadersJSON), "invalid input: "},
		{"bad env key", `{"action":"add","name":"x","command":"npx","env":{"9BAD":"v"}}`, "invalid input: "},
		{"multiline command", `{"action":"add","name":"x","command":"npx\nrm"}`, "invalid input: "},
		{"multiline arg", `{"action":"add","name":"x","command":"npx","args":["a\nb"]}`, "invalid input: "},
		{"multiline env value", `{"action":"add","name":"x","command":"npx","env":{"TOKEN":"a\nb"}}`, "invalid input: "},
		{"multiline header value", `{"action":"add","name":"x","url":"https://example.com/mcp","headers":{"Authorization":"a\nb"}}`, "invalid input: "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := callMCPManage(t, tool, tc, tt.input)
			if out.Status != core.ToolStatusError || !strings.HasPrefix(out.Output, tt.want) {
				t.Fatalf("output = %+v, want prefix %q", out, tt.want)
			}
		})
	}
	if len(admin.tested) != 0 || len(admin.puts) != 0 {
		t.Fatalf("validation reached the admin: tested=%d puts=%d", len(admin.tested), len(admin.puts))
	}
}

func TestMCPManageGuards(t *testing.T) {
	tool := NewMCPManage()

	out := callMCPManage(t, tool, core.ToolCtx{AgentName: "lana"}, `{"action":"list"}`)
	if out.Status != core.ToolStatusError || out.Output != "mcp_manage is not available: no home folder" {
		t.Fatalf("no home = %+v", out)
	}

	home := newMCPManageHome(t, mcpManageBaseConfig)
	out = callMCPManage(t, tool, core.ToolCtx{Home: home, AgentName: "lana"}, `{"action":"add","name":"fs","command":"npx"}`)
	if out.Status != core.ToolStatusError || out.Output != "mcp_manage is not available in this runtime" {
		t.Fatalf("nil admin add = %+v", out)
	}
	out = callMCPManage(t, tool, core.ToolCtx{Home: home, AgentName: "lana"}, `{"action":"remove","name":"fs"}`)
	if out.Status != core.ToolStatusError || out.Output != "mcp_manage is not available in this runtime" {
		t.Fatalf("nil admin remove = %+v", out)
	}
	out = callMCPManage(t, tool, core.ToolCtx{Home: home, AgentName: "lana"}, `{"action":"list"}`)
	if out.Status != core.ToolStatusSuccess || out.Output != "no MCP servers configured" {
		t.Fatalf("nil admin list = %+v", out)
	}

	missing := t.TempDir()
	out = callMCPManage(t, tool, core.ToolCtx{Home: missing, AgentName: "lana"}, `{"action":"list"}`)
	if out.Status != core.ToolStatusError || out.Output != "config.toml does not exist" {
		t.Fatalf("missing config list = %+v", out)
	}
	out = callMCPManage(t, tool, core.ToolCtx{Home: missing, AgentName: "lana", MCPAdmin: &fakeMCPAdmin{}}, `{"action":"add","name":"fs","command":"npx"}`)
	if out.Status != core.ToolStatusError || out.Output != "config.toml does not exist" {
		t.Fatalf("missing config add = %+v", out)
	}
}

func TestMCPManageAddAttachErrors(t *testing.T) {
	home := newMCPManageHome(t, `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[mcp.db]
command = "db-server"

[agents.lana]
provider = "mock"
model = "lana-model"
mcp = ["db"]
`)
	admin := &fakeMCPAdmin{tools: 2}
	tc := core.ToolCtx{Home: home, AgentName: "lana", MCPAdmin: admin}
	tool := NewMCPManage()
	before := readMCPManageConfig(t, home)

	out := callMCPManage(t, tool, tc, `{"action":"add","name":"missing"}`)
	if out.Status != core.ToolStatusError || out.Output != "mcp server missing is not configured; give command or url" {
		t.Fatalf("unknown attach = %+v", out)
	}
	out = callMCPManage(t, tool, tc, `{"action":"add","name":"db"}`)
	if out.Status != core.ToolStatusSuccess || out.Output != "mcp server db is already attached to agent lana" {
		t.Fatalf("already attached = %+v", out)
	}
	if got := readMCPManageConfig(t, home); string(got) != string(before) {
		t.Fatalf("already attached changed config.toml:\n%s", got)
	}
	if len(admin.tested) != 0 {
		t.Fatalf("TestServer calls = %d, want 0", len(admin.tested))
	}

	out = callMCPManage(t, tool, tc, `{"action":"remove","name":"nope"}`)
	if out.Status != core.ToolStatusError || out.Output != "mcp server nope is not attached to agent lana" {
		t.Fatalf("not attached = %+v", out)
	}
}
