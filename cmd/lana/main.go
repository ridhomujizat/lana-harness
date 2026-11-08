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
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/channels/cli/tui"
	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/models"
	"github.com/ridhomujizat/lana-harness/providers/mock"
	"github.com/ridhomujizat/lana-harness/router"
)

var version = "dev"

// commit is the git commit the binary was built from; it is set at build time
// via -X main.commit=....
var commit = ""

// buildDate is the time the binary was built; it is set at build time via
// -X main.buildDate=....
var buildDate = ""

// versionString renders the version line. commit and buildDate are added only
// when set, and buildDate never appears on its own.
func versionString() string {
	switch {
	case commit != "" && buildDate != "":
		return fmt.Sprintf("lana %s (%s, %s)", version, commit, buildDate)
	case commit != "":
		return fmt.Sprintf("lana %s (%s)", version, commit)
	default:
		return fmt.Sprintf("lana %s", version)
	}
}

func main() {
	setupTerminalLogging()
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		help()
		return 0
	}
	switch args[0] {
	case "-V", "--version", "version":
		fmt.Println(versionString())
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
	case "agent":
		return cmdAgent(args[1:])
	case "mcp":
		return cmdMCP(args[1:])
	case "skill":
		return cmdSkill(args[1:])
	case "daemon":
		return cmdDaemon(args[1:])
	case "web":
		return cmdWeb(args[1:])
	case "update":
		return cmdUpdate(args[1:])
	case "update-probe":
		// Hidden: the update job runs it against a copy of the data
		// (API-UPD-010).
		return cmdUpdateProbe(args[1:])
	case "update-watch":
		// Hidden: the old binary starts it to guard the first start of a new
		// one (API-UPD-010).
		return cmdUpdateWatch(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "error: unknown command: %s\n", args[0])
		return 2
	}
}

func help() {
	// The logo is for people at a terminal; piped help stays plain text.
	if term.IsTerminal(os.Stdout.Fd()) {
		fmt.Print("\n" + tui.LogoText() + "\n")
	}
	fmt.Print("lana - personal coding agent\n\n  lana init [--force] [--yes] [--provider openai|openrouter|deepseek|anthropic|claude-code] [--fallback <provider>/<model>]\n  lana run [--agent <name>] [--output text|json|stream-json] [--ask] [prompt...]\n  lana config check|path|edit\n  lana chat [--agent <name>] [--yolo] [--continue] [--resume <id>] [--verbose]\n  lana agent new <name> [--template assistant|worker|coder] [--model provider:model] [--yes]\n  lana agent list | edit <name> [persona|user|memory] | show <name> [--prompt]\n  lana mcp list | tools <server>\n  lana skill list | new <name> | show <name> [--agent <name>]\n  lana daemon\n  lana web reset-login\n  lana update rollback\n  lana --version\n")
}

func homeDir() (string, error) { return config.HomeDir(nil) }

// providerPreset holds the wizard's default values for one provider. The
// provider table uses the preset kind of the same name (API-RTR-001), so it
// needs no base_url. claude-code is the built-in Claude Code account: it has
// no provider table and no key (API-CCT-001).
type providerPreset struct {
	name   string
	keyEnv string
	model  string
}

// claudeCodePreset is the preset name of the built-in Claude Code account.
const claudeCodePreset = core.ClaudeCodeProvider

// initComboName is the combo `lana init --fallback` writes: [model, fallback].
const initComboName = "default"

var providerPresets = []providerPreset{
	{name: "openai", keyEnv: "OPENAI_API_KEY", model: models.TestModel("openai")},
	{name: "openrouter", keyEnv: "OPENROUTER_API_KEY", model: models.TestModel("openrouter")},
	{name: "deepseek", keyEnv: "DEEPSEEK_API_KEY", model: models.TestModel("deepseek")},
	{name: "anthropic", keyEnv: "ANTHROPIC_API_KEY", model: defaultAnthropicModel()},
	{name: claudeCodePreset, model: "sonnet"},
}

// defaultAnthropicModel is the first Sonnet model of the Anthropic picker:
// a capable default for a coding agent, cheaper than Opus.
func defaultAnthropicModel() string {
	for _, model := range models.ForKind("anthropic") {
		if strings.HasPrefix(model.ID, "claude-sonnet-") {
			return model.ID
		}
	}
	return models.TestModel("anthropic")
}

