// Command fakeagy stands in for the `agy` binary in tests.
//
// Environment:
//
//	FAKEAGY_FIXTURE        JSONL ACP fixture to replay (see below)
//	FAKEAGY_RECORD         file to append JSON log lines to (start argv/env/cwd, stdin lines)
//	FAKEAGY_VERSION        `--version` output (default "1.5.17")
//	FAKEAGY_GATE           file the {"fake":"gate"} directive waits for
//	FAKEAGY_IGNORE_CANCEL  "1" hangs after receiving session/cancel
//	FAKEAGY_FAIL_LOAD      "1" answers session/load with a JSON-RPC error
//	FAKEAGY_MODELS         `models` prints this file (default testdata/models.txt)
//	FAKEAGY_MODELS_ERROR    `models` prints this line and exits 1 instead
//	FAKEAGY_SIGNED_OUT     "1" makes `models` print the signed-out error and exit 1
//	FAKEAGY_USAGE          `-p /usage` prints this file
//	FAKEAGY_LOGIN_CODE     `acp --login` succeeds only when stdin matches this
//	FAKEAGY_LOGIN_EMAIL    a successful `acp --login` prints "Logged in as <this>."
//	FAKEAGY_PROMPT         file of lines a router prompt run (spec 78: `-p`
//	                       without /usage) prints; the default streams "Hi
//	                       from agy" as step_update text deltas and ends with
//	                       a SUCCESS result with usage. Directives:
//	                       {"fake":"sleep","ms":100}, {"fake":"hang"},
//	                       {"fake":"exit","code":3,"stderr":"text"}. Any other
//	                       line is printed as it is (an "AGY_ERROR: …" line
//	                       too).
//
// A prompt run records a line with kind "prompt": argv, the selected
// environment, cwd, the stdin text, and the number of entries in cwd.
//
// The fixture is the recorded `agy acp` conversation. Each line is a one-line
// JSON object the recorder wrote: {"dir":"send"|"recv","msg":{…}}, where
// "recv" is a message agy sent lana and "send" one lana sent agy. The fake
// drives lana: for every request lana writes on stdin it replays the next
// "recv" messages up to and including that request's response, and for every
// agent request (session/request_permission) it waits for lana's reply on
// stdin. A "recv" line may instead carry a directive:
//
//	{"dir":"recv","fake":"gate"}  wait until FAKEAGY_GATE exists
//	{"dir":"recv","fake":"hang"}  wait for session/cancel, then hang
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// fixtureLine is one recorded ACP line: who sent it and the message itself. A
// line with fake set is a directive instead of a message.
type fixtureLine struct {
	Dir  string          `json:"dir"`
	Fake string          `json:"fake"`
	Msg  json.RawMessage `json:"msg"`
}

// rpcMsg is the part of a lana message the fake reads.
type rpcMsg struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
}

// selectedEnv lists the environment variables the fake records.
var selectedEnv = []string{"HOME", "USERPROFILE", "AGY_CLI_DISABLE_AUTO_UPDATE", "GEMINI_API_KEY", "GOOGLE_API_KEY", "SSH_CONNECTION"}

var (
	logMu sync.Mutex
	outMu sync.Mutex
)

func main() {
	args := os.Args[1:]
	switch {
	case len(args) == 1 && args[0] == "--version":
		v := os.Getenv("FAKEAGY_VERSION")
		if v == "" {
			v = "1.5.17"
		}
		fmt.Println(v)
	case len(args) == 1 && args[0] == "acp":
		runACP(args)
	case len(args) == 2 && args[0] == "acp" && args[1] == "--login":
		runLogin(args)
	case len(args) == 1 && args[0] == "models":
		runModels()
	case isUsageArgs(args):
		runUsage(args)
	case len(args) >= 1 && args[0] == "--input-format":
		runPrompt(args)
	default:
		fmt.Fprintf(os.Stderr, "fakeagy: unsupported args %v\n", args)
		os.Exit(2)
	}
}

// isUsageArgs reports whether argv is the `/usage` print mode ("-p /usage").
func isUsageArgs(args []string) bool {
	for i, a := range args {
		if a == "-p" && i+1 < len(args) && args[i+1] == "/usage" {
			return true
		}
	}
	return false
}

// runModels fakes `agy models`: the model list, or the signed-out error, or a
// forced error line.
func runModels() {
	cwd, _ := os.Getwd()
	logStart([]string{"models"}, cwd)
	if os.Getenv("FAKEAGY_SIGNED_OUT") == "1" {
		fmt.Println("Fetching available models...")
		fmt.Println("Error: Please sign in to view available models. Launch the CLI without arguments to sign in.")
		os.Exit(1)
	}
	if text := os.Getenv("FAKEAGY_MODELS_ERROR"); text != "" {
		fmt.Fprintln(os.Stderr, text)
		os.Exit(1)
	}
	path := os.Getenv("FAKEAGY_MODELS")
	if path == "" {
		path = filepath.Join("testdata", "models.txt")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakeagy: "+err.Error())
		os.Exit(2)
	}
	_, _ = os.Stdout.Write(data)
}

