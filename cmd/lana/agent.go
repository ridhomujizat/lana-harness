package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

func cmdAgent(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: use lana agent new|list|edit|show")
		return 2
	}
	switch args[0] {
	case "new":
		return agentNew(args[1:], isTTY(os.Stdin))
	case "list":
		return agentList()
	case "edit":
		return agentEdit(args[1:])
	case "show":
		return agentShow(args[1:])
	}
	fmt.Fprintf(os.Stderr, "error: unknown agent command %q (use new, list, edit, show)\n", args[0])
	return 2
}

// splitArgs moves positional arguments out of args so flags may follow them.
func splitArgs(args []string, valueFlags map[string]bool) (flags, positional []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
			continue
		}
		flags = append(flags, arg)
		if valueFlags[strings.TrimLeft(arg, "-")] && !strings.Contains(arg, "=") && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return flags, positional
}

func agentNew(args []string, interactive bool) int {
	fs := flag.NewFlagSet("agent new", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	templateFlag := fs.String("template", "", "template: "+strings.Join(agenthome.TemplateNames(), ", "))
	modelFlag := fs.String("model", "", "model as provider:model")
	yes := fs.Bool("yes", false, "accept defaults without asking")
	flags, positional := splitArgs(args, map[string]bool{"template": true, "model": true})
	if err := fs.Parse(flags); err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, "error: use lana agent new <name> [--template assistant|worker|coder] [--model provider:model] [--yes]")
		return 2
	}
	name := positional[0]
	if !agenthome.ValidName(name) {
		fmt.Fprintf(os.Stderr, "error: invalid agent name %q: use a-z, 0-9, - and _, start with a letter, max 32 characters\n", name)
		return 2
	}
	if !*yes && !interactive {
		fmt.Fprintln(os.Stderr, "error: lana agent new needs a terminal; use --yes for default values")
		return 2
	}
	home, err := homeDir()
	if err != nil {
		return fail(err, 1)
	}
	result, err := config.Load(context.Background(), config.Options{Home: home})
	if err != nil {
		return fail(err, 2)
	}
	cfg := result.Config
	paths := agenthome.Paths(home, name)
	if _, ok := cfg.Agents[name]; ok {
		fmt.Fprintf(os.Stderr, "error: agent %q already exists\n", name)
		return 2
	}
	if _, err := os.Stat(paths.Dir); err == nil {
		fmt.Fprintf(os.Stderr, "error: agent %q already exists\n", name)
		return 2
	}

	templateName := *templateFlag
	if templateName == "" {
		templateName = "assistant"
		if !*yes {
			templateName = ask("Template ["+strings.Join([]string{"assistant", "worker", "coder"}, "/")+"]", "assistant")
		}
	}
	tpl, ok := agenthome.LookupTemplate(templateName)
	if !ok {
		fmt.Fprintf(os.Stderr, "error: unknown template %q (available: %s)\n", templateName, strings.Join(agenthome.TemplateNames(), ", "))
		return 2
	}
	model := *modelFlag
	if model == "" {
		model = defaultModel(cfg)
		if !*yes {
			model = ask("Model", model)
		}
	}
	providerName, modelName, err := checkModel(cfg, model)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid model %q: %v\n", model, err)
		return 2
	}
	permission := tpl.Permission
	makeDefault, startChat := false, false
	if !*yes {
		permission = ask("Permission [ask/trust/never]", permission)
		switch permission {
		case "ask", "trust", "never":
		default:
			fmt.Fprintf(os.Stderr, "error: invalid permission %q (choices: ask, trust, never)\n", permission)
			return 2
		}
		makeDefault = yesNo(ask("Use "+name+" as the default agent for lana chat? [y/N]", "n"))
		startChat = yesNo(ask("Start chat with "+name+" now? [Y/n]", "y"))
	}

	persona, err := tpl.Persona(name)
	if err != nil {
		return fail(err, 1)
	}
	if err := createHome(paths, persona); err != nil {
		_ = os.RemoveAll(paths.Dir)
		return fail(err, 1)
	}
	if err := appendAgentConfig(home, name, providerName, modelName, tpl.Tools, permission, makeDefault); err != nil {
		_ = os.RemoveAll(paths.Dir)
		return fail(err, 1)
	}
	fmt.Printf("created agent %s\n  home:   %s\n  model:  %s\n  tools:  %s\nnext: lana chat --agent %s\n",
		name, paths.Dir, providerName+":"+modelName, strings.Join(tpl.Tools, ", "), name)
	if startChat {
		return cmdChat([]string{"--agent", name})
	}
	return 0
}

func yesNo(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	}
	return false
}

// defaultModel is the model of the agent that lana chat uses by default.
func defaultModel(cfg core.Config) string {
	agent, err := resolveAgent(cfg, "")
	if err != nil {
		return ""
	}
	a := cfg.Agents[agent]
	if a.Provider == "" || a.Model == "" {
		return ""
	}
	return a.Provider + ":" + a.Model
}