func lookupProvider(name string) (providerPreset, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, preset := range providerPresets {
		if preset.name == name {
			return preset, true
		}
	}
	return providerPreset{}, false
}

func providerNames() string {
	names := make([]string, 0, len(providerPresets))
	for _, preset := range providerPresets {
		names = append(names, preset.name)
	}
	return strings.Join(names, ", ")
}

// initProvider is one provider `lana init` writes: a preset with the key
// variable the owner chose.
type initProvider struct {
	preset providerPreset
	keyEnv string
	apiKey string
}

// initPlan is what `lana init` writes: the providers with a table, the main
// model address, and an optional fallback address (the default combo).
type initPlan struct {
	providers []initProvider
	model     string
	fallback  string
}

// splitInitAddress splits "<provider>/<model>" of a preset provider.
func splitInitAddress(address string) (providerPreset, string, error) {
	address = strings.TrimSpace(address)
	slash := strings.Index(address, "/")
	if slash <= 0 || slash == len(address)-1 {
		return providerPreset{}, "", fmt.Errorf("fallback %q must be <provider>/<model>", address)
	}
	preset, ok := lookupProvider(address[:slash])
	if !ok {
		return providerPreset{}, "", fmt.Errorf("unknown provider %q in fallback (choices: %s)", address[:slash], providerNames())
	}
	return preset, address[slash+1:], nil
}

// addProvider adds the table of a preset once; claude-code has none.
func (p *initPlan) addProvider(preset providerPreset, keyEnv, apiKey string) {
	if preset.name == claudeCodePreset {
		return
	}
	for _, provider := range p.providers {
		if provider.preset.name == preset.name {
			return
		}
	}
	p.providers = append(p.providers, initProvider{preset: preset, keyEnv: keyEnv, apiKey: apiKey})
}

func (p *initPlan) claudeCodeMain() bool {
	return strings.HasPrefix(p.model, claudeCodePreset+"/")
}

func cmdInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	force := fs.Bool("force", false, "overwrite file")
	yes := fs.Bool("yes", false, "accept defaults")
	providerFlag := fs.String("provider", "openai", "provider: "+providerNames())
	fallbackFlag := fs.String("fallback", "", "fallback model <provider>/<model>; makes the default combo")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*yes && !isTTY(os.Stdin) {
		fmt.Fprintln(os.Stderr, "error: lana init needs a terminal; use --yes for default values")
		return 2
	}
	home, err := homeDir()
	if err != nil {
		return fail(err, 1)
	}
	preset, ok := lookupProvider(*providerFlag)
	if !ok {
		fmt.Fprintf(os.Stderr, "error: unknown provider %q (choices: %s)\n", *providerFlag, providerNames())
		return 2
	}
	keyEnv, model, apiKey, fallback := preset.keyEnv, preset.model, "", strings.TrimSpace(*fallbackFlag)
	if !*yes {
		fmt.Printf("Provider: %s\n", providerNames())
		selected, ok := lookupProvider(ask("Provider", preset.name))
		if !ok {
			fmt.Fprintf(os.Stderr, "error: unknown provider (choices: %s)\n", providerNames())
			return 2
		}
		preset = selected
		keyEnv = preset.keyEnv
		if preset.name != claudeCodePreset {
			keyEnv = ask("Env API key", preset.keyEnv)
		}
		model = ask("Model", preset.model)
		if preset.name != claudeCodePreset {
			apiKey = askKey("API key " + keyEnv)
		}
		fallback = askOptional("Fallback model <provider>/<model>", fallback)
	}
	plan := initPlan{model: preset.name + "/" + strings.TrimSpace(model)}
	plan.addProvider(preset, keyEnv, apiKey)
	if fallback != "" {
		fallbackPreset, fallbackModel, err := splitInitAddress(fallback)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 2
		}
		plan.fallback = fallbackPreset.name + "/" + fallbackModel
		if plan.fallback == plan.model {
			fmt.Fprintln(os.Stderr, "error: the fallback must differ from the main model")
			return 2
		}
		fallbackKey := ""
		if !*yes && fallbackPreset.name != claudeCodePreset && fallbackPreset.name != preset.name {
			fallbackKey = askKey("API key " + fallbackPreset.keyEnv)
		}
		plan.addProvider(fallbackPreset, fallbackPreset.keyEnv, fallbackKey)
	}
	cfgPath := filepath.Join(home, "config.toml")
	promptPath := filepath.Join(home, "agents", "default.md")
	secretsPath := filepath.Join(home, "secrets.env")
	for _, path := range []string{cfgPath, promptPath, secretsPath} {
		if _, err := os.Stat(path); err == nil && !*force {
			fmt.Fprintf(os.Stderr, "error: file already exists: %s; use --force to overwrite\n", path)
			return 2
		}
	}
	cfgText := initConfig(plan)
	if _, err := config.Check(context.Background(), home, []byte(cfgText)); err != nil {
		var errs config.Errors
		if errors.As(err, &errs) {
			for _, item := range errs {
				fmt.Fprintf(os.Stderr, "error: %s\n", item.Error())
			}
			return 2
		}
		return fail(err, 2)
	}
	for path, data := range map[string]struct {
		text string
		mode os.FileMode
	}{
		cfgPath:     {cfgText, 0o644},
		promptPath:  {"You are a concise, careful personal coding assistant.\n", 0o644},
		secretsPath: {initSecretsText(plan.providers), 0o600},
	} {
		if err := atomicWrite(path, []byte(data.text), data.mode); err != nil {
			return fail(err, 1)
		}
	}
	savedKey := false
	var missing []string
	for _, provider := range plan.providers {
		if provider.apiKey != "" {
			savedKey = true
		} else {
			missing = append(missing, provider.keyEnv)
		}
	}
	secretsNote := " (0600)"
	if savedKey {
		secretsNote = " (0600, API key saved)"
	}
	fmt.Printf("✓ created %s\n✓ created %s\n✓ created %s%s\n", cfgPath, promptPath, secretsPath, secretsNote)
	if plan.claudeCodeMain() || strings.HasPrefix(plan.fallback, claudeCodePreset+"/") {
		fmt.Println("· Sign in once with `claude auth login` if you haven't")
	}
	if len(missing) > 0 {
		fmt.Printf("· set %s in %s or as a shell env var, then run: lana chat\n", strings.Join(missing, ", "), secretsPath)
		return 0
	}
	fmt.Println("· run: lana chat")
	return 0
}

