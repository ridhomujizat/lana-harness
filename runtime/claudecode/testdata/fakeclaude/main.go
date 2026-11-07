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
//
// Fixture lines with a "fake" key are directives:
//
//	{"fake":"sleep","ms":100}                 sleep (interrupts still handled)
//	{"fake":"hang"}                           wait for an interrupt
//	{"fake":"gate"}                           wait until FAKECLAUDE_GATE exists
//	{"fake":"exit","code":1,"stderr":"text"}  write stderr and exit
//
// The fixture session ID 00000000-0000-4000-8000-000000000001 is replaced by
// the --session-id or --resume value.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

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
	if len(args) >= 2 && args[0] == "auth" && args[1] == "status" {
		a := os.Getenv("FAKECLAUDE_AUTH")
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
	logLine(map[string]any{"kind": "start", "argv": args, "env": os.Environ(), "cwd": cwd})
	for i, a := range args {
		if (a == "--session-id" || a == "--resume") && i+1 < len(args) {
			session = args[i+1]
		}
	}
	turns := loadFixture(os.Getenv("FAKECLAUDE_FIXTURE"))
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
				outJSON(map[string]any{"type": "control_response", "response": map[string]any{
					"subtype": "success", "request_id": id, "response": map[string]any{"commands": []any{}}}})
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
