package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/channels/cli/tui"
	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

// captureStdout runs fn and returns what it wrote to stdout.
func captureStdout(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	code := fn()
	_ = w.Close()
	os.Stdout = old
	data, _ := io.ReadAll(r)
	return string(data), code
}

func initHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("LANA_HOME", home)
	if code := cmdInit([]string{"--yes", "--provider", "deepseek"}); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return home
}

func TestAgentNewYes(t *testing.T) {
	home := initHome(t)
	out, code := captureStdout(t, func() int { return agentNew([]string{"helper", "--template", "assistant", "--yes"}, false) })
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out, "next: lana chat --agent helper") {
		t.Fatalf("stdout = %q", out)
	}
	persona, err := os.ReadFile(filepath.Join(home, "agents", "helper", "AGENT.md"))
	if err != nil || !strings.Contains(string(persona), "You are helper") {
		t.Fatalf("AGENT.md = %q, %v", persona, err)
	}
	if info, err := os.Stat(filepath.Join(home, "agents", "helper")); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("folder mode = %v, %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(home, "agents", "helper", "USER.md")); !os.IsNotExist(err) {
		t.Fatal("agent new must not create a per-agent USER.md")
	}
	if _, err := os.Stat(filepath.Join(home, "USER.md")); !os.IsNotExist(err) {
		t.Fatal("agent new must not create the shared USER.md")
	}
	result, err := config.Load(context.Background(), config.Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	agent := result.Config.Agents["helper"]
	if agent.Provider != "" || agent.Model != "deepseek/deepseek-flash" || agent.Permission != "ask" || strings.Join(agent.Tools, ",") != "read_file,write_file,edit_file,glob,grep,bash,web_fetch,memory_read,memory_write,skill_write,ask_user,send_file" {
		t.Fatalf("agent = %+v", agent)
	}
	if result.Config.Routes[0].Agent != "default" {
		t.Fatalf("--yes must not change the cli route: %+v", result.Config.Routes)
	}
}

func TestAgentNewWorkerTemplate(t *testing.T) {
	home := initHome(t)
	if _, code := captureStdout(t, func() int { return agentNew([]string{"w1", "--template", "worker", "--yes"}, false) }); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	result, _ := config.Load(context.Background(), config.Options{Home: home})
	if got := strings.Join(result.Config.Agents["w1"].Tools, ","); got != "read_file,write_file,edit_file,glob,grep,bash,web_fetch,memory_read,memory_write,skill_write,ask_user,send_file" {
		t.Fatalf("tools = %s", got)
	}
}

func TestAgentNewFullToolTemplates(t *testing.T) {
	const wantTools = "read_file,write_file,edit_file,glob,grep,bash,web_fetch,memory_read,memory_write,skill_write,ask_user,send_file"
	for _, tc := range []struct{ name, template string }{
		{"a1", "assistant"},
		{"c1", "coder"},
	} {
		home := initHome(t)
		if _, code := captureStdout(t, func() int { return agentNew([]string{tc.name, "--template", tc.template, "--yes"}, false) }); code != 0 {
			t.Fatalf("%s exit = %d", tc.template, code)
		}
		result, err := config.Load(context.Background(), config.Options{Home: home})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(result.Config.Agents[tc.name].Tools, ","); got != wantTools {
			t.Fatalf("%s tools = %s", tc.template, got)
		}
	}
}

func TestAgentNewErrors(t *testing.T) {
	home := initHome(t)
	cases := [][]string{
		{"Helper Bot", "--yes"},
		{"default", "--yes"},
		{"x", "--template", "nope", "--yes"},
		{"x", "--model", "missing:m", "--yes"},
		{"x"},
	}
	for _, args := range cases {
		if code := agentNew(args, false); code != 2 {
			t.Fatalf("%v exit = %d, want 2", args, code)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "agents", "x")); !os.IsNotExist(err) {
		t.Fatal("failed new left a folder")
	}
}