// initConfig renders the router-form config of `lana init`: one table per
// provider with its preset kind, the default combo when there is a fallback,
// and the agent with model = "<provider>/<model>" (or "default") and no
// provider key. A Claude Code main model with an API fallback runs the
// background passes on the fallback ([router] background_model, REQ-CCT-007).
func initConfig(plan initPlan) string {
	var b strings.Builder
	for _, provider := range plan.providers {
		fmt.Fprintf(&b, "[providers.%s]\nkind = %q\napi_key_env = %q\n\n", provider.preset.name, provider.preset.name, provider.keyEnv)
	}
	agentModel := plan.model
	if plan.fallback != "" {
		fmt.Fprintf(&b, "[combos.%s]\nmodels = [%q, %q]\n\n", initComboName, plan.model, plan.fallback)
		agentModel = initComboName
	}
	fmt.Fprintf(&b, "[agents.default]\nmodel = %q\nprompt_file = \"agents/default.md\"\ntools = [\"read_file\", \"write_file\", \"edit_file\", \"glob\", \"grep\", \"bash\"]\npermission = \"ask\"\n\n", agentModel)
	if plan.claudeCodeMain() && plan.fallback != "" && !strings.HasPrefix(plan.fallback, claudeCodePreset+"/") {
		fmt.Fprintf(&b, "[router]\nbackground_model = %q\n\n", plan.fallback)
	}
	b.WriteString("[[routes]]\nchannel = \"cli\"\nagent = \"default\"\n")
	return b.String()
}

// initSecretsText is the first secrets.env: one line per provider key.
func initSecretsText(providers []initProvider) string {
	if len(providers) == 0 {
		return "# API keys of the providers in config.toml; do not commit.\n"
	}
	var b strings.Builder
	for i, provider := range providers {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(secretsText(provider.keyEnv, provider.apiKey))
	}
	return b.String()
}

func secretsText(keyEnv, key string) string {
	if key == "" {
		return "# Put the API key here, or set it as an env var in your shell.\n# " + keyEnv + "=\n"
	}
	return "# Provider API key; do not commit.\n" + keyEnv + "=" + key + "\n"
}

