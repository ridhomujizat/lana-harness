package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

var version = "dev"

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		help()
		return 0
	}
	switch args[0] {
	case "-V", "--version", "version":
		fmt.Printf("lana %s\n", version)
		return 0
	case "-h", "--help", "help":
		help()
		return 0
	case "init":
		return cmdInit(args[1:])
	case "run":
		return cmdRun(args[1:])
	case "config":
		return cmdConfig(args[1:])
	case "chat":
		return cmdChat(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "error: perintah tidak dikenal: %s\n", args[0])
		return 2
	}
}

func help() {
	fmt.Print("lana - agen coding dan personal\n\n  lana init [--force] [--yes]\n  lana run [--agent <nama>] [--output text|json|stream-json] [--ask] [prompt...]\n  lana config check|path|edit\n  lana chat [--agent <nama>] [--yolo]\n  lana --version\n")
}

func homeDir() (string, error) { return config.HomeDir(nil) }

func cmdInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	force := fs.Bool("force", false, "timpa file")
	yes := fs.Bool("yes", false, "default")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*yes && !isTTY(os.Stdin) {
		fmt.Fprintln(os.Stderr, "error: lana init butuh terminal; pakai --yes untuk nilai default")
		return 2
	}
	home, err := homeDir()
	if err != nil {
		return fail(err, 1)
	}
	provider, keyEnv, model := "openai", "OPENAI_API_KEY", "gpt-5-mini"
	if !*yes {
		r := bufio.NewReader(os.Stdin)
		provider = ask(r, "Provider", "openai", "openai")
		if provider == "openrouter" {
			keyEnv, model = "OPENROUTER_API_KEY", "openai/gpt-5-mini"
		}
		keyEnv = ask(r, "Env API key", keyEnv, "OPENAI_API_KEY")
		model = ask(r, "Model", model, "gpt-5-mini")
	}
	base := "https://api.openai.com/v1"
	if provider == "openrouter" {
		base = "https://openrouter.ai/api/v1"
	}
	cfgPath := filepath.Join(home, "config.toml")
	promptPath := filepath.Join(home, "agents", "default.md")
	secretsPath := filepath.Join(home, "secrets.env")
	for _, path := range []string{cfgPath, promptPath, secretsPath} {
		if _, err := os.Stat(path); err == nil && !*force {
			fmt.Fprintf(os.Stderr, "error: file sudah ada: %s; pakai --force untuk menimpa\n", path)
			return 2
		}
	}
	cfg := fmt.Sprintf("[providers.%s]\nkind = \"openai_compat\"\nbase_url = %q\napi_key_env = %q\n\n[agents.default]\nprovider = %q\nmodel = %q\nprompt_file = \"agents/default.md\"\ntools = [\"read_file\", \"write_file\", \"edit_file\", \"glob\", \"grep\", \"bash\"]\npermission = \"ask\"\n\n[[routes]]\nchannel = \"cli\"\nagent = \"default\"\n", provider, base, keyEnv, provider, model)
	for path, data := range map[string]struct {
		text string
		mode os.FileMode
	}{
		cfgPath:     {cfg, 0o644},
		promptPath:  {"Kamu adalah asisten coding dan personal yang ringkas dan teliti.\n", 0o644},
		secretsPath: {"# Isi API key di sini, atau set sebagai env di shell.\n# " + keyEnv + "=\n", 0o600},
	} {
		if err := atomicWrite(path, []byte(data.text), data.mode); err != nil {
			return fail(err, 1)
		}
	}
	fmt.Printf("✓ dibuat %s\n✓ dibuat %s\n✓ dibuat %s (0600)\n", cfgPath, promptPath, secretsPath)
	fmt.Printf("· isi %s di %s atau env shell, lalu jalankan: lana chat\n", keyEnv, secretsPath)
	return 0
}

