// Command fakeclaude stands in for the `claude` binary in tests.
//
// Environment:
//
//	FAKECLAUDE_LOG               file to append JSON log lines to (start argv/env/cwd, stdin lines)
//	FAKECLAUDE_FIXTURE           JSONL fixture to replay; turns are split after each "result" line
//	FAKECLAUDE_IGNORE_INTERRUPT  "1" ignores interrupt control requests
//	FAKECLAUDE_VERSION           `--version` output (default "2.1.319 (Claude Code)")
//	FAKECLAUDE_AUTH              `auth status --json` output (default logged in)
//	FAKECLAUDE_AUTH_EXIT         `auth status` exit code (default 0)
//	FAKECLAUDE_GATE              file the {"fake":"gate"} directive waits for
//	FAKECLAUDE_MODELS            JSON models array of the initialize response
//	                             (default: a recorded, scrubbed list)
//	FAKECLAUDE_LOGIN             `auth login` behaviour: "" or "ok" succeeds,
//	                             "fail" writes stderr lines (the last one with
//	                             a URL) and exits 1, "hang" waits until killed
//	FAKECLAUDE_LOGIN_MS          `auth login` sleeps this long first
//	FAKECLAUDE_LOGIN_EMAIL       e-mail the successful login reports
//	                             (default "new@example.com")
//
// `auth login` and `auth logout` log a line with kind "auth", and
// `auth status` one with kind "status" (argv and env).
// A successful login writes fakeclaude_auth.json (logged in) into
// CLAUDE_CONFIG_DIR, and a logout writes a logged-out one, so a later
// `auth status` of that account sees the change.
//
// With CLAUDE_CONFIG_DIR set (a lana Claude Code account), the files
// fakeclaude_auth.json and fakeclaude_fixture.jsonl in that folder, when
// present, win over FAKECLAUDE_AUTH and FAKECLAUDE_FIXTURE, so two accounts
// can answer differently in one test.
//
// --mcp-config takes JSON or the path of a JSON file; the start log line has
// the file's content as "mcp_config" and its permission bits as
// "mcp_config_mode".
//
// Fixture lines with a "fake" key are directives:
//
//	{"fake":"sleep","ms":100}                 sleep (interrupts still handled)
//	{"fake":"hang"}                           wait for an interrupt
//	{"fake":"gate"}                           wait until FAKECLAUDE_GATE exists
//	{"fake":"exit","code":1,"stderr":"text"}  write stderr and exit
//	{"fake":"mcp_call","server":"lana","tool":"memory_write","id":"toolu_1","args":{...}}
//	    emit an assistant tool_use for mcp__<server>__<tool>, ask can_use_tool,
//	    call the tool on the MCP server of --mcp-config (URL and bearer token
//	    from the argv), log the result, and emit the user tool_result
//	{"fake":"mcp_list","server":"lana"}       log the server's tool names
//
// The fixture session ID 00000000-0000-4000-8000-000000000001 is replaced by
// the --session-id or --resume value.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// defaultModels is the models array of a recorded Claude Code 2.1.319
// initialize response (descriptions shortened).
const defaultModels = `[{"value":"default","displayName":"Default (recommended)","description":"Use the default model"},` +
	`{"value":"opus","displayName":"Opus","description":"Opus for complex work"},` +
	`{"value":"claude-opus-5-5","displayName":"Opus 5.5","description":"Claude Opus 5.5"},` +
	`{"value":"claude-sonnet-5-5[1m]","displayName":"Sonnet 5.5 (1M context)","description":"Claude Sonnet 5.5 with 1M context"},` +
	`{"value":"claude-haiku-5-5","displayName":"Haiku 5.5","description":"Claude Haiku 5.5"}]`

