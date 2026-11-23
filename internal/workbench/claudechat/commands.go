package claudechat

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Command is one entry of the slash command list (spec 71 REQ-WC-035).
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

const (
	// maxCommandDepth is how many directory levels below a commands dir are
	// scanned.
	maxCommandDepth = 3
	// maxScanFiles caps the total number of skill and command files read.
	maxScanFiles = 500
	// maxDescriptionRunes cuts a derived description.
	maxDescriptionRunes = 120
)

// commandNamePattern is the accepted skill and command name shape.
var commandNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)

// builtinCommands are Claude Code 2.1.330's built-in slash commands (REQ-WC-035).
var builtinCommands = []Command{
	{Name: "/add-dir", Description: "Add a working directory", Source: "built-in"},
	{Name: "/advisor", Description: "Let Claude consult a stronger model at key moments", Source: "built-in"},
	{Name: "/autocompact", Description: "Set how full the context gets before auto-summarizing", Source: "built-in"},
	{Name: "/branch", Description: "Create a branch of the current conversation at this point", Source: "built-in"},
	{Name: "/btw", Description: "Ask a side question without interrupting Claude", Source: "built-in"},
	{Name: "/cd", Description: "Move this session to a new working directory", Source: "built-in"},
	{Name: "/clear", Description: "Start a new session with empty context", Source: "built-in"},
	{Name: "/compact", Description: "Free up context by summarizing the conversation so far", Source: "built-in"},
	{Name: "/config", Description: "Open settings", Source: "built-in"},
	{Name: "/context", Description: "Show what fills the context", Source: "built-in"},
	{Name: "/copy", Description: "Copy the last response to clipboard", Source: "built-in"},
	{Name: "/effort", Description: "Set the model's effort level", Source: "built-in"},
	{Name: "/export", Description: "Export this session to a file or clipboard", Source: "built-in"},
	{Name: "/feedback", Description: "Send feedback to Anthropic or report a bug", Source: "built-in"},
	{Name: "/fork", Description: "Copy this conversation into a new background session", Source: "built-in"},
	{Name: "/goal", Description: "Set a goal Claude checks before stopping", Source: "built-in"},
	{Name: "/help", Description: "Show help and available commands", Source: "built-in"},
	{Name: "/hooks", Description: "View hook configurations for tool events", Source: "built-in"},
	{Name: "/ide", Description: "Manage IDE integrations and show status", Source: "built-in"},
	{Name: "/init", Description: "Create a CLAUDE.md for this project", Source: "built-in"},
	{Name: "/mcp", Description: "Manage MCP servers", Source: "built-in"},
	{Name: "/memory", Description: "Edit CLAUDE.md files and memory settings", Source: "built-in"},
	{Name: "/model", Description: "Set the model for this session", Source: "built-in"},
	{Name: "/permissions", Description: "Manage allow and deny rules", Source: "built-in"},
	{Name: "/plan", Description: "Turn on plan mode or view the plan", Source: "built-in"},
	{Name: "/recap", Description: "Generate a one-line session recap now", Source: "built-in"},
	{Name: "/reload-skills", Description: "Pick up skills added or changed on disk", Source: "built-in"},
	{Name: "/resume", Description: "Resume a previous conversation", Source: "built-in"},
	{Name: "/review", Description: "Review a pull request", Source: "built-in"},
	{Name: "/rewind", Description: "Rewind the conversation or code", Source: "built-in"},
	{Name: "/skills", Description: "List available skills", Source: "built-in"},
	{Name: "/status", Description: "Show version, account, model, MCP servers", Source: "built-in"},
	{Name: "/subtask", Description: "Send a subagent off with your full context", Source: "built-in"},
	{Name: "/theme", Description: "Change the theme", Source: "built-in"},
	{Name: "/usage", Description: "Show plan usage limits", Source: "built-in"},
	{Name: "/version", Description: "Show this session's version", Source: "built-in"},
}

// commandsTTL is how long a command list is cached; a package var so tests can
// shorten it.
var commandsTTL = 30 * time.Second

var (
	commandsMu    sync.Mutex
	commandsCache = map[string]commandsEntry{}
)

type commandsEntry struct {
	commands []Command
	at       time.Time
}

