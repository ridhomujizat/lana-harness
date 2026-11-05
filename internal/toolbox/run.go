package toolbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// maxOutput is the combined-output cap of one command run (design "Command
// execution").
const maxOutput = 64 * 1024

// errorTail is how many trailing characters an exit-code error keeps.
const errorTail = 500

// RunResult is the result of one command run (REQ-TB-004, API-TB-009).
type RunResult struct {
	Output     string `json:"output"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
}

// Run renders the argv template of a command, execs the resolved binary with no
// shell and returns its output (REQ-TB-004). It validates the required inputs
// and enum values, applies defaults and resolves [env] secrets through the
// injected resolver (REQ-TB-007).
func (s *Store) Run(ctx context.Context, m Manifest, cmdName string, input map[string]any, workspace string, secret func(string) (string, bool)) (RunResult, error) {
	command, ok := findCommand(m, cmdName)
	if !ok {
		return RunResult{}, fmt.Errorf("toolbox: %s has no command %q", m.ID, cmdName)
	}
	values, err := commandInput(command, input)
	if err != nil {
		return RunResult{}, err
	}
	args := renderArgs(command.Args, values)

	path, ok := s.Resolve(m)
	if !ok {
		return RunResult{}, fmt.Errorf("toolbox: %s is not installed", m.Detect.Binary)
	}
	dir := s.workdir(m, command.Workdir, workspace)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return RunResult{}, err
	}
	env, err := commandEnv(m, secret)
	if err != nil {
		return RunResult{}, err
	}

	timeout := command.TimeoutSecs
	if timeout <= 0 {
		timeout = defaultTimeoutSecs
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, path, args...)
	cmd.Dir = dir
	cmd.Env = env
	out := &cappedBuffer{max: maxOutput}
	cmd.Stdout = out
	cmd.Stderr = out

	start := time.Now()
	err = cmd.Run()
	duration := time.Since(start).Milliseconds()
	text := out.String()

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return RunResult{}, fmt.Errorf("toolbox: %s timed out after %ds", cmdName, timeout)
	}
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return RunResult{}, err
		}
	}
	if exitCode != 0 {
		return RunResult{}, fmt.Errorf("exit code %d: %s", exitCode, tail(text, errorTail))
	}
	if command.Output == "json" && !json.Valid([]byte(strings.TrimSpace(text))) {
		return RunResult{}, errors.New("toolbox: command output is not valid JSON")
	}
	return RunResult{Output: text, ExitCode: exitCode, DurationMs: duration}, nil
}

// findCommand returns the named command of a manifest.
func findCommand(m Manifest, name string) (Command, bool) {
	for _, c := range m.Commands {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

// commandInput validates the caller input against the declared fields and
// returns the values that are present after defaults. A missing required field
// or a value outside its enum is an error (REQ-TB-004).
func commandInput(c Command, input map[string]any) (map[string]any, error) {
	values := make(map[string]any, len(c.Input))
	for name, field := range c.Input {
		raw, provided := input[name]
		if !provided || isMissingValue(raw) {
			if field.Default != nil {
				values[name] = field.Default
				continue
			}
			if field.Required {
				return nil, fmt.Errorf("toolbox: input %q is required", name)
			}
			continue
		}
		if err := checkInputType(name, field, raw); err != nil {
			return nil, err
		}
		if len(field.Enum) > 0 && !containsString(field.Enum, formatValue(raw)) {
			return nil, fmt.Errorf("toolbox: input %q must be one of %s", name, strings.Join(field.Enum, ", "))
		}
		values[name] = raw
	}
	return values, nil
}

// checkInputType rejects a value whose JSON type does not match the field.
func checkInputType(name string, field InputField, value any) error {
	switch field.Type {
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("toolbox: input %q must be a boolean", name)
		}
	case "number":
		if _, ok := asFloat(value); !ok {
			return fmt.Errorf("toolbox: input %q must be a number", name)
		}
	case "integer":
		f, ok := asFloat(value)
		if !ok || f != float64(int64(f)) {
			return fmt.Errorf("toolbox: input %q must be an integer", name)
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("toolbox: input %q must be a string", name)
		}
	}
	return nil
}

// renderArgs replaces every {{field}} of an argv template. An arg that is only
// references to absent optional fields is dropped (REQ-TB-004).
func renderArgs(templates []string, values map[string]any) []string {
	args := make([]string, 0, len(templates))
	for _, template := range templates {
		matches := refRE.FindAllStringSubmatch(template, -1)
		if len(matches) == 0 {
			args = append(args, template)
			continue
		}
		onlyRefs := refRE.ReplaceAllString(template, "") == ""
		if onlyRefs && allAbsent(matches, values) {
			continue
		}
		rendered := refRE.ReplaceAllStringFunc(template, func(ref string) string {
			name := refRE.FindStringSubmatch(ref)[1]
			return formatValue(values[name])
		})
		args = append(args, rendered)
	}
	return args
}

// allAbsent reports whether every reference names a field without a value.
func allAbsent(matches [][]string, values map[string]any) bool {
	for _, match := range matches {
		if _, ok := values[match[1]]; ok {
			return false
		}
	}
	return true
}

// workdir resolves a command's working directory: the package data dir, or the
// caller workspace when the rule is "workspace" and one is given (REQ-TB-004).
func (s *Store) workdir(m Manifest, rule, workspace string) string {
	if rule == "workspace" && workspace != "" {
		return workspace
	}
	return s.DataDir(m.ID)
}

// commandEnv builds the environment of a command: the daemon environment plus
// the manifest [env] entries. A "secret:<NAME>" value is resolved through the
// resolver; a missing secret is an error (REQ-TB-007).
func commandEnv(m Manifest, secret func(string) (string, bool)) ([]string, error) {
	env := os.Environ()
	for name, value := range m.Env {
		if key, ok := strings.CutPrefix(value, "secret:"); ok {
			if secret == nil {
				return nil, fmt.Errorf("toolbox: missing secret %s", key)
			}
			resolved, ok := secret(key)
			if !ok {
				return nil, fmt.Errorf("toolbox: missing secret %s", key)
			}
			env = append(env, name+"="+resolved)
			continue
		}
		env = append(env, name+"="+value)
	}
	return env, nil
}

// InputSchema builds the JSON Schema object of a command's input fields
// (REQ-TB-004).
func InputSchema(c Command) json.RawMessage {
	properties := make(map[string]any, len(c.Input))
	required := make([]string, 0, len(c.Input))
	for name, field := range c.Input {
		property := map[string]any{"type": jsonType(field.Type)}
		if field.Description != "" {
			property["description"] = field.Description
		}
		if len(field.Enum) > 0 {
			property["enum"] = field.Enum
		}
		if field.Default != nil {
			property["default"] = field.Default
		}
		properties[name] = property
		if field.Required {
			required = append(required, name)
		}
	}
	sort.Strings(required)
	schema := map[string]any{"type": "object"}
	if len(properties) > 0 {
		schema["properties"] = properties
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	out, _ := json.Marshal(schema)
	return out
}

// jsonType maps an input type to its JSON Schema type. The default is string.
func jsonType(t string) string {
	switch t {
	case "number", "integer", "boolean":
		return t
	default:
		return "string"
	}
}

// typeOf returns an input field type with the design default applied.
func typeOf(f InputField) string {
	if f.Type == "" {
		return "string"
	}
	return f.Type
}

// formatValue renders a value as the string an argv template uses.
func formatValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 32)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case json.Number:
		return t.String()
	default:
		return fmt.Sprint(v)
	}
}

// asFloat returns a numeric value as a float64.
func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// isMissingValue reports whether an input value counts as absent: nil or an
// empty string.
func isMissingValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	default:
		return false
	}
}

// tail returns the last n runes of s.
func tail(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[len(runes)-n:])
}

// cappedBuffer collects at most max bytes and drops the rest. It is safe for
// concurrent writers because the command copiers write from two goroutines.
type cappedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.max - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
