// Package agentdetect identifies the coding agent running in a terminal's
// foreground job and reads its state from the rendered screen.
//
// The identification rules and the screen-manifest engine are a Go port of
// herdr's detect module (see NOTICE and LICENSE-herdr). Only the parts that
// matter to a Unix pty foreground job are ported; the Windows cmd and
// PowerShell command-line parsing is intentionally left out.
package agentdetect

import (
	"path/filepath"
	"strings"
)

// Process is one process of a terminal's foreground job.
type Process struct {
	PID   int
	Name  string
	Argv0 string
	Argv  []string
}

// Job is the foreground process group of a terminal: the group id and every
// process that belongs to it.
type Job struct {
	PGID      int
	Processes []Process
}

// Identify reports the coding agent running in a foreground job. It prefers a
// recognized process-group leader and otherwise scores every process, so a
// node/bun/python/shell wrapper that names an agent in its arguments is still
// recognized. The returned label is the agent's canonical name, the pid is the
// process that matched, and ok is false when nothing in the job is an agent.
func Identify(job Job) (label string, pid int, ok bool) {
	for _, p := range job.Processes {
		if p.PID != job.PGID {
			continue
		}
		candidate := normalizedProcessName(p)
		if l, found := identifyAgent(candidate); found && (l != agentLetta || isInteractiveLettaProcess(p)) {
			return l, p.PID, true
		}
	}

	bestScore := -1
	for _, p := range job.Processes {
		candidate := normalizedProcessName(p)
		l, found := identifyAgent(candidate)
		if !found {
			continue
		}
		if l == agentLetta && !isInteractiveLettaProcess(p) {
			continue
		}
		score := processPriority(p, candidate)
		if bestScore >= score {
			continue
		}
		bestScore, label, pid = score, l, p.PID
	}
	return label, pid, bestScore >= 0
}

// agent labels, matching herdr's canonical names.
const (
	agentPi          = "pi"
	agentClaude      = "claude"
	agentCodex       = "codex"
	agentGemini      = "gemini"
	agentCursor      = "cursor"
	agentDevin       = "devin"
	agentAntigravity = "agy"
	agentCline       = "cline"
	agentOmp         = "omp"
	agentMastracode  = "mastracode"
	agentOpenCode    = "opencode"
	agentCopilot     = "copilot"
	agentKimi        = "kimi"
	agentKiro        = "kiro"
	agentDroid       = "droid"
	agentAmp         = "amp"
	agentGrok        = "grok"
	agentHermes      = "hermes"
	agentKilo        = "kilo"
	agentQodercli    = "qodercli"
	agentQwen        = "qwen"
	agentLetta       = "letta"
	agentMaki        = "maki"
	agentMuse        = "muse"
)

// lookupAgent maps a process name to its canonical agent label. It returns
// false for plain shells and unrecognized programs.
func lookupAgent(name string) (string, bool) {
	name = pathBasename(name)
	switch name {
	case "pi":
		return agentPi, true
	case "claude", "claude-code":
		return agentClaude, true
	case "codex":
		return agentCodex, true
	case "gemini":
		return agentGemini, true
	case "cursor", "cursor-agent":
		return agentCursor, true
	case "devin", "devin-cli", "devin cli":
		return agentDevin, true
	case "agy", "antigravity", "antigravity-cli":
		return agentAntigravity, true
	case "cline", ".cline":
		return agentCline, true
	case "omp":
		return agentOmp, true
	case "mastracode", "mastra-code", "mastra code":
		return agentMastracode, true
	case "opencode", "opencode2", "open-code":
		return agentOpenCode, true
	case "copilot", "github-copilot", "ghcs":
		return agentCopilot, true
	case "kimi", "kimi-code", "kimi code":
		return agentKimi, true
	case "kiro", "kiro-cli":
		return agentKiro, true
	case "droid":
		return agentDroid, true
	case "amp", "amp-local":
		return agentAmp, true
	case "grok", "grok-build":
		return agentGrok, true
	case "hermes", "hermes-agent":
		return agentHermes, true
	case "kilo", "kilo-code", "kilo code":
		return agentKilo, true
	case "qodercli", "qoderclicn", "qoder", "qodercn":
		return agentQodercli, true
	case "qwen", "qwen-code", "qwen code":
		return agentQwen, true
	case "letta", "letta-code", "letta code":
		return agentLetta, true
	case "maki":
		return agentMaki, true
	case "muse", "muse-code", "muse-cli":
		return agentMuse, true
	}
	if isMuseVersionedBinary(name) {
		return agentMuse, true
	}
	return "", false
}

