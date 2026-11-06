package toolbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// maxOutput is the combined-output cap of one command run (design "Command
// execution").
const maxOutput = 64 * 1024

// maxFileOutput is the output cap of a command with output_from = "files"
// (REQ-TB-012).
const maxFileOutput = 256 * 1024

// maxFileRead bounds how many bytes of written files one run reads before a
// transform shrinks them (REQ-TB-012).
const maxFileRead = 16 << 20

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
// injected resolver (REQ-TB-007). With output_from = "files" the {{out}}
// template is a fresh temp directory, removed after the run, and the output is
// the content of the files written there; a transform then cleans it
// (REQ-TB-012).
func (s *Store) Run(ctx context.Context, m Manifest, cmdName string, input map[string]any, workspace string, secret func(string) (string, bool)) (RunResult, error) {
	command, ok := findCommand(m, cmdName)
	if !ok {
		return RunResult{}, fmt.Errorf("toolbox: %s has no command %q", m.ID, cmdName)
	}
	values, err := commandInput(command, input)
	if err != nil {
		return RunResult{}, err
	}

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
	files := command.OutputFrom == outputFromFiles
	var outDir string
	if files {
		outDir, err = os.MkdirTemp("", "lana-"+m.ID+"-out-*")
		if err != nil {
			return RunResult{}, err
		}
		defer func() { _ = os.RemoveAll(outDir) }()
		values[outField] = outDir
	}
	args := renderArgs(command.Args, values)

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
	truncated, limit := out.Truncated(), maxOutput
	if files {
		fileText, found, cut, err := fileOutput(outDir, command.Transform)
		if err != nil {
			return RunResult{}, err
		}
		if !found {
			return RunResult{}, fmt.Errorf("toolbox: %s wrote no output file: %s", cmdName, tail(strings.TrimSpace(text), errorTail))
		}
		text, truncated, limit = fileText, cut, maxFileOutput
	} else if command.Transform != "" {
		text = applyTransform(command.Transform, text)
	}
	if command.Output == "json" && !json.Valid([]byte(strings.TrimSpace(text))) {
		if truncated {
			return RunResult{}, fmt.Errorf("toolbox: command output is larger than %d KiB and was cut off, so it is not valid JSON", limit/1024)
		}
		return RunResult{}, errors.New("toolbox: command output is not valid JSON")
	}
	return RunResult{Output: text, ExitCode: exitCode, DurationMs: duration}, nil
}

// fileOutput reads the regular files written under dir, sorted by path,
// applies the transform to each and joins them. With more than one file each
// part starts with a "== <name>" line. The result is capped at maxFileOutput
// with a note; found is false when no file was written (REQ-TB-012).
func fileOutput(dir, transform string) (text string, found, truncated bool, err error) {
	var names []string
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type().IsRegular() {
			rel, relErr := filepath.Rel(dir, path)
			if relErr != nil {
				return relErr
			}
			names = append(names, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return "", false, false, fmt.Errorf("toolbox: read output files: %w", err)
	}
	if len(names) == 0 {
		return "", false, false, nil
	}
	sort.Strings(names)

	var b strings.Builder
	budget := maxFileRead
	for i, name := range names {
		if budget <= 0 {
			truncated = true
			break
		}
		content, readCut, readErr := readCapped(filepath.Join(dir, filepath.FromSlash(name)), budget)
		if readErr != nil {
			return "", true, false, fmt.Errorf("toolbox: read output file %s: %w", name, readErr)
		}
		budget -= len(content)
		truncated = truncated || readCut
		part := string(content)
		if transform != "" {
			part = applyTransform(transform, part)
		}
		if len(names) > 1 {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString("== " + name + "\n")
		}
		b.WriteString(strings.TrimRight(part, "\n"))
		b.WriteString("\n")
	}
	text = b.String()
	if len(text) > maxFileOutput {
		cut := maxFileOutput
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut]
		truncated = true
	}
	if truncated {
		text = strings.TrimRight(text, "\n") + fmt.Sprintf("\n…cut off at %d KiB\n", maxFileOutput/1024)
	}
	return text, true, truncated, nil
}

// readCapped reads at most limit bytes of a file and reports whether more was
// left.
func readCapped(path string, limit int) ([]byte, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	content, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, false, err
	}
	if len(content) > limit {
		return content[:limit], true, nil
	}
	return content, false, nil
}

// applyTransform runs a named output transform (REQ-TB-012). An unknown name
// leaves the text unchanged; validation rejects it earlier.
func applyTransform(transform, text string) string {
	if transform == transformSubtitles {
		return SubtitlesToText(text)
	}
	return text
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
	mu        sync.Mutex
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.max - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
			b.truncated = true
		} else {
			b.buf.Write(p)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

// Truncated reports whether output was dropped at the cap.
func (b *cappedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
