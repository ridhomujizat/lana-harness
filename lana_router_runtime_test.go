package lana

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/router"
	"github.com/ridhomujizat/lana-harness/router/openaiproxy"
)

// proxyRuntimeEnv is a lana runtime whose Claude Code accounts (built-in and
// cc-2) and Antigravity accounts (built-in and agy-2) run the fake CLIs,
// with the OpenAI-compatible proxy over its router.
type proxyRuntimeEnv struct {
	rt        *Runtime
	home      string
	srv       *httptest.Server
	claudeLog string
	agyLog    string
}

func newProxyRuntimeEnv(t *testing.T) *proxyRuntimeEnv {
	t.Helper()
	claudeBin := buildFakeClaude(t)
	agyBin := buildFakeAgy(t)
	e := &proxyRuntimeEnv{home: t.TempDir()}
	e.claudeLog = filepath.Join(t.TempDir(), "claude.jsonl")
	e.agyLog = filepath.Join(t.TempDir(), "agy.jsonl")
	t.Setenv("FAKECLAUDE_LOG", e.claudeLog)
	t.Setenv("FAKEAGY_RECORD", e.agyLog)
	t.Setenv("FAKECLAUDE_PROMPT_FIXTURE", "")
	t.Setenv("FAKEAGY_PROMPT", "")
	cfg := fmt.Sprintf(`
[providers.claude-code]
kind = "claude-code"
binary = %[1]q

[providers.cc-2]
kind = "claude-code"
binary = %[1]q

[providers.antigravity]
kind = "antigravity"
binary = %[2]q

[providers.agy-2]
kind = "antigravity"
binary = %[2]q
`, claudeBin, agyBin)
	rt, err := New(context.Background(), Options{Home: e.home, Store: openRouterTestStore(t), Layers: []config.Layer{{Name: "test", TOML: []byte(cfg)}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	e.rt = rt
	auth := func(bearer string) (string, bool) { return "k1", bearer == "secret" }
	e.srv = httptest.NewServer(http.StripPrefix("/router/v1", openaiproxy.NewHandler(rt.router, auth)))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *proxyRuntimeEnv) do(t *testing.T, ctx context.Context, method, path string, body any) *http.Response {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, e.srv.URL+"/router/v1"+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// complete posts a non-streaming call and returns the reply text, the usage,
// and the X-Lana-Served-By header.
func (e *proxyRuntimeEnv) complete(t *testing.T, body map[string]any) (string, map[string]any, string) {
	t.Helper()
	resp := e.do(t, context.Background(), http.MethodPost, "/chat/completions", body)
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%v: status %d: %s", body["model"], resp.StatusCode, raw)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage map[string]any `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) != 1 {
		t.Fatalf("reply %s: %v", raw, err)
	}
	return out.Choices[0].Message.Content, out.Usage, resp.Header.Get(openaiproxy.HeaderServedBy)
}

// stream posts a streaming call with include_usage and returns the text and
// the final usage.
func (e *proxyRuntimeEnv) stream(t *testing.T, body map[string]any) (string, map[string]any) {
	t.Helper()
	body["stream"] = true
	body["stream_options"] = map[string]any{"include_usage": true}
	resp := e.do(t, context.Background(), http.MethodPost, "/chat/completions", body)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("%v: status %d: %s", body["model"], resp.StatusCode, raw)
	}
	var text strings.Builder
	var usage map[string]any
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		var ch struct {
			Choices []struct {
				Delta struct {
					Content *string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage map[string]any `json:"usage"`
			Error any            `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &ch); err != nil || ch.Error != nil {
			t.Fatalf("chunk %s: %v", data, err)
		}
		if ch.Usage != nil {
			usage = ch.Usage
		}
		for _, c := range ch.Choices {
			if c.Delta.Content != nil {
				text.WriteString(*c.Delta.Content)
			}
		}
	}
	return text.String(), usage
}

// fakeLines returns the lines of a fake CLI log of one kind.
func fakeLines(t *testing.T, path, kind string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range strings.Split(string(data), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil && m["kind"] == kind {
			out = append(out, m)
		}
	}
	return out
}

func argvOf(m map[string]any) []string {
	raw, _ := m["argv"].([]any)
	out := make([]string, len(raw))
	for i, v := range raw {
		out[i], _ = v.(string)
	}
	return out
}

func envOf(m map[string]any) []string {
	raw, _ := m["env"].([]any)
	out := make([]string, len(raw))
	for i, v := range raw {
		out[i], _ = v.(string)
	}
	return out
}

// checkWorkDir checks a prompt run's cwd: a new folder under
// <LANA_HOME>/router/proxy-work/ that was empty at start and is gone now.
func (e *proxyRuntimeEnv) checkWorkDir(t *testing.T, m map[string]any) {
	t.Helper()
	cwd, _ := m["cwd"].(string)
	base, err := filepath.EvalSymlinks(filepath.Join(e.home, "router", "proxy-work"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(cwd) != base && filepath.Dir(cwd) != filepath.Join(e.home, "router", "proxy-work") {
		t.Fatalf("cwd %q is not in %q", cwd, base)
	}
	if n, _ := m["cwd_entries"].(float64); n != 0 {
		t.Fatalf("cwd had %v entries", n)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(cwd); os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("work dir %q is still there", cwd)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func usageIs(u map[string]any, prompt, completion, cached float64) bool {
	details, _ := u["prompt_tokens_details"].(map[string]any)
	return u["prompt_tokens"] == prompt && u["completion_tokens"] == completion &&
		u["total_tokens"] == prompt+completion && details["cached_tokens"] == cached
}

// AC-RPR-001, AC-RPR-002 with the fake CLIs: GET /models lists the Claude
// Code and Antigravity accounts; a non-streaming and a streaming call to each
// runtime answer with the fake CLI's text and usage; the fake saw the flags
// of REQ-RPR-002, the prompt on stdin, the account env, and an empty work
// dir that is gone afterwards.
func TestProxyRuntimeCLIs(t *testing.T) {
	e := newProxyRuntimeEnv(t)
	e.rt.router.SetRuntimeModels(AntigravityRuntime, []core.RuntimeModel{{Value: "gemini-4-neon-low"}, {Value: "gemini-4-neon-high"}})
	e.rt.router.SetRuntimeModels("agy-2", []core.RuntimeModel{{Value: "gemini-4-neon-high"}})

	resp := e.do(t, context.Background(), http.MethodGet, "/models", nil)
	var list struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	owners := map[string]string{}
	for _, m := range list.Data {
		owners[m.ID] = m.OwnedBy
	}
	for id, want := range map[string]string{
		"claude-code/sonnet": "claude-code", "cc-2/opus": "claude-code",
		"antigravity/gemini-4-neon": "antigravity", "agy-2/gemini-4-neon": "antigravity",
	} {
		if owners[id] != want {
			t.Fatalf("%s owned by %q: %+v", id, owners[id], list.Data)
		}
	}

	// Claude Code, built-in account, non-streaming.
	text, usage, served := e.complete(t, map[string]any{
		"model": "claude-code/sonnet", "reasoning_effort": "high",
		"messages": []any{map[string]any{"role": "system", "content": "Be brief."}, map[string]any{"role": "user", "content": "Hi"}},
	})
	if text != "Hello from fake" || served != "claude-code/sonnet" || !usageIs(usage, 14, 4, 3) {
		t.Fatalf("claude reply %q usage %+v served %q", text, usage, served)
	}
	prompts := fakeLines(t, e.claudeLog, "prompt")
	if len(prompts) != 1 {
		t.Fatalf("claude prompt runs = %+v", prompts)
	}
	wantArgv := []string{"-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--model", "sonnet",
		"--tools", "", "--strict-mcp-config", "--setting-sources", "project,local", "--no-session-persistence", "--system-prompt", "Be brief.", "--effort", "high"}
	if got := argvOf(prompts[0]); !slices.Equal(got, wantArgv) {
		t.Fatalf("claude argv = %q", got)
	}
	if prompts[0]["stdin"] != "Hi" {
		t.Fatalf("claude stdin = %q", prompts[0]["stdin"])
	}
	e.checkWorkDir(t, prompts[0])

	// Claude Code, account cc-2, streaming: its own CLAUDE_CONFIG_DIR.
	text, usage = e.stream(t, map[string]any{"model": "cc-2/haiku", "messages": []any{map[string]any{"role": "user", "content": "Yo"}}})
	if text != "Hello from fake" || !usageIs(usage, 14, 4, 3) {
		t.Fatalf("cc-2 stream %q usage %+v", text, usage)
	}
	prompts = fakeLines(t, e.claudeLog, "prompt")
	configDir := filepath.Join(e.home, "claude", "cc-2")
	env := envOf(prompts[1])
	if !slices.Contains(env, "CLAUDE_CONFIG_DIR="+configDir) || !slices.Contains(env, "CLAUDE_SECURESTORAGE_CONFIG_DIR="+configDir) ||
		slices.Contains(argvOf(prompts[1]), "--effort") || slices.Contains(argvOf(prompts[1]), "--system-prompt") {
		t.Fatalf("cc-2 run argv %q env %q", argvOf(prompts[1]), env)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			t.Fatalf("cc-2 env has %s", kv)
		}
	}
	e.checkWorkDir(t, prompts[1])

	// Antigravity, built-in account, non-streaming: the system text leads the
	// prompt.
	text, usage, _ = e.complete(t, map[string]any{
		"model": "antigravity/gemini-4-neon", "reasoning_effort": "high",
		"messages": []any{map[string]any{"role": "system", "content": "Be brief."}, map[string]any{"role": "user", "content": "Hi"}},
	})
	if text != "Hi from agy" || !usageIs(usage, 24, 5, 4) {
		t.Fatalf("agy reply %q usage %+v", text, usage)
	}
	agyPrompts := fakeLines(t, e.agyLog, "prompt")
	if len(agyPrompts) != 1 {
		t.Fatalf("agy prompt runs = %+v", agyPrompts)
	}
	if got := argvOf(agyPrompts[0]); !slices.Equal(got, []string{"--input-format", "stream-json", "--output-format", "stream-json", "--disable-slash-commands", "--model", "gemini-4-neon", "--sandbox", "--effort", "high"}) {
		t.Fatalf("agy argv = %q", got)
	}
	if agyPrompts[0]["stdin"] != "System instructions:\nBe brief.\n\nHi" {
		t.Fatalf("agy stdin = %q", agyPrompts[0]["stdin"])
	}
	e.checkWorkDir(t, agyPrompts[0])

	// Antigravity, account agy-2, streaming: its own HOME and the file-token
	// setting of added accounts.
	text, usage = e.stream(t, map[string]any{"model": "agy-2/gemini-4-neon", "messages": []any{map[string]any{"role": "user", "content": "Yo"}}})
	if text != "Hi from agy" || !usageIs(usage, 24, 5, 4) {
		t.Fatalf("agy-2 stream %q usage %+v", text, usage)
	}
	agyPrompts = fakeLines(t, e.agyLog, "prompt")
	env = envOf(agyPrompts[1])
	if !slices.Contains(env, "HOME="+filepath.Join(e.home, "antigravity", "agy-2")) || !slices.Contains(env, "AGY_CLI_DISABLE_AUTO_UPDATE=true") {
		t.Fatalf("agy-2 env = %q", env)
	}
	if os.Getenv("SSH_CONNECTION") == "" && !slices.Contains(env, "SSH_CONNECTION=127.0.0.1 0 127.0.0.1 0") {
		t.Fatalf("agy-2 env has no file-token setting: %q", env)
	}
	for _, run := range agyPrompts {
		if slices.Contains(argvOf(run), "--dangerously-skip-permissions") {
			t.Fatalf("agy argv = %q", argvOf(run))
		}
	}
	e.checkWorkDir(t, agyPrompts[1])
}

// AC-RPR-004 with the fake CLI: a rejected plan window cools the built-in
// account until its reset and the pooled address answers from cc-2.
func TestProxyRuntimeCLILimit(t *testing.T) {
	e := newProxyRuntimeEnv(t)
	reset := time.Now().Add(2 * time.Hour).Unix()
	rejected := filepath.Join(t.TempDir(), "rejected.jsonl")
	writeTestFile(t, rejected, fmt.Sprintf(`{"type":"system","subtype":"init","session_id":"00000000-0000-4000-8000-000000000001","model":"claude-opus-5-5"}
{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":%d,"rateLimitType":"five_hour"}}
{"fake":"hang"}
`, reset))
	t.Setenv("FAKECLAUDE_PROMPT_FIXTURE", rejected)
	// cc-2 answers from its own fixture file.
	ccDir := filepath.Join(e.home, "claude", "cc-2")
	if err := os.MkdirAll(ccDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(ccDir, "fakeclaude_prompt.jsonl"), `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"from cc-2"}}}
{"type":"result","subtype":"success","is_error":false,"result":"from cc-2","usage":{"input_tokens":1,"output_tokens":1}}
`)
	text, _, served := e.complete(t, map[string]any{"model": "claude-code/opus", "messages": []any{map[string]any{"role": "user", "content": "Hi"}}})
	if text != "from cc-2" || served != "cc-2/opus" {
		t.Fatalf("reply %q served by %q", text, served)
	}
	until, ok := e.rt.router.Cooling()[router.Target{Provider: router.ClaudeCode, Model: "opus"}]
	if !ok || until.Unix() < reset-5 || until.Unix() > reset+5 {
		t.Fatalf("cooling = %+v, want until %d", e.rt.router.Cooling(), reset)
	}
}

// AC-RPR-005: a client that goes away kills the fake CLI's process group and
// the work dir is removed.
func TestProxyRuntimeCLIDisconnect(t *testing.T) {
	e := newProxyRuntimeEnv(t)
	hang := filepath.Join(t.TempDir(), "hang.jsonl")
	writeTestFile(t, hang, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"partial"}}}
{"fake":"hang"}
`)
	t.Setenv("FAKECLAUDE_PROMPT_FIXTURE", hang)
	ctx, cancel := context.WithCancel(context.Background())
	resp := e.do(t, ctx, http.MethodPost, "/chat/completions", map[string]any{
		"model": "cc-2/sonnet", "stream": true, "messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if strings.Contains(sc.Text(), "partial") {
			break
		}
	}
	prompts := fakeLines(t, e.claudeLog, "prompt")
	if len(prompts) != 1 {
		t.Fatalf("prompt runs = %+v", prompts)
	}
	pid := int(prompts[0]["pid"].(float64))
	cancel()
	_ = resp.Body.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake claude %d still runs after the client went away", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.checkWorkDir(t, prompts[0])
}