// runUsage fakes `agy -p /usage --output-format json`: it prints the file
// named by FAKEAGY_USAGE (nothing when unset) and records its cwd.
func runUsage(argv []string) {
	cwd, _ := os.Getwd()
	logStart(argv, cwd)
	path := os.Getenv("FAKEAGY_USAGE")
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakeagy: "+err.Error())
		os.Exit(2)
	}
	_, _ = os.Stdout.Write(data)
}

// runLogin fakes `agy acp --login`: it prints the link and the code prompt,
// reads one stdin line, records it, and exits 0 only when it matches
// FAKEAGY_LOGIN_CODE, after "Logged in as <FAKEAGY_LOGIN_EMAIL>." when set.
func runLogin(argv []string) {
	cwd, _ := os.Getwd()
	logStart(argv, cwd)
	fmt.Println("Open this link in a browser to log in with Google:")
	fmt.Println("https://accounts.google.com/o/oauth2/auth?client_id=fake&redirect_uri=x&state=x")
	fmt.Println("Then paste the authorization code, or the full URL of the page you land on, and press Enter:")
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		fmt.Fprintln(os.Stderr, "login cancelled: no authorization code was entered")
		os.Exit(1)
	}
	line := strings.TrimRight(sc.Text(), "\r")
	writeRecord(map[string]any{"kind": "login", "line": line})
	if line != "" && line == os.Getenv("FAKEAGY_LOGIN_CODE") {
		if email := os.Getenv("FAKEAGY_LOGIN_EMAIL"); email != "" {
			fmt.Printf("Logged in as %s.\n", email)
		}
		return
	}
	fmt.Fprintln(os.Stderr, "login failed: bad code")
	os.Exit(1)
}

// writeRecord appends one JSON object to FAKEAGY_RECORD.
func writeRecord(v map[string]any) {
	path := os.Getenv("FAKEAGY_RECORD")
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

// logStart records argv, the selected environment, and the working directory.
func logStart(argv []string, cwd string) {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		for _, want := range selectedEnv {
			if name == want {
				env = append(env, kv)
				break
			}
		}
	}
	writeRecord(map[string]any{"kind": "start", "argv": argv, "env": env, "cwd": cwd})
}

// out writes one message to stdout.
func out(msg any) {
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	outMu.Lock()
	defer outMu.Unlock()
	fmt.Fprintln(os.Stdout, string(b))
}

// stdinReader reads lana's stdin lines into a channel, recording each.
func stdinReader() <-chan json.RawMessage {
	ch := make(chan json.RawMessage, 64)
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 1<<20), 32<<20)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			raw := json.RawMessage(line)
			writeRecord(map[string]any{"kind": "stdin", "line": raw})
			ch <- raw
		}
	}()
	return ch
}

func loadFixture(path string) []fixtureLine {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakeagy: "+err.Error())
		os.Exit(2)
	}
	var out []fixtureLine
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var fl fixtureLine
		if json.Unmarshal([]byte(line), &fl) == nil {
			out = append(out, fl)
		}
	}
	return out
}

func runACP(argv []string) {
	cwd, _ := os.Getwd()
	logStart(argv, cwd)
	lines := loadFixture(os.Getenv("FAKEAGY_FIXTURE"))
	var recvs []fixtureLine
	for _, fl := range lines {
		if fl.Dir == "recv" {
			recvs = append(recvs, fl)
		}
	}
	stdin := stdinReader()
	ignoreCancel := os.Getenv("FAKEAGY_IGNORE_CANCEL") == "1"

	idx := 0
	for {
		raw, ok := <-stdin
		if !ok {
			return
		}
		var env rpcMsg
		if json.Unmarshal(raw, &env) != nil {
			continue
		}
		if env.Method == "session/cancel" {
			if ignoreCancel {
				hang()
			}
			continue
		}
		if env.Method == "" || len(env.ID) == 0 {
			continue // a notification or a reply lana sent out of order
		}
		idx = replay(recvs, idx, env.ID, env.Method, stdin)
	}
}