func ask(r *bufio.Reader, label, def, fallback string) string {
	fmt.Printf("%s [%s]: ", label, def)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		if def != "" {
			return def
		}
		return fallback
	}
	return line
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".lana-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func cmdConfig(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: pakai lana config check|path|edit")
		return 2
	}
	home, err := homeDir()
	if err != nil {
		return fail(err, 1)
	}
	switch args[0] {
	case "check":
		result, err := config.Load(context.Background(), config.Options{Home: home})
		if err != nil {
			var errs config.Errors
			if errors.As(err, &errs) {
				for _, item := range errs {
					fmt.Fprintf(os.Stderr, "error: %s\n", item.Error())
				}
				return 2
			}
			return fail(err, 2)
		}
		fmt.Printf("✓ config valid (%d lapisan, %d agent, %d provider)\n", len(result.Layers), len(result.Config.Agents), len(result.Config.Providers))
		return 0
	case "path":
		fmt.Printf("LANA_HOME  %s\n", home)
		for _, item := range []string{"config.toml", "profiles/minimal.toml", "secrets.env", "lana.db", "logs/lana.log"} {
			path := filepath.Join(home, filepath.FromSlash(item))
			if _, err := os.Stat(path); err == nil {
				fmt.Printf("✓ %s\n", path)
			} else {
				fmt.Printf("· %s (tidak ada)\n", path)
			}
		}
		return 0
	case "edit":
		editor := os.Getenv("VISUAL")
		if editor == "" {
			editor = os.Getenv("EDITOR")
		}
		if editor == "" {
			fmt.Fprintln(os.Stderr, "error: set $EDITOR untuk memakai lana config edit")
			return 2
		}
		path := filepath.Join(home, "config.toml")
		if _, err := os.Stat(path); err != nil {
			fmt.Fprintf(os.Stderr, "error: konfigurasi belum ada di %s; jalankan 'lana init'\n", path)
			return 2
		}
		parts := strings.Fields(editor)
		cmd := exec.Command(parts[0], append(parts[1:], path)...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		return cmdConfig([]string{"check"})
	default:
		return 2
	}
}

func runtimeFor(ctx context.Context, home, agentName string, ask bool) (*lana.Runtime, string, string, error) {
	result, err := config.Load(ctx, config.Options{Home: home})
	if err != nil {
		return nil, "", "", err
	}
	providerName, model := "", ""
	if agent, ok := result.Config.Agents[agentName]; ok {
		providerName, model = agent.Provider, agent.Model
	}
	opts := lana.Options{Home: home}
	var options []lana.Option
	if ask {
		options = append(options, lana.ApproveWith(lineAnswerer))
	}
	if providerName != "" {
		if provider, ok := result.Config.Providers[providerName]; ok && provider.APIKeyEnv != "" {
			if os.Getenv(provider.APIKeyEnv) == "" && !secretInFile(home, provider.APIKeyEnv) {
				text := "Halo dari MockLlm."
				layer := fmt.Sprintf("[providers.mock]\nkind = \"openai_compat\"\nbase_url = \"mock://local\"\n\n[agents.%s]\nprovider = \"mock\"\nmodel = \"mock\"\n\n[[routes]]\nchannel = \"cli\"\nagent = %q\n\n[[routes]]\nchannel = \"library\"\nagent = %q\n", agentName, agentName, agentName)
				opts.Layers = []config.Layer{{Name: "mock", TOML: []byte(layer)}}
				opts.Providers = map[string]core.Provider{"mock": mock.New(mock.Script{Responses: []mock.Response{{Events: []mock.Event{{Text: &text}, {Stop: ptr("end_turn")}}}}})}
				providerName, model = "mock", "mock"
			}
		}
	}
	rt, err := lana.New(ctx, opts, options...)
	return rt, providerName, model, err
}

func secretInFile(home, name string) bool {
	data, err := os.ReadFile(filepath.Join(home, "secrets.env"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), name+"=") {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), name+"=")) != ""
		}
	}
	return false
}

func lineAnswerer(ctx context.Context, req core.ApprovalRequest) core.ApprovalOutcome {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return core.OutcomeUnavailable
	}
	defer func() { _ = tty.Close() }()
	_, _ = fmt.Fprintf(tty, "approve %s [y/a/n]: ", req.ToolName)
	line, _ := bufio.NewReader(tty).ReadString('\n')
	switch strings.TrimSpace(line) {
	case "y":
		return core.OutcomeAllowedOnce
	case "a":
		return core.OutcomeAllowedSession
	}
	return core.OutcomeRejected
}