// isMuseVersionedBinary recognizes Muse's install-dir launcher, which execs
// muse-bin-<version>. A digit must follow the prefix so unrelated binaries such
// as muse-binary or a bare muse-bin stay unmatched.
func isMuseVersionedBinary(name string) bool {
	rest, ok := strings.CutPrefix(pathBasename(name), "muse-bin-")
	return ok && rest != "" && rest[0] >= '0' && rest[0] <= '9'
}

// identifyAgent maps a possibly path-qualified, possibly versioned process name
// to its canonical agent label.
func identifyAgent(name string) (string, bool) {
	return lookupAgent(normalizedAgentLookupName(name))
}

// normalizedAgentLookupName trims, lowercases and drops a known executable
// suffix so "Claude.exe" and "opencode.js" resolve.
func normalizedAgentLookupName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, suffix := range []string{".exe", ".cmd", ".bat", ".ps1", ".js"} {
		if strings.HasSuffix(n, suffix) {
			return n[:len(n)-len(suffix)]
		}
	}
	return n
}

// pathBasename returns the last non-empty component of a /- or \-separated path.
func pathBasename(path string) string {
	last := ""
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		last = part
	}
	if last == "" {
		return path
	}
	return last
}

// normalizedProcessName resolves a process to the name an agent lookup should
// see: its argv0 when present, a wrapped agent named by a runtime's arguments,
// or the process name itself.
func normalizedProcessName(p Process) string {
	effective := p.Name
	if p.Argv0 != "" {
		effective = p.Argv0
	}
	lowerEffective := strings.ToLower(effective)

	if isGenericRuntimeOrShell(lowerEffective) {
		if wrapped, ok := wrappedAgentNameFromRuntimeArgv(lowerEffective, p.Argv); ok {
			return wrapped
		}
	}

	if _, ok := identifyAgent(effective); ok {
		return effective
	}

	if len(p.Argv) > 0 {
		runtimeName := normalizedAgentLookupName(pathBasename(p.Argv[0]))
		if runtimeName == "node" || runtimeName == "bun" {
			if wrapped, ok := wrappedAgentNameFromRuntimeArgv(p.Argv[0], p.Argv); ok {
				if label, found := identifyAgent(wrapped); found &&
					(label == agentQwen || label == agentCline || label == agentLetta) {
					return wrapped
				}
			}
		}
	}

	if wrapped, ok := argv0AgentName(p.Argv); ok {
		return wrapped
	}

	return effective
}

// wrappedAgentNameFromRuntimeArgv reads the script name an interpreter or shell
// was handed and returns the agent it names, if any.
func wrappedAgentNameFromRuntimeArgv(runtime string, argv []string) (string, bool) {
	if argv == nil {
		return "", false
	}
	switch runtimeName := normalizedAgentLookupName(pathBasename(runtime)); {
	case runtimeName == "node":
		if wrapped, ok := cursorAgentNameFromBundledNodeArgv(argv); ok {
			return wrapped, true
		}
		return scriptArgAgentName(argv, []string{"-e", "--eval", "-p", "--print"}, nil)
	case runtimeName == "bun":
		return scriptArgAgentName(argv, []string{"-e", "--eval", "-p", "--print"}, nil)
	case isPythonRuntime(runtimeName):
		if wrapped, ok := hermesInstallerAgentName(argv); ok {
			return wrapped, true
		}
		return scriptArgAgentName(argv, []string{"-c"}, []string{"-m"})
	case runtimeName == "sh" || runtimeName == "bash" || runtimeName == "zsh" || runtimeName == "fish":
		return scriptArgAgentName(argv, []string{"-c"}, nil)
	default:
		return "", false
	}
}

// scriptArgAgentName returns the agent named by the first non-flag argument of
// an interpreter invocation, refusing eval/module invocations whose arguments
// are code rather than a script path.
func scriptArgAgentName(argv []string, evalFlags, moduleFlags []string) (string, bool) {
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			if i+1 < len(argv) {
				return agentNameFromPathToken(argv[i+1])
			}
			return "", false
		}
		if flagMatches(arg, evalFlags) || flagMatches(arg, moduleFlags) {
			return "", false
		}
		if strings.HasPrefix(arg, "-") {
			if optionTakesValue(arg) {
				i++
			}
			continue
		}
		return agentNameFromPathToken(arg)
	}
	return "", false
}