// replay writes recv messages until the response to lana's request (with the
// request's actual id). An agent request pauses for lana's reply. It returns
// the new recv cursor.
func replay(recvs []fixtureLine, idx int, id json.RawMessage, method string, stdin <-chan json.RawMessage) int {
	failLoad := os.Getenv("FAKEAGY_FAIL_LOAD") == "1"
	for idx < len(recvs) {
		fl := recvs[idx]
		idx++
		switch fl.Fake {
		case "gate":
			waitGate()
			continue
		case "hang":
			waitCancel(stdin)
			return idx
		}
		var msg map[string]any
		if len(fl.Msg) > 0 {
			_ = json.Unmarshal(fl.Msg, &msg)
		}
		if msg == nil {
			continue
		}
		_, hasMethod := msg["method"]
		_, hasResult := msg["result"]
		_, hasError := msg["error"]
		switch {
		case hasResult || hasError:
			if method == "session/load" && failLoad {
				out(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32603, "message": "session not found"}})
				return idx
			}
			msg["id"] = id
			out(msg)
			return idx
		case hasMethod:
			out(msg)
			if _, ok := msg["id"]; ok {
				// An agent request: wait for lana's reply on stdin.
				<-stdin
			}
		default:
			out(msg)
		}
	}
	return idx
}

// waitGate waits until the FAKEAGY_GATE file exists.
func waitGate() {
	path := os.Getenv("FAKEAGY_GATE")
	if path == "" {
		return
	}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitCancel reads stdin until lana sends session/cancel, then hangs.
func waitCancel(stdin <-chan json.RawMessage) {
	for {
		raw, ok := <-stdin
		if !ok {
			return
		}
		var env rpcMsg
		if json.Unmarshal(raw, &env) == nil && env.Method == "session/cancel" {
			hang()
		}
	}
}

// hang blocks forever, keeping the process alive until it is killed.
func hang() {
	select {}
}

// defaultPrompt is the answer of a prompt run without FAKEAGY_PROMPT.
// defaultPrompt is agy 1.5.17's stream-json output shape (captured live).
var defaultPrompt = []string{
	`{"event":"init","conversation_id":"c-1","init":{"cwd":"/tmp","permission_mode":"request-review"}}`,
	`{"event":"step_update","step_update":{"conversation_id":"c-1","step_index":0,"state":"DONE","step_type":"user_input"}}`,
	`{"event":"step_update","step_update":{"conversation_id":"c-1","step_index":1,"state":"ACTIVE","step_type":"agent_response","thinking_delta":"secret thoughts"}}`,
	`{"event":"step_update","step_update":{"conversation_id":"c-1","step_index":1,"state":"ACTIVE","step_type":"agent_response","text_delta":"Hi"}}`,
	`{"event":"step_update","step_update":{"conversation_id":"c-1","step_index":1,"state":"DONE","step_type":"agent_response","text_delta":" from agy"}}`,
	`{"event":"result","result":{"conversation_id":"c-1","status":"SUCCESS","response":"Hi from agy","usage":{"input_tokens":20,"output_tokens":5,"thinking_tokens":7,"cache_read_tokens":4,"total_tokens":36}}}`,
}

// runPrompt fakes a router prompt run (spec 78): it reads the whole stdin,
// records it, and prints the prompt lines.
func runPrompt(argv []string) {
	cwd, _ := os.Getwd()
	raw, _ := io.ReadAll(os.Stdin)
	// Like agy 1.5.17, the prompt is one stream-json input message.
	var input struct {
		Event   string `json:"event"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	stdin := raw
	if json.Unmarshal(raw, &input) == nil && input.Event == "user" {
		stdin = []byte(input.Message.Content)
	}
	entries, _ := os.ReadDir(cwd)
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		for _, want := range selectedEnv {
			if name == want {
				env = append(env, kv)
				break
			}
		}
	}
	writeRecord(map[string]any{"kind": "prompt", "argv": argv, "env": env, "cwd": cwd, "stdin": string(stdin), "cwd_entries": len(entries)})
	lines := defaultPrompt
	if path := os.Getenv("FAKEAGY_PROMPT"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fakeagy: "+err.Error())
			os.Exit(2)
		}
		lines = nil
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) != "" {
				lines = append(lines, line)
			}
		}
	}
	for _, line := range lines {
		var d struct {
			Fake   string `json:"fake"`
			MS     int    `json:"ms"`
			Code   int    `json:"code"`
			Stderr string `json:"stderr"`
		}
		if json.Unmarshal([]byte(line), &d) == nil && d.Fake != "" {
			switch d.Fake {
			case "sleep":
				time.Sleep(time.Duration(d.MS) * time.Millisecond)
			case "hang":
				time.Sleep(time.Hour)
			case "exit":
				if d.Stderr != "" {
					fmt.Fprintln(os.Stderr, d.Stderr)
				}
				os.Exit(d.Code)
			}
			continue
		}
		outMu.Lock()
		fmt.Fprintln(os.Stdout, line)
		outMu.Unlock()
	}
}