// Commands returns the built-in Claude Code slash commands followed by the
// user and project skills and commands found under cwd and home (REQ-WC-035).
// The result is cached per (cwd, home) for commandsTTL.
func Commands(cwd, home string) []Command {
	key := cwd + "\x00" + home

	commandsMu.Lock()
	if e, ok := commandsCache[key]; ok && time.Since(e.at) < commandsTTL {
		commandsMu.Unlock()
		return append([]Command(nil), e.commands...)
	}
	commandsMu.Unlock()

	cmds := scanCommands(cwd, home)

	commandsMu.Lock()
	commandsCache[key] = commandsEntry{commands: cmds, at: time.Now()}
	commandsMu.Unlock()
	return append([]Command(nil), cmds...)
}

// scanCommands builds the command list: built-ins first, then user skills,
// project skills, user commands and project commands. A later duplicate name is
// dropped, so the first occurrence wins.
func scanCommands(cwd, home string) []Command {
	out := append([]Command(nil), builtinCommands...)
	seen := map[string]bool{}
	for _, c := range out {
		seen[c.Name] = true
	}
	budget := maxScanFiles
	add := func(c Command) {
		if !commandNamePattern.MatchString(c.Name) || seen[c.Name] {
			return
		}
		seen[c.Name] = true
		out = append(out, c)
	}

	scanSkills(filepath.Join(home, ".claude", "skills"), "user skill", add, &budget)
	scanSkills(filepath.Join(cwd, ".claude", "skills"), "project skill", add, &budget)
	walkCommands(filepath.Join(home, ".claude", "commands"), "user command", add, &budget)
	walkCommands(filepath.Join(cwd, ".claude", "commands"), "project command", add, &budget)
	return out
}

// scanSkills reads the *.md skills under dir: each <name>/SKILL.md is a skill
// named after its folder (REQ-WC-035).
func scanSkills(dir, source string, add func(Command), budget *int) {
	files, _ := filepath.Glob(filepath.Join(dir, "*", "SKILL.md"))
	for _, file := range files {
		if *budget <= 0 {
			return
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		*budget--
		add(Command{
			Name:        filepath.Base(filepath.Dir(file)),
			Description: describe(raw),
			Source:      source,
		})
	}
}

// walkCommands reads the *.md command files under root, up to maxCommandDepth
// levels deep. The name is the path relative to root without ".md", with path
// separators replaced by ":" (REQ-WC-035).
func walkCommands(root, source string, add func(Command), budget *int) {
	root = filepath.Clean(root)
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil || rel == "." {
			return nil
		}
		depth := strings.Count(rel, string(filepath.Separator))
		if d.IsDir() {
			if depth >= maxCommandDepth {
				return fs.SkipDir
			}
			return nil
		}
		if depth > maxCommandDepth || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		if *budget <= 0 {
			return fs.SkipAll
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		*budget--
		name := strings.TrimSuffix(rel, ".md")
		name = strings.ReplaceAll(name, string(filepath.Separator), ":")
		add(Command{Name: name, Description: describe(raw), Source: source})
		return nil
	})
}

// describe derives a description from a skill or command file: the frontmatter
// description when present, else the first non-empty line after the frontmatter.
// The result is cut to maxDescriptionRunes.
func describe(raw []byte) string {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	body := text
	if strings.HasPrefix(text, "---\n") {
		rest := text[len("---\n"):]
		if end := strings.Index(rest, "\n---"); end >= 0 {
			if d := frontmatterDescription(rest[:end]); d != "" {
				return cutRunes(strings.TrimSpace(d), maxDescriptionRunes)
			}
			body = rest[end+len("\n---"):]
			body = strings.TrimPrefix(body, "\n")
		}
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return cutRunes(line, maxDescriptionRunes)
	}
	return ""
}

// frontmatterDescription returns the frontmatter's description value, unquoted,
// or "" when there is none.
func frontmatterDescription(front string) string {
	for _, line := range strings.Split(front, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "description" {
			continue
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			first, last := val[0], val[len(val)-1]
			if (first == '\'' && last == '\'') || (first == '"' && last == '"') {
				val = val[1 : len(val)-1]
			}
		}
		return val
	}
	return ""
}

// cutRunes cuts s to at most max runes.
func cutRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