// flagMatches reports whether arg is one of flags, its short-flag payload, or
// its "--flag=value" form.
func flagMatches(arg string, flags []string) bool {
	for _, flag := range flags {
		if arg == flag || shortFlagPayload(arg, flag) || longFlagValue(arg, flag) {
			return true
		}
	}
	return false
}

func shortFlagPayload(arg, flag string) bool {
	return strings.HasPrefix(flag, "-") && !strings.HasPrefix(flag, "--") &&
		strings.HasPrefix(arg, flag) && len(arg) > len(flag)
}

func longFlagValue(arg, flag string) bool {
	if !strings.HasPrefix(flag, "--") {
		return false
	}
	rest, ok := strings.CutPrefix(arg, flag)
	return ok && strings.HasPrefix(rest, "=")
}

func optionTakesValue(arg string) bool {
	switch arg {
	case "-r", "--require", "--loader", "--import", "--experimental-loader",
		"--inspect-port", "-W", "-X", "-S", "-L", "-o":
		return true
	}
	return false
}

// cursorAgentNameFromBundledNodeArgv recognizes Cursor's bundled node, whose
// argv0 is .../cursor-agent/versions/<version>/node.exe next to index.js.
func cursorAgentNameFromBundledNodeArgv(argv []string) (string, bool) {
	if len(argv) < 2 {
		return "", false
	}
	runtimeParent, runtimeName, ok := pathParentAndBasename(argv[0])
	if !ok {
		return "", false
	}
	scriptParent, scriptName, ok := pathParentAndBasename(argv[1])
	if !ok {
		return "", false
	}
	if !strings.EqualFold(runtimeName, "node.exe") ||
		!strings.EqualFold(scriptName, "index.js") ||
		!strings.EqualFold(runtimeParent, scriptParent) {
		return "", false
	}
	tail := splitPathComponents(runtimeParent)
	if len(tail) < 3 {
		return "", false
	}
	version := tail[len(tail)-1]
	versions := tail[len(tail)-2]
	pkg := tail[len(tail)-3]
	if strings.EqualFold(pkg, "cursor-agent") && strings.EqualFold(versions, "versions") &&
		strings.TrimSpace(version) != "" {
		return agentCursor, true
	}
	return "", false
}

func pathParentAndBasename(path string) (parent, base string, ok bool) {
	split := strings.LastIndexAny(path, "/\\")
	if split < 0 {
		return "", "", false
	}
	parent = strings.TrimRight(path[:split], "/\\")
	base = path[split+1:]
	if parent == "" || base == "" {
		return "", "", false
	}
	return parent, base, true
}

func splitPathComponents(path string) []string {
	return strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
}

// argv0AgentName returns the agent named by argv[0] alone.
func argv0AgentName(argv []string) (string, bool) {
	if len(argv) == 0 {
		return "", false
	}
	return agentNameFromPathToken(argv[0])
}

// agentNameFromPathToken resolves a single command-line token to an agent: as a
// basename, as a known npm package path, or through a symlink.
func agentNameFromPathToken(token string) (string, bool) {
	trimmed := strings.Trim(token, `"'`)
	if trimmed == "" || strings.HasPrefix(trimmed, "-") {
		return "", false
	}
	if label, ok := agentNameFromBasename(pathBasename(trimmed)); ok {
		return label, true
	}
	if label, ok := agentNameFromKnownPackagePath(trimmed); ok {
		return label, true
	}
	return resolvedAgentNameFromPathToken(trimmed)
}