// askOptional asks for a value that may stay empty.
func askOptional(label, def string) string {
	if def != "" {
		return ask(label, def)
	}
	fmt.Printf("%s (optional, leave empty for none): ", label)
	return readLine()
}

func ask(label, def string) string {
	fmt.Printf("%s [%s]: ", label, def)
	line := readLine()
	if line == "" {
		return def
	}
	return line
}

func askKey(label string) string {
	fmt.Printf("%s (optional, leave empty for later): ", label)
	if isTTY(os.Stdin) {
		data, err := term.ReadPassword(os.Stdin.Fd())
		fmt.Println()
		if err == nil {
			return strings.Trim(strings.TrimSpace(string(data)), "\"'")
		}
	}
	return readLine()
}

// readLine reads one line from stdin without over-buffering, so the next
// prompt (e.g. ReadPassword) does not lose input.
func readLine() string {
	var buf []byte
	one := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(one)
		if n > 0 {
			if one[0] == '\n' {
				break
			}
			if one[0] != '\r' {
				buf = append(buf, one[0])
			}
		}
		if err != nil {
			break
		}
	}
	return strings.TrimSpace(string(buf))
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
		fmt.Fprintln(os.Stderr, "error: use lana config check|path|edit")
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
		fmt.Printf("✓ config valid (%d layers, %d agents, %d providers)\n", len(result.Layers), len(result.Config.Agents), len(result.Config.Providers))
		names := make([]string, 0, len(result.Config.Providers))
		for name := range result.Config.Providers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			env := router.EffectiveKeyEnv(result.Config.Providers[name], name)
			if env == "" {
				continue
			}
			if secret, ok := result.Secrets.Lookup(env); ok && secret.Reveal() != "" {
				fmt.Printf("· %s set (provider %s)\n", env, name)
			} else {
				fmt.Printf("· %s is not set (provider %s)\n", env, name)
			}
		}
		for _, warning := range agentSizeWarnings(home, result.Config) {
			fmt.Println(warning)
		}
		for _, warning := range skillWarnings(home, result.Config) {
			fmt.Println(warning)
		}
		return 0
	case "path":
		fmt.Printf("LANA_HOME  %s\n", home)
		for _, item := range []string{"config.toml", "profiles/minimal.toml", "secrets.env", "lana.db", "logs/lana.log"} {
			path := filepath.Join(home, filepath.FromSlash(item))
			if _, err := os.Stat(path); err == nil {
				fmt.Printf("✓ %s\n", path)
			} else {
				fmt.Printf("· %s (missing)\n", path)
			}
		}
		return 0
	case "edit":
		editor := os.Getenv("VISUAL")
		if editor == "" {
			editor = os.Getenv("EDITOR")
		}
		if editor == "" {
			fmt.Fprintln(os.Stderr, "error: set $EDITOR to use lana config edit")
			return 2
		}
		path := filepath.Join(home, "config.toml")
		if _, err := os.Stat(path); err != nil {
			fmt.Fprintf(os.Stderr, "error: config does not exist yet at %s; run 'lana init'\n", path)
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

func runtimeFor(ctx context.Context, home, agentName string, approve lana.ApproveFunc, extra ...lana.Option) (*lana.Runtime, string, string, string, error) {
	result, err := config.Load(ctx, config.Options{Home: home})
	if err != nil {
		return nil, "", "", "", err
	}
	providerName, model := "", ""
	if agent, ok := result.Config.Agents[agentName]; ok {
		providerName, model = primaryTarget(result.Config, agent)
	}
	opts := lana.Options{Home: home}
	var options []lana.Option
	if approve != nil {
		options = append(options, lana.ApproveWith(approve))
	}
	missingEnv := ""
	if providerName != "" {
		if env := router.EffectiveKeyEnv(result.Config.Providers[providerName], providerName); env != "" {
			if os.Getenv(env) == "" && !secretInFile(home, env) {
				text := "Hello from MockLlm. The provider is not configured, so this is a sample answer. Set the provider's API key to use a real model."
				layer := fmt.Sprintf("[providers.mock]\nkind = \"openai_compat\"\nbase_url = \"mock://local\"\n\n[agents.%s]\nprovider = \"mock\"\nmodel = \"mock\"\n\n[[routes]]\nchannel = \"cli\"\nagent = %q\n\n[[routes]]\nchannel = \"library\"\nagent = %q\n", agentName, agentName, agentName)
				opts.Layers = []config.Layer{{Name: "mock", TOML: []byte(layer)}}
				responses := make([]mock.Response, 200)
				for i := range responses {
					responses[i] = mock.Response{Events: []mock.Event{{Text: &text}, {Stop: ptr("end_turn")}}}
				}
				opts.Providers = map[string]core.Provider{"mock": mock.New(mock.Script{Responses: responses})}
				providerName, model = "mock", "mock"
				missingEnv = env
			}
		}
	}
	options = append(options, extra...)
	rt, err := lana.New(ctx, opts, options...)
	return rt, providerName, model, missingEnv, err
}

// primaryTarget returns the provider and model of an agent's first target:
// the legacy provider and model, the first model of its combo, or its
// provider/model address.
func primaryTarget(cfg core.Config, agent core.AgentConfig) (string, string) {
	if agent.Provider != "" {
		return agent.Provider, agent.Model
	}
	value := agent.Model
	if combo, ok := cfg.Combos[value]; ok && len(combo.Models) > 0 {
		value = combo.Models[0]
	}
	if target, ok := router.ParseAddress(value); ok {
		return target.Provider, target.Model
	}
	return "", agent.Model
}

func secretInFile(home, name string) bool {
	data, err := os.ReadFile(filepath.Join(home, "secrets.env"))
	if err != nil {
		return false
	}
	// The last assignment wins, as in the config loader.
	value := ""
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), name+"=") {
			value = strings.Trim(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), name+"=")), "\"'")
		}
	}
	return value != ""
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

