// Package scriptrt runs guardian-written JavaScript for the Script Parse node
// (spec #44) in an embedded goja sandbox (ADR 0021). A script defines
// function parse(input, ctx) and runs with only the ECMAScript built-ins plus
// the console.log and html helpers. There is no file, network, process, timer
// or module access; size caps and vm.Interrupt bound every run.
package scriptrt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dop251/goja"
)

// MaxScriptBytes is the largest script source Compile accepts (REQ-SP-002).
const MaxScriptBytes = 32 << 10

// Limits and defaults (REQ-SP-002, design §"Package internal/scriptrt").
const (
	defaultTimeout     = 2 * time.Second
	minTimeout         = 100 * time.Millisecond
	maxTimeout         = 10 * time.Second
	defaultMaxInput    = 2 << 20
	defaultMaxOutput   = 1 << 20
	defaultMaxLogLines = 50
	maxLogLineBytes    = 500
	logEllipsis        = "…"
)

// Errors returned by Compile and Run.
var (
	// ErrTimeout reports that the script exceeded its time limit.
	ErrTimeout = errors.New("scriptrt: script timed out")
	// ErrInputTooLarge reports that the JSON input exceeds MaxInput.
	ErrInputTooLarge = errors.New("scriptrt: input too large")
	// ErrOutputTooLarge reports that the JSON output exceeds MaxOutput.
	ErrOutputTooLarge = errors.New("scriptrt: output too large")
	// ErrScriptTooLarge reports that the source exceeds MaxScriptBytes.
	ErrScriptTooLarge = errors.New("scriptrt: script too large")
	// ErrNoParse reports that the script defines no parse function.
	ErrNoParse = errors.New("scriptrt: script has no parse function")
)

// errInterruptTimeout is the value passed to vm.Interrupt on the time limit, so a
// run can tell a timeout from a context cancellation.
var errInterruptTimeout = errors.New("scriptrt: timeout")

// Options tunes one Run. A zero field takes its default.
type Options struct {
	// Timeout bounds the run; default 2s, clamped to 100ms..10s.
	Timeout time.Duration
	// MaxInput is the JSON input size cap in bytes; default 2 MiB.
	MaxInput int
	// MaxOutput is the JSON output size cap in bytes; default 1 MiB.
	MaxOutput int
	// MaxLogLines is the number of console lines kept; default 50.
	MaxLogLines int
}

// withDefaults returns o with every unset field replaced by its default and the
// timeout clamped to its allowed range.
func (o Options) withDefaults() Options {
	if o.Timeout == 0 {
		o.Timeout = defaultTimeout
	}
	if o.Timeout < minTimeout {
		o.Timeout = minTimeout
	}
	if o.Timeout > maxTimeout {
		o.Timeout = maxTimeout
	}
	if o.MaxInput <= 0 {
		o.MaxInput = defaultMaxInput
	}
	if o.MaxOutput <= 0 {
		o.MaxOutput = defaultMaxOutput
	}
	if o.MaxLogLines <= 0 {
		o.MaxLogLines = defaultMaxLogLines
	}
	return o
}

// Result is one script run.
type Result struct {
	// Value is the parse output, normalized to plain JSON types.
	Value any
	// Logs holds the console.log lines, capped at MaxLogLines.
	Logs []string
	// Duration is the wall time of the run.
	Duration time.Duration
}

// Program is a compiled script and the hash of its source.
type Program struct {
	prog *goja.Program
	hash string
}

// Hash returns the SHA-256 hex digest of the script source, computed at
// Compile time.
func (p *Program) Hash() string {
	return p.hash
}

// ScriptError reports a compile-time syntax error or a runtime exception in a
// script. Line and Col are zero when the position is unknown.
type ScriptError struct {
	Message string
	Line    int
	Col     int
}