// agentNameFromKnownPackagePath recognizes well-known npm package entry points
// whose file names are not agents themselves.
func agentNameFromKnownPackagePath(path string) (string, bool) {
	raw := splitPathComponents(path)
	endsWith := func(suffix ...string) bool {
		if len(raw) < len(suffix) {
			return false
		}
		offset := len(raw) - len(suffix)
		for i, want := range suffix {
			if !strings.EqualFold(raw[offset+i], want) {
				return false
			}
		}
		return true
	}
	if endsWith("node_modules", "@earendil-works", "pi-coding-agent", "dist", "cli.js") ||
		endsWith("node_modules", "@earendil-works", "pi-coding-agent", "dist", "bundle", "cli.js") {
		return agentPi, true
	}
	if endsWith("node_modules", "@oh-my-pi", "pi-coding-agent", "dist", "cli.js") {
		return agentOmp, true
	}
	if endsWith("node_modules", "@moonshot-ai", "kimi-code", "dist", "main.mjs") {
		return agentKimi, true
	}

	components := make([]string, len(raw))
	for i, component := range raw {
		components[i] = normalizedAgentLookupName(component)
	}
	for i := 0; i+5 <= len(components); i++ {
		if equalComponents(components[i:i+5], "node_modules", "@qwen-code", "qwen-code", "dist", "index") {
			return agentQwen, true
		}
	}
	for i := 0; i+4 <= len(components); i++ {
		if equalComponents(components[i:i+4], "node_modules", "mastracode", "dist", "cli") {
			return agentMastracode, true
		}
		if equalComponents(components[i:i+4], "node_modules", "@letta-ai", "letta-code", "letta") {
			return agentLetta, true
		}
	}
	return "", false
}

func equalComponents(components []string, want ...string) bool {
	if len(components) != len(want) {
		return false
	}
	for i := range want {
		if components[i] != want[i] {
			return false
		}
	}
	return true
}

func agentNameFromBasename(basename string) (string, bool) {
	return identifyAgent(basename)
}

// resolvedAgentNameFromPathToken follows a path-qualified symlink and looks for
// an agent at the resolved basename.
func resolvedAgentNameFromPathToken(token string) (string, bool) {
	if !strings.ContainsAny(token, "/\\") {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(token)
	if err != nil {
		return "", false
	}
	return agentNameFromBasename(filepath.Base(resolved))
}

// processPriority scores how strongly a process looks like the agent itself:
// a name that differs from the resolved agent name wins, then a non-runtime
// name, then a generic runtime or shell.
func processPriority(p Process, normalizedName string) int {
	if strings.ToLower(normalizedName) != strings.ToLower(p.Name) {
		return 3
	}
	if !isGenericRuntimeOrShell(strings.ToLower(normalizedName)) {
		return 2
	}
	return 1
}

func isGenericRuntimeOrShell(name string) bool {
	name = normalizedAgentLookupName(pathBasename(name))
	if isPythonRuntime(name) {
		return true
	}
	switch name {
	case "sh", "bash", "zsh", "fish", "tmux", "node", "bun", "cmd", "powershell", "pwsh":
		return true
	}
	return false
}

func isPythonRuntime(name string) bool {
	if name == "python" {
		return true
	}
	version, ok := strings.CutPrefix(name, "python")
	if !ok || version == "" {
		return false
	}
	for _, part := range strings.Split(version, ".") {
		if part == "" {
			return false
		}
		for i := 0; i < len(part); i++ {
			if part[i] < '0' || part[i] > '9' {
				return false
			}
		}
	}
	return true
}

// isInteractiveLettaProcess keeps non-interactive Letta runs (one-shot prompts,
// servers, subcommands) from being mistaken for the interactive TUI.
func isInteractiveLettaProcess(p Process) bool {
	argv := p.Argv
	if len(argv) == 0 {
		return true
	}
	cliArgs := argv
	if index, ok := lettaEntrypointIndex(argv); ok {
		cliArgs = argv[index+1:]
	}
	for _, arg := range cliArgs {
		option := arg
		if name, _, found := strings.Cut(arg, "="); found {
			option = name
		}
		switch option {
		case "-p", "--print", "--prompt", "--json", "--stream-json", "--run",
			"--disable-memory-guard", "--output-format", "--input-format",
			"--include-partial-messages", "--from-agent", "--environment", "--env",
			"--pre-load-skills", "--tags", "--ephemeral", "--stateless", "--max-turns",
			"--memfs-startup", "-h", "--help", "-v", "--version", "--info", "--update",
			"--upgrade":
			return false
		}
	}
	first, ok := lettaFirstArgAfterBackendSelection(cliArgs)
	return !ok || strings.HasPrefix(first, "-")
}

func lettaEntrypointIndex(argv []string) (int, bool) {
	isLetta := func(arg string) bool {
		label, ok := agentNameFromPathToken(arg)
		return ok && label == agentLetta
	}
	if len(argv) > 0 && isLetta(argv[0]) {
		return 0, true
	}
	if len(argv) == 0 {
		return 0, false
	}
	runtime := normalizedAgentLookupName(pathBasename(argv[0]))
	if runtime != "node" && runtime != "bun" {
		return 0, false
	}
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			if i+1 < len(argv) && isLetta(argv[i+1]) {
				return i + 1, true
			}
			return 0, false
		}
		if flagMatches(arg, []string{"-e", "--eval", "-p", "--print"}) {
			return 0, false
		}
		if strings.HasPrefix(arg, "-") {
			if optionTakesValue(arg) {
				i++
			}
			continue
		}
		if isLetta(arg) {
			return i, true
		}
		return 0, false
	}
	return 0, false
}

