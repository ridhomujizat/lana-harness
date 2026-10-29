package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/config"
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
	result, err := config.Load(context.Background(), config.Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	agent := result.Config.Agents["helper"]
	if agent.Provider != "deepseek" || agent.Model != "deepseek-chat" || agent.Permission != "ask" || strings.Join(agent.Tools, ",") != "web_fetch,read_file,glob,grep,memory_read,memory_write" {
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
	if got := strings.Join(result.Config.Agents["w1"].Tools, ","); got != "read_file,write_file,edit_file,glob,grep,bash,web_fetch,memory_read,memory_write" {
		t.Fatalf("tools = %s", got)
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
	if got := result.Config.Agents["dev"]; got.Permission != "trust" || got.Model != "deepseek-chat" {
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
	if code != 0 || !strings.Contains(out, "default  deepseek:deepseek-chat  ask  persona=prompt_file:agents/default.md") || !strings.Contains(out, "helper  deepseek:deepseek-chat  ask  persona=AGENT.md  user=0/2000  memory=0/4000") {
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