func checkModel(cfg core.Config, model string) (string, string, error) {
	providerName, modelName, ok := strings.Cut(model, ":")
	if !ok || providerName == "" || modelName == "" {
		return "", "", errors.New("use provider:model")
	}
	if _, ok := cfg.Providers[providerName]; !ok {
		names := make([]string, 0, len(cfg.Providers))
		for name := range cfg.Providers {
			names = append(names, name)
		}
		sort.Strings(names)
		return "", "", fmt.Errorf("unknown provider %q (available: %s)", providerName, strings.Join(names, ", "))
	}
	return providerName, modelName, nil
}

func createHome(paths agenthome.Home, persona string) error {
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		return err
	}
	if err := agenthome.WriteFile(paths.Agent, persona); err != nil {
		return err
	}
	if err := agenthome.WriteFile(paths.Memory, ""); err != nil {
		return err
	}
	return nil
}

// appendAgentConfig adds [agents.<name>] to config.toml and, if asked, points the cli route at it.
// The original file is restored when the result does not load.
func appendAgentConfig(home, name, providerName, model string, tools []string, permission string, makeDefault bool) error {
	path := filepath.Join(home, "config.toml")
	original, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	quoted := make([]string, len(tools))
	for i, tool := range tools {
		quoted[i] = fmt.Sprintf("%q", tool)
	}
	text := strings.TrimRight(string(original), "\n")
	if makeDefault {
		text = setCLIRoute(text, name)
	}
	text += fmt.Sprintf("\n\n[agents.%s]\nprovider = %q\nmodel = %q\ntools = [%s]\npermission = %q\n",
		name, providerName, model, strings.Join(quoted, ", "), permission)
	text = strings.TrimLeft(text, "\n")
	if err := atomicWrite(path, []byte(text), mode); err != nil {
		return err
	}
	if _, err := config.Load(context.Background(), config.Options{Home: home}); err != nil {
		if original != nil {
			_ = atomicWrite(path, original, mode)
		} else {
			_ = os.Remove(path)
		}
		return fmt.Errorf("config is invalid after adding agent: %w", err)
	}
	return nil
}

// setCLIRoute sets the agent of the first [[routes]] block with channel "cli", or appends one.
func setCLIRoute(text, agent string) string {
	lines := strings.Split(text, "\n")
	for start := 0; start < len(lines); start++ {
		if strings.TrimSpace(lines[start]) != "[[routes]]" {
			continue
		}
		end := start + 1
		for end < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[end]), "[") {
			end++
		}
		isCLI, agentLine := false, -1
		for i := start + 1; i < end; i++ {
			key, value, ok := strings.Cut(lines[i], "=")
			if !ok {
				continue
			}
			switch strings.TrimSpace(key) {
			case "channel":
				isCLI = strings.Trim(strings.TrimSpace(value), `"'`) == "cli"
			case "agent":
				agentLine = i
			}
		}
		if !isCLI {
			continue
		}
		if agentLine >= 0 {
			lines[agentLine] = fmt.Sprintf("agent = %q", agent)
		} else {
			lines = append(lines[:start+1], append([]string{fmt.Sprintf("agent = %q", agent)}, lines[start+1:]...)...)
		}
		return strings.Join(lines, "\n")
	}
	return text + fmt.Sprintf("\n\n[[routes]]\nchannel = \"cli\"\nagent = %q", agent)
}

// personaSource returns the persona path and a label for lana agent list.
func personaSource(home, name string, agent core.AgentConfig) (string, string) {
	if agent.PromptFile != "" {
		path := resolveHomePath(home, agent.PromptFile)
		return path, "prompt_file:" + agent.PromptFile
	}
	path := agenthome.Paths(home, name).Agent
	if _, err := os.Stat(path); err == nil {
		return path, "AGENT.md"
	}
	return path, "none"
}