func lettaFirstArgAfterBackendSelection(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--backend" {
			i++
			continue
		}
		if strings.HasPrefix(arg, "--backend=") {
			continue
		}
		return arg, true
	}
	return "", false
}

// Hermes' install launcher is a python -I -c program; these constants are the
// fixed parts of its source, around the installation root.
const (
	hermesInstallerEntry = `from hermes_cli.main import main
sys.argv[0] = re.sub(r'(-script\.pyw|\.exe)?$', '', sys.argv[0])
sys.exit(main())
`
	hermesInstallerPrefix = `os.environ.pop('PYTHONHOME', None)
os.environ.pop('PYTHONPATH', None)
sys.path.insert(0, '`
	hermesInstallerMiddle = `if sys.argv[1:2] == ['--print-runtime-command']: sys.dont_write_bytecode = True
from hermes_constants import get_default_hermes_root
os.environ['HERMES_HOME'] = os.environ.get('HERMES_HOME') or str(get_default_hermes_root())
if sys.argv[1:2] == ['--print-runtime-command']:
    from pathlib import Path
    from hermes_cli._launchers import print_runtime_command
    print_runtime_command(Path('`
	hermesInstallerSuffix = `'), sys.argv[2:])
    sys.exit(0)
import hermes_bootstrap
if sys.argv[1:2] == ['--run-module']:
    import runpy
    if len(sys.argv) < 3: sys.exit('hermes: --run-module needs a module')
    module = sys.argv.pop(2)
    del sys.argv[1]
    runpy.run_module(module, run_name='__main__', alter_sys=True)
    sys.exit(0)
from hermes_cli.main import main
sys.argv[0] = re.sub(r'(-script\.pyw|\.exe)?$', '', sys.argv[0])
sys.exit(main())
`
)

// hermesInstallerAgentName recognizes the `python -I -c <installer>` invocation
// hermes' launcher uses to start its interactive TUI.
func hermesInstallerAgentName(argv []string) (string, bool) {
	if len(argv) < 4 {
		return "", false
	}
	isolation, command, code, args := argv[1], argv[2], argv[3], argv[4:]
	if isolation != "-I" || command != "-c" {
		return "", false
	}
	if len(args) > 0 && (args[0] == "--run-module" || args[0] == "--print-runtime-command") {
		return "", false
	}

	code, ok := strings.CutPrefix(code, "import os, re, sys\n")
	if !ok {
		return "", false
	}
	expanded := false
	if strings.HasPrefix(code, "import ") {
		expanded = true
		_, rest, found := strings.Cut(code, "\n")
		if !found {
			return "", false
		}
		code = rest
	}

	rest, ok := strings.CutPrefix(code, hermesInstallerPrefix)
	if !ok {
		return "", false
	}
	root, rest, ok := strings.Cut(rest, "')\n")
	if !ok {
		return "", false
	}
	if root == "" || strings.ContainsAny(root, "'\\\n\r") {
		return "", false
	}
	if expanded {
		// Recovery helpers change independently of the interactive entry point.
		if !strings.HasSuffix(rest, hermesInstallerEntry) {
			return "", false
		}
		for _, line := range rustLines(rest) {
			if line == "import hermes_bootstrap" || line == "    import hermes_bootstrap" {
				return agentHermes, true
			}
		}
		return "", false
	}

	rest, ok = strings.CutPrefix(rest, hermesInstallerMiddle)
	if !ok {
		return "", false
	}
	rest, ok = strings.CutPrefix(rest, root)
	if !ok {
		return "", false
	}
	if rest == hermesInstallerSuffix {
		return agentHermes, true
	}
	return "", false
}