// mcpConfig is the --mcp-config value: servers by name with URL and headers.
type mcpConfig struct {
	MCPServers map[string]struct {
		Type    string            `json:"type"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	} `json:"mcpServers"`
}

type headerTransport struct {
	headers map[string]string
}

func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	return http.DefaultTransport.RoundTrip(req)
}

// mcpConfigArg returns the --mcp-config JSON of argv: the value itself, or
// the content of the file it names, with the file's permission bits ("" for
// inline JSON).
func mcpConfigArg(args []string) (string, string) {
	value := ""
	for i, a := range args {
		if a == "--mcp-config" && i+1 < len(args) {
			value = args[i+1]
		}
	}
	if value == "" || strings.HasPrefix(strings.TrimSpace(value), "{") {
		return value, ""
	}
	info, err := os.Stat(value)
	if err != nil {
		return "", ""
	}
	data, err := os.ReadFile(value)
	if err != nil {
		return "", ""
	}
	return string(data), fmt.Sprintf("%#o", info.Mode().Perm())
}

// accountFile returns the path of name in CLAUDE_CONFIG_DIR when both exist.
func accountFile(name string) (string, bool) {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		return "", false
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	return path, true
}

// mcpSession connects to the named server of the --mcp-config argv.
func mcpSession(args []string, server string) (*mcp.ClientSession, error) {
	raw, _ := mcpConfigArg(args)
	if raw == "" {
		return nil, fmt.Errorf("no --mcp-config")
	}
	var cfg mcpConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, err
	}
	srv, ok := cfg.MCPServers[server]
	if !ok {
		return nil, fmt.Errorf("no MCP server %q", server)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "fakeclaude", Version: "0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: srv.URL, HTTPClient: &http.Client{Transport: headerTransport{headers: srv.Headers}},
		MaxRetries: -1, DisableStandaloneSSE: true,
	}, nil)
}

const fixtureSession = "00000000-0000-4000-8000-000000000001"

var (
	logMu   sync.Mutex
	outMu   sync.Mutex
	session = fixtureSession
)

func logLine(v map[string]any) {
	path := os.Getenv("FAKECLAUDE_LOG")
	if path == "" {
		return
	}
	logMu.Lock()
	defer logMu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	v["pid"] = os.Getpid()
	b, _ := json.Marshal(v)
	_, _ = f.Write(append(b, '\n'))
}

func out(line string) {
	outMu.Lock()
	defer outMu.Unlock()
	fmt.Fprintln(os.Stdout, strings.ReplaceAll(line, fixtureSession, session))
}

func outJSON(v any) {
	b, _ := json.Marshal(v)
	out(string(b))
}

func main() {
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--version" {
		v := os.Getenv("FAKECLAUDE_VERSION")
		if v == "" {
			v = "2.1.319 (Claude Code)"
		}
		fmt.Println(v)
		return
	}
	if len(args) >= 2 && args[0] == "auth" && (args[1] == "login" || args[1] == "logout") {
		authCommand(args)
		return
	}
	if len(args) >= 2 && args[0] == "auth" && args[1] == "status" {
		logLine(map[string]any{"kind": "status", "argv": args, "env": os.Environ()})
		a := os.Getenv("FAKECLAUDE_AUTH")
		if path, ok := accountFile("fakeclaude_auth.json"); ok {
			if data, err := os.ReadFile(path); err == nil {
				a = strings.TrimSpace(string(data))
			}
		}
		if a == "" {
			a = `{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","email":"user@example.com"}`
		}
		fmt.Println(a)
		if os.Getenv("FAKECLAUDE_AUTH_EXIT") == "1" {
			os.Exit(1)
		}
		return
	}
	cwd, _ := os.Getwd()
	mcpCfg, mcpMode := mcpConfigArg(args)
	logLine(map[string]any{"kind": "start", "argv": args, "env": os.Environ(), "cwd": cwd, "mcp_config": mcpCfg, "mcp_config_mode": mcpMode})
	for i, a := range args {
		if (a == "--session-id" || a == "--resume") && i+1 < len(args) {
			session = args[i+1]
		}
	}
	fixture := os.Getenv("FAKECLAUDE_FIXTURE")
	if path, ok := accountFile("fakeclaude_fixture.jsonl"); ok {
		fixture = path
	}
	turns := loadFixture(fixture)
	ignoreInterrupt := os.Getenv("FAKECLAUDE_IGNORE_INTERRUPT") == "1"

	stdin := make(chan map[string]any, 64)
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 1<<20), 32<<20)
		for sc.Scan() {
			line := sc.Text()
			logLine(map[string]any{"kind": "stdin", "line": json.RawMessage(line)})
			var m map[string]any
			if json.Unmarshal([]byte(line), &m) == nil {
				stdin <- m
			}
		}
		close(stdin)
	}()

	// interrupted is set by handleControl when an interrupt arrives during a turn.
	interrupted := false
	responses := map[string]map[string]any{}
	handleControl := func(m map[string]any) {
		switch m["type"] {
		case "control_request":
			req, _ := m["request"].(map[string]any)
			id, _ := m["request_id"].(string)
			switch req["subtype"] {
			case "initialize":
				models := os.Getenv("FAKECLAUDE_MODELS")
				if models == "" {
					models = defaultModels
				}
				outJSON(map[string]any{"type": "control_response", "response": map[string]any{
					"subtype": "success", "request_id": id, "response": map[string]any{
						"commands": []any{}, "models": json.RawMessage(models),
						"account": map[string]any{"email": "user@example.com", "subscriptionType": "max"}}}})
			case "interrupt":
				if ignoreInterrupt {
					return
				}
				interrupted = true
				outJSON(map[string]any{"type": "control_response", "response": map[string]any{
					"subtype": "success", "request_id": id, "response": map[string]any{"still_queued": []any{}}}})
			}
		case "control_response":
			resp, _ := m["response"].(map[string]any)
			id, _ := resp["request_id"].(string)
			responses[id] = resp
		}
	}
	// wait processes stdin until done reports true; false when stdin closed.
	wait := func(timeout time.Duration, done func() bool) bool {
		var deadline <-chan time.Time
		if timeout > 0 {
			deadline = time.After(timeout)
		}
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for !done() {
			select {
			case m, ok := <-stdin:
				if !ok {
					return false
				}
				if m["type"] == "user" {
					continue // queued input is ignored mid-turn
				}
				handleControl(m)
			case <-deadline:
				return true
			case <-tick.C:
			}
		}
		return true
	}

	turn := 0
	for m := range stdin {
		if m["type"] != "user" {
			handleControl(m)
			continue
		}
		if len(turns) == 0 {
			continue
		}
		lines := turns[turn%len(turns)]
		turn++
		interrupted = false
		for _, line := range lines {
			if interrupted {
				break
			}
			var probe map[string]any
			_ = json.Unmarshal([]byte(line), &probe)
			if d, ok := probe["fake"].(string); ok {
				switch d {
				case "sleep":
					ms, _ := probe["ms"].(float64)
					if !wait(time.Duration(ms)*time.Millisecond, func() bool { return interrupted }) {
						return
					}
				case "hang":
					if !wait(0, func() bool { return interrupted }) {
						return
					}
				case "gate":
					gate := os.Getenv("FAKECLAUDE_GATE")
					if !wait(0, func() bool {
						_, err := os.Stat(gate)
						return interrupted || err == nil
					}) {
						return
					}
				case "mcp_list":
					server, _ := probe["server"].(string)
					entry := map[string]any{"kind": "mcp_list"}
					if cs, err := mcpSession(args, server); err != nil {
						entry["error"] = err.Error()
					} else {
						res, err := cs.ListTools(context.Background(), nil)
						if err != nil {
							entry["error"] = err.Error()
						} else {
							names := []string{}
							for _, tool := range res.Tools {
								names = append(names, tool.Name)
							}
							entry["tools"] = names
						}
						_ = cs.Close()
					}
					logLine(entry)
				case "mcp_call":
					server, _ := probe["server"].(string)
					tool, _ := probe["tool"].(string)
					callID, _ := probe["id"].(string)
					callArgs, _ := probe["args"].(map[string]any)
					name := "mcp__" + server + "__" + tool
					outJSON(map[string]any{"type": "assistant", "message": map[string]any{"model": "claude-haiku-5-5", "role": "assistant",
						"content": []any{map[string]any{"type": "tool_use", "id": callID, "name": name, "input": callArgs}}},
						"parent_tool_use_id": nil, "session_id": session})
					reqID := "fake_perm_" + callID
					outJSON(map[string]any{"type": "control_request", "request_id": reqID, "request": map[string]any{
						"subtype": "can_use_tool", "tool_name": name, "input": callArgs, "tool_use_id": callID}})
					if !wait(0, func() bool { _, ok := responses[reqID]; return ok || interrupted }) {
						return
					}
					text, isError := "", false
					perm, _ := responses[reqID]["response"].(map[string]any)
					if perm["behavior"] != "allow" {
						text, isError = fmt.Sprint(perm["message"]), true
					} else if cs, err := mcpSession(args, server); err != nil {
						text, isError = err.Error(), true
					} else {
						res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
							Name: tool, Arguments: callArgs, Meta: mcp.Meta{"claudecode/toolUseId": callID},
						})
						if err != nil {
							text, isError = err.Error(), true
						} else {
							isError = res.IsError
							for _, c := range res.Content {
								if tc, ok := c.(*mcp.TextContent); ok {
									text += tc.Text
								}
							}
						}
						_ = cs.Close()
					}
					logLine(map[string]any{"kind": "mcp_result", "tool": tool, "text": text, "is_error": isError})
					outJSON(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{
						map[string]any{"type": "tool_result", "tool_use_id": callID, "content": text, "is_error": isError}}},
						"parent_tool_use_id": nil, "session_id": session})
				case "exit":
					if s, _ := probe["stderr"].(string); s != "" {
						fmt.Fprintln(os.Stderr, s)
					}
					code, _ := probe["code"].(float64)
					os.Exit(int(code))
				}
				continue
			}
			out(line)
			if probe["type"] == "control_request" {
				id, _ := probe["request_id"].(string)
				if !wait(0, func() bool { _, ok := responses[id]; return ok || interrupted }) {
					return
				}
			}
		}
		if interrupted {
			outJSON(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "[Request interrupted by user]"}}}, "parent_tool_use_id": nil, "session_id": session})
			outJSON(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true,
				"stop_reason": nil, "terminal_reason": "aborted_streaming", "result": nil, "session_id": session,
				"total_cost_usd": 0, "usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
				"model_usage_since_last_result": map[string]any{}})
		}
	}
}