// lineQuestioner answers questions on /dev/tty with the API-ASK-007 line
// prompt. It is used by lana run --ask.
func lineQuestioner(_ context.Context, req core.QuestionRequest) core.QuestionOutcome {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return noQuestionResponder()
	}
	defer func() { _ = tty.Close() }()
	reader := bufio.NewReader(tty)
	answers := make([]core.QuestionAnswer, 0, len(req.Questions))
	for i, question := range req.Questions {
		answer, ok := lineQuestion(tty, reader, question, i+1, len(req.Questions))
		if !ok {
			return noQuestionResponder()
		}
		answers = append(answers, answer)
	}
	return core.QuestionOutcome{Status: core.QuestionStatusAnswered, Answers: answers}
}

func noQuestionResponder() core.QuestionOutcome {
	return core.QuestionOutcome{Status: core.QuestionStatusUnavailable, Reason: core.QuestionReasonNoResponder}
}

func lineQuestion(tty *os.File, reader *bufio.Reader, question core.Question, index, total int) (core.QuestionAnswer, bool) {
	renderLineQuestion(tty, question, index, total)
	if question.MultiSelect {
		return lineMultiSelect(tty, reader, question)
	}
	return lineSingleSelect(tty, reader, question)
}

func renderLineQuestion(tty *os.File, question core.Question, index, total int) {
	_, _ = fmt.Fprint(tty, "? ")
	if question.Header != "" {
		_, _ = fmt.Fprintf(tty, "[%s] ", question.Header)
	}
	_, _ = fmt.Fprintf(tty, "%s  (%d of %d", question.Text, index, total)
	if question.MultiSelect {
		_, _ = fmt.Fprint(tty, ", select all that apply")
	}
	_, _ = fmt.Fprintln(tty, ")")
	for i, option := range question.Options {
		if question.MultiSelect {
			_, _ = fmt.Fprint(tty, "  [ ] ")
		} else {
			_, _ = fmt.Fprint(tty, "  ")
		}
		if option.Description != "" {
			_, _ = fmt.Fprintf(tty, "%d) %s — %s\n", i+1, option.Label, option.Description)
		} else {
			_, _ = fmt.Fprintf(tty, "%d) %s\n", i+1, option.Label)
		}
	}
	_, _ = fmt.Fprintf(tty, "  %d) Something else\n", len(question.Options)+1)
	_, _ = fmt.Fprintln(tty, "  s) Skip")
}

