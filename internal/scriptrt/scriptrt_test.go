package scriptrt

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// mustCompile compiles src or fails the test.
func mustCompile(t *testing.T, src string) *Program {
	t.Helper()
	p, err := Compile(src)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return p
}

// runObject runs p on input and returns the object output.
func runObject(t *testing.T, p *Program, input any, o Options) map[string]any {
	t.Helper()
	res, err := Run(context.Background(), p, input, nil, o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	m, ok := res.Value.(map[string]any)
	if !ok {
		t.Fatalf("Run value = %#v, want object", res.Value)
	}
	return m
}

// TestRunParse covers the happy paths: fields, a markdown regex and JSON.parse.
func TestRunParse(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		input any
		check func(t *testing.T, got map[string]any)
	}{
		{
			name:  "fields",
			src:   `function parse(input, ctx){ return {title: input.title, n: input.items.length}; }`,
			input: map[string]any{"title": "hi", "items": []any{1, 2, 3}},
			check: func(t *testing.T, got map[string]any) {
				if got["title"] != "hi" {
					t.Fatalf("title = %v, want hi", got["title"])
				}
				if got["n"] != float64(3) {
					t.Fatalf("n = %#v, want 3", got["n"])
				}
			},
		},
		{
			name: "markdown headings",
			src: `function parse(input, ctx){
				const re = /^#+\s+(.*)$/gm;
				const out = [];
				let m;
				while ((m = re.exec(input)) !== null) { out.push(m[1]); }
				return {headings: out};
			}`,
			input: "# One\nbody\n## Two\n### Three\n",
			check: func(t *testing.T, got map[string]any) {
				want := []any{"One", "Two", "Three"}
				if !reflect.DeepEqual(got["headings"], want) {
					t.Fatalf("headings = %#v, want %#v", got["headings"], want)
				}
			},
		},
		{
			name:  "json parse",
			src:   `function parse(input, ctx){ const o = JSON.parse(input); return {v: o.v}; }`,
			input: `{"v":42}`,
			check: func(t *testing.T, got map[string]any) {
				if got["v"] != float64(42) {
					t.Fatalf("v = %#v, want 42", got["v"])
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runObject(t, mustCompile(t, tc.src), tc.input, Options{})
			tc.check(t, got)
		})
	}
}

// TestRunContextArg checks the ctx object built from the jsctx map.
func TestRunContextArg(t *testing.T) {
	p := mustCompile(t, `function parse(input, ctx){ return {item: ctx.item, run: ctx.run_number}; }`)
	res, err := Run(context.Background(), p, nil, map[string]any{"item": "a", "run_number": 3}, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := res.Value.(map[string]any)
	if got["item"] != "a" || got["run"] != float64(3) {
		t.Fatalf("ctx = %#v, want item=a run=3", got)
	}
}

// TestRunTimeout checks an endless loop stops at the time limit.
func TestRunTimeout(t *testing.T) {
	p := mustCompile(t, `function parse(input, ctx){ while (true) {} }`)
	start := time.Now()
	_, err := Run(context.Background(), p, nil, nil, Options{Timeout: 100 * time.Millisecond})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("Run err = %v, want ErrTimeout", err)
	}
	if d := time.Since(start); d > 100*time.Millisecond+200*time.Millisecond {
		t.Fatalf("timeout took %v, want within %v", d, 300*time.Millisecond)
	}
}

// TestRunContextCancel checks a cancelled context stops the script promptly.
func TestRunContextCancel(t *testing.T) {
	p := mustCompile(t, `function parse(input, ctx){ while (true) {} }`)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Run(ctx, p, nil, nil, Options{Timeout: 5 * time.Second})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run err = %v, want context deadline", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("run did not stop promptly: %v", d)
	}
}

// TestRunInputTooLarge checks the JSON input size cap.
func TestRunInputTooLarge(t *testing.T) {
	p := mustCompile(t, `function parse(input, ctx){ return {ok: true}; }`)
	_, err := Run(context.Background(), p, strings.Repeat("x", 3<<20), nil, Options{})
	if !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("Run err = %v, want ErrInputTooLarge", err)
	}
}

