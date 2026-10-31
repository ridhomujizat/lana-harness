package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	lana "github.com/ridhomujizat/lana-harness"
)

// commandTimeout bounds one setup or check command.
const commandTimeout = 2 * time.Minute

// runConfig is the per-run input shared by all tasks.
type runConfig struct {
	model       string
	providerURL string
	keyEnv      string
	timeout     time.Duration
	keep        bool
	prices      priceTable
	stderr      io.Writer
}

// renderConfig builds the temporary lana home's config.toml (design section 5).
// The provider key is api_key_env, not api_key: the config stores the env var
// name and the provider reads its value from the process environment.
func renderConfig(providerURL, keyEnv, model, workspace string, overrides map[string]any) (string, error) {
	var b strings.Builder
	b.WriteString("version = 1\n\n")
	b.WriteString("[providers.eval]\n")
	for _, field := range []struct {
		key   string
		value any
	}{
		{key: "kind", value: "openai_compat"},
		{key: "base_url", value: providerURL},
		{key: "api_key_env", value: keyEnv},
		{key: "timeout", value: "300s"},
	} {
		text, err := tomlValue(field.value)
		if err != nil {
			return "", fmt.Errorf("provider %s: %w", field.key, err)
		}
		fmt.Fprintf(&b, "%s = %s\n", field.key, text)
	}

	b.WriteString("\n[agents.eval]\n")
	for _, field := range []struct {
		key   string
		value any
	}{
		{key: "provider", value: "eval"},
		{key: "model", value: model},
		{key: "workspace", value: workspace},
		{key: "tools", value: []string{
			"read_file", "write_file", "edit_file", "glob", "grep", "bash", "web_fetch",
		}},
		{key: "permission", value: "trust"},
	} {
		text, err := tomlValue(field.value)
		if err != nil {
			return "", fmt.Errorf("agent %s: %w", field.key, err)
		}
		fmt.Fprintf(&b, "%s = %s\n", field.key, text)
	}

	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !validTomlKey(key) {
			return "", fmt.Errorf("invalid agent override key %q", key)
		}
		text, err := tomlValue(overrides[key])
		if err != nil {
			return "", fmt.Errorf("agent override %s: %w", key, err)
		}
		fmt.Fprintf(&b, "%s = %s\n", key, text)
	}
	return b.String(), nil
}

// validTomlKey reports whether key is a bare TOML key.
func validTomlKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// tomlValue renders a scalar or string array as a TOML value. JSON string
// escaping is a valid subset of TOML basic-string escaping.
func tomlValue(value any) (string, error) {
	switch value.(type) {
	case string, bool, int, int64, float64, []string:
		data, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		return string(data), nil
	default:
		return "", fmt.Errorf("unsupported value type %T", value)
	}
}