func TestAgentNewWizard(t *testing.T) {
	home := initHome(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()
	_, _ = w.WriteString("coder\n\ntrust\ny\nn\n")
	_ = w.Close()
	if _, code := captureStdout(t, func() int { return agentNew([]string{"dev"}, true) }); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	result, err := config.Load(context.Background(), config.Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Config.Agents["dev"]; got.Permission != "trust" || got.Model != "deepseek/deepseek-flash" {
		t.Fatalf("agent = %+v", got)
	}
	if result.Config.Routes[0].Channel != "cli" || result.Config.Routes[0].Agent != "dev" {
		t.Fatalf("routes = %+v", result.Config.Routes)
	}
}

func TestAgentListShowEdit(t *testing.T) {
	home := initHome(t)
	captureStdout(t, func() int { return agentNew([]string{"helper", "--yes"}, false) })
	out, code := captureStdout(t, agentList)
	if code != 0 || !strings.Contains(out, "default  deepseek/deepseek-flash  ask  persona=prompt_file:agents/default.md") || !strings.Contains(out, "helper  deepseek/deepseek-flash  ask  persona=AGENT.md  user=0/2000  memory=0/8000") {
		t.Fatalf("list = %q", out)
	}
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "true")
	if code := agentEdit([]string{"default", "memory"}); code != 0 {
		t.Fatalf("edit exit = %d", code)
	}
	if _, err := os.Stat(filepath.Join(home, "agents", "default", "MEMORY.md")); err != nil {
		t.Fatalf("edit did not create MEMORY.md: %v", err)
	}
	if code := agentEdit([]string{"ghost"}); code != 2 {
		t.Fatalf("edit unknown exit = %d", code)
	}
	out, code = captureStdout(t, func() int { return agentShow([]string{"helper", "--prompt"}) })
	if code != 0 || !strings.HasPrefix(out, "You are helper") || !strings.Contains(out, "- channel: cli") {
		t.Fatalf("show --prompt = %q", out)
	}
}

// TestAgentShowMigratesUserCoversACHOME006 covers AC-HOME-006: lana agent show
// copies a per-agent USER.md into the shared file and leaves the source intact.
func TestAgentShowMigratesUserCoversACHOME006(t *testing.T) {
	home := initHome(t)
	if _, code := captureStdout(t, func() int { return agentNew([]string{"helper", "--yes"}, false) }); code != 0 {
		t.Fatalf("new exit = %d", code)
	}
	source := filepath.Join(home, "agents", "helper", "USER.md")
	legacy := "Name: Ridho\n§\nLanguage: Indonesian"
	if err := os.WriteFile(source, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, code := captureStdout(t, func() int { return agentShow([]string{"helper"}) }); code != 0 {
		t.Fatalf("show exit = %d", code)
	}
	shared, err := os.ReadFile(filepath.Join(home, "USER.md"))
	if err != nil {
		t.Fatalf("shared USER.md: %v", err)
	}
	if string(shared) != legacy {
		t.Fatalf("shared USER.md = %q, want %q", shared, legacy)
	}
	unchanged, err := os.ReadFile(source)
	if err != nil || string(unchanged) != legacy {
		t.Fatalf("per-agent USER.md changed: %q, %v", unchanged, err)
	}
}

// TestAgentEditMemoryCoversACAGCLI005 covers AC-AGCLI-005: editing the memory
// file creates it and exits 0.
func TestAgentEditMemoryCoversACAGCLI005(t *testing.T) {
	home := initHome(t)
	if _, code := captureStdout(t, func() int { return agentNew([]string{"helper", "--yes"}, false) }); code != 0 {
		t.Fatalf("new exit = %d", code)
	}
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "true")
	if code := agentEdit([]string{"helper", "memory"}); code != 0 {
		t.Fatalf("edit exit = %d", code)
	}
	if _, err := os.Stat(filepath.Join(home, "agents", "helper", "MEMORY.md")); err != nil {
		t.Fatalf("edit memory did not create MEMORY.md: %v", err)
	}
}

// TestConfigCheckLegacyUserWarningCoversACHOME010 covers AC-HOME-010 through
// lana config check: a remaining per-agent USER.md with entries is reported and
// the shared file is named.
func TestConfigCheckLegacyUserWarningCoversACHOME010(t *testing.T) {
	home := initHome(t)
	if _, code := captureStdout(t, func() int { return agentNew([]string{"helper", "--yes"}, false) }); code != 0 {
		t.Fatalf("new exit = %d", code)
	}
	legacy := filepath.Join(home, "agents", "helper", "USER.md")
	if err := os.WriteFile(legacy, []byte("Name: Old"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, code := captureStdout(t, func() int { return cmdConfig([]string{"check"}) })
	if code != 0 {
		t.Fatalf("check exit = %d", code)
	}
	want := "warning: agents/helper/USER.md is not read; move its entries to " + filepath.Join(home, "USER.md")
	if !strings.Contains(out, want) {
		t.Fatalf("config check lacks %q:\n%s", want, out)
	}
}

// TestAgentEditUserUsesSharedFile checks that edit user opens the shared file.
func TestAgentEditUserUsesSharedFile(t *testing.T) {
	home := initHome(t)
	if _, code := captureStdout(t, func() int { return agentNew([]string{"helper", "--yes"}, false) }); code != 0 {
		t.Fatalf("new exit = %d", code)
	}
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "true")
	if code := agentEdit([]string{"helper", "user"}); code != 0 {
		t.Fatalf("edit exit = %d", code)
	}
	if _, err := os.Stat(filepath.Join(home, "USER.md")); err != nil {
		t.Fatalf("edit user did not create the shared USER.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "agents", "helper", "USER.md")); !os.IsNotExist(err) {
		t.Fatal("edit user must not create a per-agent USER.md")
	}
}

func TestSetCLIRoute(t *testing.T) {
	in := "[agents.a]\nmodel = \"m\"\n\n[[routes]]\nchannel = \"telegram\"\nagent = \"a\"\n\n[[routes]]\nchannel = \"cli\"\nagent = \"a\"\n"
	got := setCLIRoute(in, "b")
	if !strings.Contains(got, "channel = \"telegram\"\nagent = \"a\"") || !strings.Contains(got, "channel = \"cli\"\nagent = \"b\"") {
		t.Fatalf("got:\n%s", got)
	}
	if got := setCLIRoute("[agents.a]", "b"); !strings.HasSuffix(got, "[[routes]]\nchannel = \"cli\"\nagent = \"b\"") {
		t.Fatalf("append:\n%s", got)
	}
}

func TestLeaseSetRejectsSecondHolder(t *testing.T) {
	home := initHome(t)
	ctx := context.Background()
	rt, _, _, _, err := runtimeFor(ctx, home, "default", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()
	info, err := rt.CreateSession(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	first := newLeaseSet(rt)
	if err := first.ensure(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if err := first.ensure(ctx, info.ID); err != nil {
		t.Fatalf("re-ensure by the holder: %v", err)
	}
	second := newLeaseSet(rt)
	if err := second.ensure(ctx, info.ID); err == nil || !strings.Contains(err.Error(), "is in use by process") {
		t.Fatalf("second holder err = %v", err)
	}
	first.releaseAll()
	if err := second.ensure(ctx, info.ID); err != nil {
		t.Fatalf("after release: %v", err)
	}
	second.releaseAll()
}

// lockedBuffer collects TUI output from the test goroutine.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// cliTeamBackend drives the CLI chat with a team and one delegated child.
type cliTeamBackend struct {
	obs     *core.Observe[core.LiveFrame]
	mu      sync.Mutex
	agents  []string
	setTeam bool
}

func newCLITeamBackend() *cliTeamBackend {
	return &cliTeamBackend{obs: core.NewObserve[core.LiveFrame]()}
}

func (b *cliTeamBackend) CreateSession(context.Context, string) (core.SessionInfo, error) {
	return core.SessionInfo{ID: "S2", Agent: "default"}, nil
}

func (b *cliTeamBackend) LastSession(context.Context) (core.SessionInfo, error) {
	return core.SessionInfo{ID: "S1", Agent: "default"}, nil
}

func (b *cliTeamBackend) RunTurn(_ context.Context, sessionID, _ string) (tui.TurnResult, error) {
	b.obs.Publish(core.LiveFrame{SessionID: "C1", Kind: "tool_call", CallID: "c1", Name: "read_file", Input: []byte(`{"path":"notes.md"}`)})
	b.obs.Publish(core.LiveFrame{SessionID: "C1", Kind: "tool_result", CallID: "c1", Status: "success", Output: "notes"})
	b.obs.Publish(core.LiveFrame{SessionID: sessionID, Kind: "text_delta", Text: "Lead answer."})
	return tui.TurnResult{Text: "Lead answer.", StopReason: "end_turn"}, nil
}

func (b *cliTeamBackend) Send(context.Context, string, string) error { return nil }
func (b *cliTeamBackend) Cancel(context.Context, string) error       { return nil }
func (b *cliTeamBackend) SubscribeLive(context.Context, string) (*core.Subscription[core.LiveFrame], error) {
	return b.obs.Subscribe(), nil
}
func (b *cliTeamBackend) Policy(context.Context, string) (core.Policy, error) {
	return core.PolicyAsk, nil
}
func (b *cliTeamBackend) SetPolicy(context.Context, string, core.Policy) error { return nil }
func (b *cliTeamBackend) History(context.Context, string) ([]core.SessionEvent, error) {
	return nil, nil
}
func (b *cliTeamBackend) SessionInfo(_ context.Context, id string) (core.SessionInfo, error) {
	if id == "C1" {
		return core.SessionInfo{ID: id, Agent: "researcher", ParentSessionID: "S1"}, nil
	}
	return core.SessionInfo{ID: id, Agent: "default"}, nil
}
func (b *cliTeamBackend) ListSessions(context.Context, int) ([]core.SessionInfo, error) {
	return nil, nil
}
func (b *cliTeamBackend) ForkSession(context.Context, string) (core.SessionInfo, error) {
	return core.SessionInfo{ID: "S1", Agent: "default"}, nil
}
func (b *cliTeamBackend) SetAgent(_ context.Context, _, agent string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.agents = append(b.agents, agent)
	b.setTeam = agent == "research-squad"
	return nil
}
func (b *cliTeamBackend) SetModel(context.Context, string, string) error { return nil }
func (b *cliTeamBackend) Compact(context.Context, string) (tui.CompactionInfo, error) {
	return tui.CompactionInfo{}, nil
}
func (b *cliTeamBackend) AgentNames() []string {
	return []string{"default", "assistant", "research-squad"}
}
func (b *cliTeamBackend) AgentModel(agent string) string {
	if agent == "research-squad" {
		return "mock:lead"
	}
	return "mock:mock"
}
func (b *cliTeamBackend) AgentPolicy(string) string { return "ask" }
func (b *cliTeamBackend) Memory(string) ([]string, []string, error) {
	return []string{"Name: Ridho"}, nil, nil
}
func (b *cliTeamBackend) MCPStatus(string) []core.MCPServerStatus { return nil }
func (b *cliTeamBackend) Skills(string) []agenthome.Skill         { return nil }

// TestAgentTeamChatChildToolLine covers AC-TEAM-054 on the CLI side: /agent
// accepts a team name and a child tool line carries the child agent name.
func TestAgentTeamChatChildToolLine(t *testing.T) {
	backend := newCLITeamBackend()
	reader, writer := io.Pipe()
	var out lockedBuffer
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	t.Cleanup(func() {
		cancel()
		_ = writer.Close()
	})
	done := make(chan error, 1)
	go func() {
		done <- tui.Run(ctx, tui.Options{
			Backend: backend, Agent: "default", Model: "mock:mock", Policy: "ask",
			SessionID: "S1", Cwd: "/tmp", Version: "test", Home: t.TempDir(),
			In: reader, Out: &out,
		})
	}()
	send := func(s string) {
		t.Helper()
		if _, err := writer.Write([]byte(s)); err != nil {
			t.Fatalf("send %q: %v", s, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	send("/agent research-squad\r")
	waitForAgentOutput(t, &out, "· agent: research-squad")
	send("research the notes\r")
	waitForAgentOutput(t, &out, "researcher · read_file")
	waitForAgentOutput(t, &out, "Lead answer.")
	backend.mu.Lock()
	setTeam := backend.setTeam
	backend.mu.Unlock()
	if !setTeam {
		t.Fatal("/agent did not accept the team name")
	}
	send("/exit\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("chat did not stop")
	}
}

func waitForAgentOutput(t *testing.T, buffer *lockedBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buffer.String(), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("did not find %q; output:\n%s", want, buffer.String())
}