func lineSingleSelect(tty *os.File, reader *bufio.Reader, question core.Question) (core.QuestionAnswer, bool) {
	other := len(question.Options) + 1
	for {
		_, _ = fmt.Fprintf(tty, "Choose 1-%d, or s to skip: ", other)
		line, ok := readQuestionLine(reader)
		if !ok {
			return core.QuestionAnswer{}, false
		}
		if strings.EqualFold(line, "s") {
			return core.QuestionAnswer{Question: question.Text, Skipped: true}, true
		}
		n, err := strconv.Atoi(line)
		if err != nil || n < 1 || n > other {
			_, _ = fmt.Fprintln(tty, "Choose one of the numbers shown.")
			continue
		}
		if n == other {
			text, ok := lineOther(tty, reader)
			if !ok {
				return core.QuestionAnswer{}, false
			}
			return core.QuestionAnswer{Question: question.Text, Other: text}, true
		}
		return core.QuestionAnswer{Question: question.Text, Selected: []string{question.Options[n-1].Label}}, true
	}
}

func lineMultiSelect(tty *os.File, reader *bufio.Reader, question core.Question) (core.QuestionAnswer, bool) {
	other := len(question.Options) + 1
	for {
		_, _ = fmt.Fprint(tty, "Choose numbers, e.g. 1 3, or s to skip: ")
		line, ok := readQuestionLine(reader)
		if !ok {
			return core.QuestionAnswer{}, false
		}
		if line == "" || strings.EqualFold(line, "s") {
			return core.QuestionAnswer{Question: question.Text, Skipped: true}, true
		}
		picked := map[int]bool{}
		valid := true
		for _, field := range strings.Fields(line) {
			n, err := strconv.Atoi(field)
			if err != nil || n < 1 || n > other {
				valid = false
				break
			}
			picked[n] = true
		}
		if !valid {
			_, _ = fmt.Fprintln(tty, "Choose one of the numbers shown.")
			continue
		}
		answer := core.QuestionAnswer{Question: question.Text}
		for i, option := range question.Options {
			if picked[i+1] {
				answer.Selected = append(answer.Selected, option.Label)
			}
		}
		if picked[other] {
			text, ok := lineOther(tty, reader)
			if !ok {
				return core.QuestionAnswer{}, false
			}
			answer.Other = text
		}
		return answer, true
	}
}

func lineOther(tty *os.File, reader *bufio.Reader) (string, bool) {
	for {
		_, _ = fmt.Fprint(tty, "Your answer: ")
		line, ok := readQuestionLine(reader)
		if !ok {
			return "", false
		}
		if line != "" {
			return line, true
		}
	}
}

func readQuestionLine(reader *bufio.Reader) (string, bool) {
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimSpace(line), true
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
	agentFlag := fs.String("agent", "", "agent name")
	output := fs.String("output", "text", "text|json|stream-json")
	fs.StringVar(output, "o", "text", "text|json|stream-json")
	ask := fs.Bool("ask", false, "ask for approval")
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
		fmt.Fprintln(os.Stderr, "error: prompt is empty; pass arguments or pipe via stdin")
		return 2
	}
	cwd, _ := os.Getwd()
	home, err := homeDir()
	if err != nil {
		return fail(err, 1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	agentName, err := loadAgent(ctx, home, *agentFlag)
	if err != nil {
		var errs config.Errors
		if errors.As(err, &errs) {
			for _, item := range errs {
				fmt.Fprintf(os.Stderr, "error: %s\n", item.Error())
			}
			return 2
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	var approve lana.ApproveFunc
	var questionOpts []lana.Option
	if *ask {
		approve = lineAnswerer
		questionOpts = append(questionOpts, lana.AskWith(lineQuestioner), lana.AskChannels("cli"))
	}
	rt, _, _, missingEnv, err := runtimeFor(ctx, home, agentName, approve, questionOpts...)
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
	if missingEnv != "" {
		fmt.Fprintf(os.Stderr, "· %s is not set; using MockLlm, not a real model\n", missingEnv)
	}
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
	// The served model: "provider:model" for a legacy agent, the router target
	// "provider/model" that answered, or "claude-agent/<model>" for a Claude
	// Agent (lana.Result.Model).
	runModel := result.Model
	if runModel == "" {
		runModel = rt.AgentModel(agentName)
	}
	value := runOutput{Type: "result", SessionID: result.SessionID, Agent: agentName, Model: runModel, Cwd: cwd, StopReason: result.StopReason, Text: result.Text, Usage: usageOutput{InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens}, DurationMS: duration, ExitCode: exitCode, Error: payloadError}
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
		fmt.Fprintln(os.Stderr, "error: turn stopped due to max_steps")
	} else if runErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", runErr)
	}
	return exitCode
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