// Error renders the message with its source position when one is known.
func (e *ScriptError) Error() string {
	if e.Line == 0 {
		return e.Message
	}
	return fmt.Sprintf("line %d:%d: %s", e.Line, e.Col, e.Message)
}

// Compile checks the source size, its syntax, and that it defines a top-level
// parse function (REQ-SP-013, REQ-SP-002). A syntax error is a *ScriptError; a
// missing parse is ErrNoParse.
func Compile(src string) (*Program, error) {
	if len(src) > MaxScriptBytes {
		return nil, ErrScriptTooLarge
	}
	prog, err := goja.Compile("script.js", src, false)
	if err != nil {
		return nil, compileError(err)
	}
	sum := sha256.Sum256([]byte(src))
	p := &Program{prog: prog, hash: hex.EncodeToString(sum[:])}

	// A throwaway run proves the top-level code executes and parse is callable.
	vm := newRuntime(newLogBuffer(defaultMaxLogLines))
	timer := time.AfterFunc(defaultTimeout, func() { vm.Interrupt(errInterruptTimeout) })
	defer timer.Stop()
	if _, err := vm.RunProgram(prog); err != nil {
		return nil, runError(context.Background(), err)
	}
	if _, ok := parseFunc(vm); !ok {
		return nil, ErrNoParse
	}
	return p, nil
}

// Run executes parse(input, ctx) in a fresh runtime and returns its JSON output
// (REQ-SP-002). ctx bounds the run alongside Options.Timeout; a cancellation
// interrupts the script and returns ctx.Err().
func Run(ctx context.Context, p *Program, input any, jsctx map[string]any, o Options) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	o = o.withDefaults()
	start := time.Now()

	raw, err := json.Marshal(input)
	if err != nil {
		return Result{}, fmt.Errorf("scriptrt: encode input: %w", err)
	}
	if len(raw) > o.MaxInput {
		return Result{}, ErrInputTooLarge
	}
	var norm any
	if err := json.Unmarshal(raw, &norm); err != nil {
		return Result{}, fmt.Errorf("scriptrt: decode input: %w", err)
	}

	logs := newLogBuffer(o.MaxLogLines)
	vm := newRuntime(logs)

	timer := time.AfterFunc(o.Timeout, func() { vm.Interrupt(errInterruptTimeout) })
	defer timer.Stop()
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			vm.Interrupt(ctx.Err())
		case <-stopped:
		}
	}()

	if _, err := vm.RunProgram(p.prog); err != nil {
		return Result{}, runError(ctx, err)
	}
	parse, ok := parseFunc(vm)
	if !ok {
		return Result{}, ErrNoParse
	}

	ctxVal := goja.Value(vm.NewObject())
	if jsctx != nil {
		ctxVal = vm.ToValue(jsctx)
	}
	v, err := parse(goja.Undefined(), vm.ToValue(norm), ctxVal)
	if err != nil {
		return Result{}, runError(ctx, err)
	}

	out, err := encodeOutput(v, o.MaxOutput)
	if err != nil {
		return Result{}, err
	}
	return Result{Value: out, Logs: logs.result(), Duration: time.Since(start)}, nil
}

// parseFunc resolves the script's global parse function. It tests typeof parse
// so a missing binding (including a lexically scoped const) is not an error.
func parseFunc(vm *goja.Runtime) (goja.Callable, bool) {
	v, err := vm.RunString("typeof parse === 'function' ? parse : undefined")
	if err != nil {
		return nil, false
	}
	return goja.AssertFunction(v)
}

// encodeOutput exports, JSON-marshals (with a size check) and re-decodes the
// script result so the engine sees plain JSON types.
func encodeOutput(v goja.Value, max int) (any, error) {
	raw, err := json.Marshal(v.Export())
	if err != nil {
		return nil, &ScriptError{Message: "output is not JSON: " + err.Error()}
	}
	if len(raw) > max {
		return nil, ErrOutputTooLarge
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, &ScriptError{Message: "output is not JSON: " + err.Error()}
	}
	return out, nil
}