func loadFixture(path string) [][]string {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakeclaude: "+err.Error())
		os.Exit(2)
	}
	var turns [][]string
	var cur []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cur = append(cur, line)
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(line), &probe) == nil && probe.Type == "result" {
			turns = append(turns, cur)
			cur = nil
		}
	}
	if len(cur) > 0 {
		turns = append(turns, cur)
	}
	return turns
}

// authCommand fakes `auth login` and `auth logout`.
func authCommand(args []string) {
	logLine(map[string]any{"kind": "auth", "argv": args, "env": os.Environ()})
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	write := func(content string) {
		if dir != "" {
			_ = os.WriteFile(filepath.Join(dir, "fakeclaude_auth.json"), []byte(content), 0o600)
		}
	}
	if args[1] == "logout" {
		write(`{"loggedIn":false,"authMethod":"none"}`)
		fmt.Fprintln(os.Stderr, "Successfully logged out from your Anthropic account.")
		return
	}
	if ms := os.Getenv("FAKECLAUDE_LOGIN_MS"); ms != "" {
		var n int
		_, _ = fmt.Sscanf(ms, "%d", &n)
		time.Sleep(time.Duration(n) * time.Millisecond)
	}
	switch os.Getenv("FAKECLAUDE_LOGIN") {
	case "fail":
		fmt.Fprintln(os.Stderr, "Opening browser to sign in…")
		fmt.Fprintln(os.Stderr, "If the browser didn't open, visit: https://claude.ai/oauth/authorize?code=true&state=secret-state")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "\x1b[31mLogin failed:\x1b[0m token exchange refused at https://console.anthropic.com/v1/oauth/token?code=private-code")
		os.Exit(1)
	case "hang":
		fmt.Fprintln(os.Stderr, "Opening browser to sign in…")
		time.Sleep(time.Hour)
	}
	email := os.Getenv("FAKECLAUDE_LOGIN_EMAIL")
	if email == "" {
		email = "new@example.com"
	}
	write(`{"loggedIn":true,"authMethod":"claude.ai","email":"` + email + `","subscriptionType":"pro"}`)
	fmt.Fprintln(os.Stderr, "Login successful.")
}