// TestRunOutputTooLarge checks the JSON output size cap.
func TestRunOutputTooLarge(t *testing.T) {
	p := mustCompile(t, `function parse(input, ctx){ return {s: "x".repeat(2 * 1024 * 1024)}; }`)
	_, err := Run(context.Background(), p, nil, nil, Options{})
	if !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("Run err = %v, want ErrOutputTooLarge", err)
	}
}

// TestCompileScriptTooLarge checks the source size cap is applied first.
func TestCompileScriptTooLarge(t *testing.T) {
	src := "function parse(){}" + strings.Repeat(" ", 40<<10)
	if _, err := Compile(src); !errors.Is(err, ErrScriptTooLarge) {
		t.Fatalf("Compile err = %v, want ErrScriptTooLarge", err)
	}
}

// TestCompileSyntaxError checks a syntax error carries a source line.
func TestCompileSyntaxError(t *testing.T) {
	_, err := Compile("function parse(input, ctx) {\n  const x = ;\n}\n")
	var se *ScriptError
	if !errors.As(err, &se) {
		t.Fatalf("Compile err = %v, want *ScriptError", err)
	}
	if se.Line <= 0 {
		t.Fatalf("ScriptError.Line = %d, want > 0", se.Line)
	}
}

// TestCompileNoParse checks a script without a parse function is rejected.
func TestCompileNoParse(t *testing.T) {
	if _, err := Compile("function other(input) { return {}; }"); !errors.Is(err, ErrNoParse) {
		t.Fatalf("Compile err = %v, want ErrNoParse", err)
	}
}

// TestRunLogs checks console lines reach Result.Logs.
func TestRunLogs(t *testing.T) {
	p := mustCompile(t, `function parse(input, ctx){
		console.log("hello", "world");
		console.log({a: 1});
		return {ok: true};
	}`)
	res, err := Run(context.Background(), p, nil, nil, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{"hello world", `{"a":1}`}
	if !reflect.DeepEqual(res.Logs, want) {
		t.Fatalf("Logs = %#v, want %#v", res.Logs, want)
	}
}

// TestRunLogsCap checks the line cap keeps the first lines and one marker.
func TestRunLogsCap(t *testing.T) {
	p := mustCompile(t, `function parse(input, ctx){
		for (let i = 0; i < 5; i++) { console.log("line " + i); }
		return {ok: true};
	}`)
	res, err := Run(context.Background(), p, nil, nil, Options{MaxLogLines: 3})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{"line 0", "line 1", "line 2", "… 2 more lines"}
	if !reflect.DeepEqual(res.Logs, want) {
		t.Fatalf("Logs = %#v, want %#v", res.Logs, want)
	}
}

// TestRunLogLineCap checks one long line is capped and stays valid UTF-8.
func TestRunLogLineCap(t *testing.T) {
	p := mustCompile(t, `function parse(input, ctx){ console.log("x".repeat(2000)); return {ok: true}; }`)
	res, err := Run(context.Background(), p, nil, nil, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Logs) != 1 {
		t.Fatalf("Logs = %#v, want one line", res.Logs)
	}
	line := res.Logs[0]
	if len(line) > maxLogLineBytes {
		t.Fatalf("line is %d bytes, want <= %d", len(line), maxLogLineBytes)
	}
	if !strings.HasSuffix(line, logEllipsis) {
		t.Fatalf("line = %q, want an ellipsis marker", line)
	}
	if !utf8.ValidString(line) {
		t.Fatal("line is not valid UTF-8")
	}
}

// TestRunForbiddenGlobals pins that the sandbox exposes no host capabilities.
func TestRunForbiddenGlobals(t *testing.T) {
	p := mustCompile(t, `function parse(input, ctx){
		return {
			require: typeof require,
			fetch: typeof fetch,
			setTimeout: typeof setTimeout,
			setInterval: typeof setInterval,
			XMLHttpRequest: typeof XMLHttpRequest,
			process: typeof process
		};
	}`)
	got := runObject(t, p, nil, Options{})
	for _, name := range []string{"require", "fetch", "setTimeout", "setInterval", "XMLHttpRequest", "process"} {
		if got[name] != "undefined" {
			t.Fatalf("typeof %s = %v, want undefined", name, got[name])
		}
	}
}