type runOutput struct {
	Type       string      `json:"type"`
	SessionID  string      `json:"session_id"`
	Agent      string      `json:"agent"`
	Model      string      `json:"model"`
	Cwd        string      `json:"cwd"`
	StopReason string      `json:"stop_reason"`
	Text       string      `json:"text"`
	Usage      usageOutput `json:"usage"`
	DurationMS int64       `json:"duration_ms"`
	ExitCode   int         `json:"exit_code"`
	Error      *runError   `json:"error"`
}
type usageOutput struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
type runError struct {
	Class   string `json:"class"`
	Message string `json:"message"`
}

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	agentFlag := fs.String("agent", "", "nama agent")
	output := fs.String("output", "text", "text|json|stream-json")
	fs.StringVar(output, "o", "text", "text|json|stream-json")
	ask := fs.Bool("ask", false, "approval")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	prompt := strings.Join(fs.Args(), " ")
	if !isTTY(os.Stdin) {
		if data, err := io.ReadAll(os.Stdin); err == nil {
			if strings.TrimSpace(prompt) != "" {
				prompt += "\n\n"
			}
			prompt += string(data)
		}
	}
	if strings.TrimSpace(prompt) == "" {
		fmt.Fprintln(os.Stderr, "error: prompt kosong; berikan argumen atau pipe lewat stdin")
		return 2
	}
	cwd, _ := os.Getwd()
	home, err := homeDir()
	if err != nil {
		return fail(err, 1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	agentName := *agentFlag
	if agentName == "" {
		if result, err := config.Load(ctx, config.Options{Home: home}); err == nil {
			for _, route := range result.Config.Routes {
				if route.Channel == "cli" {
					agentName = route.Agent
					break
				}
			}
		}
		if agentName == "" {
			agentName = "default"
		}
	}
	rt, providerName, model, err := runtimeFor(ctx, home, agentName, *ask)
	if err != nil {
		var errs config.Errors
		if errors.As(err, &errs) {
			for _, item := range errs {
				fmt.Fprintf(os.Stderr, "error: %s\n", item.Error())
			}
			return 2
		}
		return fail(err, 2)
	}
	defer func() { _ = rt.Close() }()
	start := time.Now()
	result, runErr := rt.Run(ctx, lana.Request{Agent: agentName, Prompt: prompt})
	duration := time.Since(start).Milliseconds()
	exitCode := 0
	var payloadError *runError
	if runErr != nil {
		var pe *lana.ProviderError
		switch {
		case errors.As(runErr, &pe):
			exitCode, payloadError = 3, &runError{Class: "provider", Message: pe.Message}
		case errors.Is(runErr, context.Canceled) || result.StopReason == lana.StopCancelled:
			exitCode = 130
		default:
			exitCode, payloadError = 1, &runError{Class: "runtime", Message: runErr.Error()}
		}
	}
	if result.StopReason == lana.StopMaxSteps {
		exitCode = 4
	}
	if result.StopReason == lana.StopCancelled && exitCode == 0 {
		exitCode = 130
	}
	value := runOutput{Type: "result", SessionID: result.SessionID, Agent: agentName, Model: providerName + ":" + model, Cwd: cwd, StopReason: result.StopReason, Text: result.Text, Usage: usageOutput{InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens}, DurationMS: duration, ExitCode: exitCode, Error: payloadError}
	switch *output {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(value)
	case "stream-json":
		enc := json.NewEncoder(os.Stdout)
		_ = enc.Encode(map[string]any{"type": "session", "session_id": result.SessionID, "agent": agentName, "model": value.Model, "cwd": cwd})
		_ = enc.Encode(value)
	default:
		fmt.Println(result.Text)
	}
	if exitCode == 0 {
		return 0
	}
	if result.StopReason == lana.StopMaxSteps {
		fmt.Fprintln(os.Stderr, "error: turn berhenti karena max_steps")
	} else if runErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", runErr)
	}
	return exitCode
}

func cmdChat(args []string) int {
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	agentFlag := fs.String("agent", "", "agent")
	yolo := fs.Bool("yolo", false, "trust")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !isTTY(os.Stdin) || !isTTY(os.Stdout) {
		fmt.Fprintln(os.Stderr, "error: lana chat butuh terminal")
		return 2
	}
	home, err := homeDir()
	if err != nil {
		return fail(err, 1)
	}
	agentName := *agentFlag
	if agentName == "" {
		agentName = "default"
	}
	rt, _, _, err := runtimeFor(context.Background(), home, agentName, !*yolo)
	if err != nil {
		return fail(err, 2)
	}
	defer func() { _ = rt.Close() }()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *yolo {
		fmt.Println("YOLO: semua tool diizinkan.")
	}
	reader := bufio.NewReader(os.Stdin)
	sessionID := ""
	for {
		fmt.Print("> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return 0
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "/exit" || line == "/quit" {
			return 0
		}
		result, err := rt.Run(ctx, lana.Request{Agent: agentName, Prompt: line, SessionID: sessionID})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}
		sessionID = result.SessionID
		fmt.Println(result.Text)
	}
}

func isTTY(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
func fail(err error, code int) int {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	return code
}
func ptr(s string) *string { return &s }