func resolveHomePath(home, path string) string {
	if strings.HasPrefix(path, "~") {
		if userHome, err := os.UserHomeDir(); err == nil {
			return filepath.Join(userHome, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
		}
	}
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(home, path)
}

// memorySizes returns the size lines of the shared user file and the agent's memory file.
func memorySizes(home string, paths agenthome.Home) (user, memory string) {
	format := func(path string, limit int) string {
		entries, _ := agenthome.ReadEntries(path)
		size := agenthome.Size(entries)
		text := fmt.Sprintf("%d/%d", size, limit)
		if size > limit {
			text += " (over limit)"
		}
		return text
	}
	return format(agenthome.SharedUser(home), agenthome.UserLimit), format(paths.Memory, agenthome.MemoryLimit)
}

// migrateUserFile runs the API-HOME-003 migration for the effective config.
func migrateUserFile(home string, cfg core.Config) {
	defaultAgent := ""
	for _, route := range cfg.Routes {
		if route.Channel == "cli" {
			defaultAgent = route.Agent
			break
		}
	}
	migration, err := agenthome.MigrateUser(home, agentNames(cfg), defaultAgent)
	if err != nil {
		slog.Warn("cannot migrate user file", "error", err)
		return
	}
	if migration.Source != "" {
		slog.Info("migrated user file from agent " + migration.Source)
		return
	}
	if warning := migration.Warning(home); warning != "" {
		slog.Warn(warning)
	}
}

func loadConfigForAgent() (string, core.Config, int) {
	home, err := homeDir()
	if err != nil {
		return "", core.Config{}, fail(err, 1)
	}
	result, err := config.Load(context.Background(), config.Options{Home: home})
	if err != nil {
		return "", core.Config{}, fail(err, 2)
	}
	migrateUserFile(home, result.Config)
	return home, result.Config, 0
}

func agentNames(cfg core.Config) []string {
	names := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func agentList() int {
	home, cfg, code := loadConfigForAgent()
	if code != 0 {
		return code
	}
	for _, name := range agentNames(cfg) {
		agent := cfg.Agents[name]
		_, source := personaSource(home, name, agent)
		user, memory := memorySizes(home, agenthome.Paths(home, name))
		permission := agent.Permission
		if permission == "" {
			permission = "ask"
		}
		fmt.Printf("%s  %s:%s  %s  persona=%s  user=%s  memory=%s\n", name, agent.Provider, agent.Model, permission, source, user, memory)
	}
	return 0
}

func lookupAgent(cfg core.Config, name string) (core.AgentConfig, bool) {
	agent, ok := cfg.Agents[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "error: unknown agent %q\n", name)
	}
	return agent, ok
}

func agentEdit(args []string) int {
	if len(args) < 1 || len(args) > 2 {
		fmt.Fprintln(os.Stderr, "error: use lana agent edit <name> [persona|user|memory]")
		return 2
	}
	home, cfg, code := loadConfigForAgent()
	if code != 0 {
		return code
	}
	name := args[0]
	agent, ok := lookupAgent(cfg, name)
	if !ok {
		return 2
	}
	which := "persona"
	if len(args) == 2 {
		which = args[1]
	}
	paths := agenthome.Paths(home, name)
	var path string
	switch which {
	case "persona":
		path, _ = personaSource(home, name, agent)
	case "user":
		path = agenthome.SharedUser(home)
	case "memory":
		path = paths.Memory
	default:
		fmt.Fprintf(os.Stderr, "error: unknown file %q (use persona, user, memory)\n", which)
		return 2
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := agenthome.WriteFile(path, ""); err != nil {
			return fail(err, 1)
		}
	}
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	parts := strings.Fields(editor)
	cmd := exec.Command(parts[0], append(parts[1:], path)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func agentShow(args []string) int {
	fs := flag.NewFlagSet("agent show", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	showPrompt := fs.Bool("prompt", false, "print the assembled system prompt")
	flags, positional := splitArgs(args, nil)
	if err := fs.Parse(flags); err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, "error: use lana agent show <name> [--prompt]")
		return 2
	}
	home, cfg, code := loadConfigForAgent()
	if code != 0 {
		return code
	}
	name := positional[0]
	agent, ok := lookupAgent(cfg, name)
	if !ok {
		return 2
	}
	paths := agenthome.Paths(home, name)
	personaPath, source := personaSource(home, name, agent)
	if *showPrompt {
		cwd, err := os.Getwd()
		if err != nil {
			return fail(err, 1)
		}
		in := agenthome.PromptInput{Agent: name, Channel: "cli", Workspace: cwd, Now: time.Now()}
		if data, err := os.ReadFile(personaPath); err == nil {
			in.Persona = string(data)
		}
		in.User, _ = agenthome.ReadEntries(agenthome.SharedUser(home))
		in.Memory, _ = agenthome.ReadEntries(paths.Memory)
		fmt.Println(agenthome.Build(in))
		return 0
	}
	user, memory := memorySizes(home, paths)
	permission := agent.Permission
	if permission == "" {
		permission = "ask"
	}
	fmt.Printf("agent       %s\nmodel       %s:%s\npermission  %s\ntools       %s\npersona     %s (%s)\nhome        %s\nuser        %s characters\nmemory      %s characters\n",
		name, agent.Provider, agent.Model, permission, strings.Join(agent.Tools, ", "), source, personaPath, paths.Dir, user, memory)
	return 0
}

// agentSizeWarnings returns config check warnings for over-limit memory files
// and for per-agent USER.md files that the migration no longer reads.
func agentSizeWarnings(home string, cfg core.Config) []string {
	out := config.LegacyUserWarnings(home, cfg)
	if entries, err := agenthome.ReadEntries(agenthome.SharedUser(home)); err == nil {
		if size := agenthome.Size(entries); size > agenthome.UserLimit {
			out = append(out, fmt.Sprintf("· warning: user file %s is %d/%d characters (over limit; the prompt is truncated)", agenthome.SharedUser(home), size, agenthome.UserLimit))
		}
	}
	for _, name := range agentNames(cfg) {
		paths := agenthome.Paths(home, name)
		entries, err := agenthome.ReadEntries(paths.Memory)
		if err != nil {
			continue
		}
		if size := agenthome.Size(entries); size > agenthome.MemoryLimit {
			out = append(out, fmt.Sprintf("· warning: agent %s MEMORY.md is %d/%d characters (over limit; the prompt is truncated)", name, size, agenthome.MemoryLimit))
		}
	}
	return out
}