// newRuntime builds a fresh sandbox: the field name mapper for Go values, the
// console.log helper and the html helper. Nothing else is defined.
func newRuntime(logs *logBuffer) *goja.Runtime {
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	installConsole(vm, logs)
	installHTML(vm)
	return vm
}

// installConsole defines the console.log global.
func installConsole(vm *goja.Runtime, logs *logBuffer) {
	console := vm.NewObject()
	_ = console.Set("log", func(call goja.FunctionCall) goja.Value {
		logs.add(formatLog(call.Arguments))
		return goja.Undefined()
	})
	_ = vm.Set("console", console)
}

// formatLog joins the log arguments with a space, JSON-stringifies objects and
// caps the line at maxLogLineBytes.
func formatLog(args []goja.Value) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = logArg(a)
	}
	return truncateBytes(strings.Join(parts, " "), maxLogLineBytes)
}

// logArg renders one console argument: objects and arrays as JSON, everything
// else through its string form.
func logArg(v goja.Value) string {
	if v == nil {
		return "undefined"
	}
	e := v.Export()
	switch e.(type) {
	case map[string]any, []any:
		if b, err := json.Marshal(e); err == nil {
			return string(b)
		}
	}
	return v.String()
}

// truncateBytes caps s at max bytes, keeping valid UTF-8 and appending an
// ellipsis marker.
func truncateBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	limit := max - len(logEllipsis)
	if limit < 0 {
		limit = 0
	}
	b := []byte(s)[:limit]
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	return string(b) + logEllipsis
}

// logBuffer keeps the first max console lines and counts the dropped rest.
type logBuffer struct {
	max     int
	lines   []string
	dropped int
}

// newLogBuffer returns a buffer that keeps at most max lines.
func newLogBuffer(max int) *logBuffer {
	return &logBuffer{max: max}
}

// add keeps line until the cap, then counts it as dropped.
func (b *logBuffer) add(line string) {
	if len(b.lines) < b.max {
		b.lines = append(b.lines, line)
		return
	}
	b.dropped++
}

// result returns the kept lines and, when any were dropped, one marker line.
func (b *logBuffer) result() []string {
	if b.dropped == 0 {
		return b.lines
	}
	out := make([]string, 0, len(b.lines)+1)
	out = append(out, b.lines...)
	out = append(out, fmt.Sprintf("… %d more lines", b.dropped))
	return out
}

// compileError maps a goja compile error to a *ScriptError with its position.
func compileError(err error) error {
	var se *goja.CompilerSyntaxError
	if errors.As(err, &se) {
		out := &ScriptError{Message: se.Message}
		if se.File != nil {
			pos := se.File.Position(se.Offset)
			out.Line, out.Col = pos.Line, pos.Column
		}
		return out
	}
	return &ScriptError{Message: err.Error()}
}

// runError maps a goja runtime error to ErrTimeout, ctx.Err() or a
// *ScriptError. Interrupted errors are uncatchable and distinct from exceptions.
func runError(ctx context.Context, err error) error {
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		if v, ok := interrupted.Value().(error); ok && errors.Is(v, errInterruptTimeout) {
			return ErrTimeout
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		return ErrTimeout
	}
	var ex *goja.Exception
	if errors.As(err, &ex) {
		return exceptionError(ex)
	}
	return &ScriptError{Message: err.Error()}
}

// exceptionError builds a *ScriptError from a JS exception and its top stack
// frame, so the engine can report a line and column.
func exceptionError(ex *goja.Exception) *ScriptError {
	out := &ScriptError{Message: "script error"}
	if v := ex.Value(); v != nil {
		out.Message = v.String()
	}
	if frames := ex.Stack(); len(frames) > 0 {
		pos := frames[0].Position()
		out.Line, out.Col = pos.Line, pos.Column
	}
	return out
}