// runTask runs one eval task in its own temporary home and workspace. A
// returned error is a setup error (exit 1); provider and check failures are
// recorded in the result instead.
func runTask(ctx context.Context, task evalTask, cfg runConfig) (result taskResult, err error) {
	result = taskResult{Name: task.Name, ToolCalls: map[string]int{}}

	home, err := os.MkdirTemp("", "lana-eval-home-*")
	if err != nil {
		return result, fmt.Errorf("create temp home: %w", err)
	}
	if cfg.keep {
		if cfg.stderr != nil {
			fmt.Fprintf(cfg.stderr, "%s: keeping home %s\n", task.Name, home)
		}
	} else {
		defer os.RemoveAll(home)
	}

	workspace, err := os.MkdirTemp("", "lana-eval-workspace-*")
	if err != nil {
		return result, fmt.Errorf("create temp workspace: %w", err)
	}
	if cfg.keep {
		if cfg.stderr != nil {
			fmt.Fprintf(cfg.stderr, "%s: keeping workspace %s\n", task.Name, workspace)
		}
	} else {
		defer os.RemoveAll(workspace)
	}

	prompt, setup, check := task.Prompt, task.Setup, task.Check
	if strings.Contains(prompt+setup+check, "{{port}}") {
		port, portErr := freePort()
		if portErr != nil {
			return result, fmt.Errorf("get free port: %w", portErr)
		}
		token := strconv.Itoa(port)
		prompt = strings.ReplaceAll(prompt, "{{port}}", token)
		setup = strings.ReplaceAll(setup, "{{port}}", token)
		check = strings.ReplaceAll(check, "{{port}}", token)
	}

	if err := writeTaskFiles(workspace, task.Files); err != nil {
		return result, err
	}
	if strings.TrimSpace(setup) != "" {
		output, setupErr := runShell(ctx, workspace, setup, commandTimeout)
		if setupErr != nil {
			return result, commandError(task.Name+" setup", setupErr, output)
		}
	}

	configText, err := renderConfig(cfg.providerURL, cfg.keyEnv, cfg.model, workspace, task.Agent)
	if err != nil {
		return result, fmt.Errorf("render config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configText), 0o600); err != nil {
		return result, fmt.Errorf("write config: %w", err)
	}

	rt, err := lana.New(ctx, lana.Options{Home: home})
	if err != nil {
		return result, fmt.Errorf("start runtime: %w", err)
	}
	defer func() {
		if closeErr := rt.Close(); closeErr != nil {
			if cfg.stderr != nil {
				fmt.Fprintf(cfg.stderr, "%s: close runtime: %v\n", task.Name, closeErr)
			}
			if err == nil {
				result.Status = statusError
				result.Error = joinError(result.Error, "close runtime: "+closeErr.Error())
			}
		}
	}()

	timeout := task.Timeout
	if timeout <= 0 {
		timeout = cfg.timeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	turnStart := time.Now()
	turn, runErr := rt.Run(runCtx, lana.Request{Agent: "eval", Prompt: prompt})
	result.DurationMS = time.Since(turnStart).Milliseconds()
	cancel()

	// The check runs before rt.Close so background jobs are still alive
	// (REQ-TRL-021).
	checkOutput, checkErr := runShell(ctx, workspace, check, commandTimeout)
	result.CheckOutput = tailText(checkOutput, 2000)

	metricsFailed := false
	if turn.SessionID != "" {
		events, historyErr := rt.History(ctx, turn.SessionID)
		if historyErr != nil {
			metricsFailed = true
			result.Error = joinError(result.Error, "read events: "+historyErr.Error())
		} else {
			metrics, metricsErr := computeMetrics(events)
			if metricsErr != nil {
				metricsFailed = true
				result.Error = joinError(result.Error, "compute metrics: "+metricsErr.Error())
			} else {
				applyMetrics(&result, metrics)
			}
		}
	} else {
		metricsFailed = true
		result.Error = joinError(result.Error, "no session for the turn")
	}
	if result.TurnEnd == "" {
		result.TurnEnd = turn.StopReason
	}
	result.CostUSD = cfg.prices.cost(result.InputTokens, result.CachedTokens, result.OutputTokens)

	switch {
	case runErr != nil || metricsFailed:
		result.Status = statusError
		if runErr != nil {
			result.Error = joinError(result.Error, runErr.Error())
		}
	case checkErr != nil:
		result.Status = statusFail
		result.Error = joinError(result.Error, "check: "+checkErr.Error())
	default:
		result.Status = statusPass
	}
	return result, nil
}

// applyMetrics copies event-derived metrics into a report row.
func applyMetrics(result *taskResult, metrics taskMetrics) {
	result.Steps = metrics.Steps
	result.ToolCalls = metrics.ToolCalls
	result.ToolErrors = metrics.ToolErrors
	result.BashFileOps = metrics.BashFileOps
	result.Reminders = metrics.Reminders
	result.Compactions = metrics.Compactions
	result.InputTokens = metrics.InputTokens
	result.CachedTokens = metrics.CachedTokens
	result.OutputTokens = metrics.OutputTokens
	result.CacheHitPct = metrics.CacheHitPct
	result.TurnEnd = metrics.TurnEnd
}

// writeTaskFiles writes a task's initial files into the workspace.
func writeTaskFiles(workspace string, files map[string]string) error {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(workspace, filepath.FromSlash(name))
		if !strings.HasPrefix(path, workspace+string(os.PathSeparator)) {
			return fmt.Errorf("task file %q escapes the workspace", name)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create directory for %s: %w", name, err)
		}
		if err := os.WriteFile(path, []byte(files[name]), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}

// runShell runs command with sh -c in dir.
func runShell(ctx context.Context, dir, command string, timeout time.Duration) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", nil
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "sh", "-c", command)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil && runCtx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("timed out after %s", timeout)
	}
	return string(output), err
}

// commandError adds the output tail to a setup error.
func commandError(label string, err error, output string) error {
	tail := strings.TrimSpace(tailText(output, 2000))
	if tail == "" {
		return fmt.Errorf("%s: %w", label, err)
	}
	return fmt.Errorf("%s: %w: %s", label, err, tail)
}

// joinError joins two error messages, skipping empty parts.
func joinError(current, next string) string {
	if current == "" {
		return next
	}
	if next == "" {
		return current
	}
	return current + "; " + next
}

// freePort returns a free TCP port on 127.0.0.1.
func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener address %T", listener.Addr())
	}
	return address.Port, nil
}
