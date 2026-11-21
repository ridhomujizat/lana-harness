package permission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/pattern"
)

// The hardline floor (spec 63): a small set of catastrophic or
// credential-exposing tool calls that never run on the model's word alone,
// whatever the policy, allow rules, or session grants. The rule tables below
// are plain Go values; no config, environment variable, or flag may change
// them (REQ-HLF-006).

// Rule IDs.
const (
	hlB001 = "HL-B-001"
	hlB002 = "HL-B-002"
	hlB003 = "HL-B-003"
	hlB004 = "HL-B-004"
	hlE001 = "HL-E-001"
	hlE002 = "HL-E-002"
	hlE003 = "HL-E-003"
	hlE004 = "HL-E-004"
	hlE005 = "HL-E-005"
	hlE006 = "HL-E-006"
)

// One-line reasons, shown to the owner in the approval card and the refusal.
const (
	reasonB001 = "recursive delete of the filesystem root, a system directory, or the home directory"
	reasonB002 = "disk or filesystem destruction"
	reasonB003 = "fork bomb"
	reasonB004 = "write to a protected path"
	reasonE001 = "download piped into a shell or interpreter"
	reasonE002 = "privilege change"
	reasonE003 = "force push or delete/mirror of a remote"
	reasonE004 = "recursive delete of a directory in the home directory"
	reasonE005 = "read of a secret file"
	reasonE006 = "persistence through cron or a launch agent"
)

// hlSystemDirs are the top-level directories a recursive delete must never
// touch (REQ-HLF-002, HL-B-001).
var hlSystemDirs = []string{
	"/System", "/Library", "/Applications", "/Users",
	"/usr", "/bin", "/sbin", "/etc", "/var", "/private",
}

var (
	// hlPipeToShell finds a curl/wget download piped (optionally through sudo)
	// into a shell or interpreter, capturing the rest of that pipeline stage so
	// the interpreter's arguments can be inspected (REQ-HLF-003, HL-E-001).
	hlPipeToShell = regexp.MustCompile(`(?i)\b(?:curl|wget)\b[^|]*\|\s*(?:sudo\s+)?(?:bash|zsh|dash|sh|python3?|node|perl)\b([^|;&]*)`)
	// hlDiskRedirect matches a redirect onto a raw disk device (HL-B-002).
	hlDiskRedirect = regexp.MustCompile(`(?i)>\s*/dev/r?disk`)
	// hlRedirectTarget reads the target of >, >>, 2>, or &>.
	hlRedirectTarget = regexp.MustCompile(`>{1,2}\s*("[^"]*"|'[^']*'|[^\s;&|<>]+)`)
	// hlCd finds a leading cd segment that moves the working directory.
	hlCd = regexp.MustCompile(`^cd\s+(.+)$`)

	// Raw-text fallback for commands that cannot be analyzed (REQ-HLF-005).
	hlWordRm    = regexp.MustCompile(`\brm\b`)
	hlWordDisk  = regexp.MustCompile(`\b(mkfs|newfs|dd|diskutil)\b`)
	hlWriteVerb = regexp.MustCompile(`\b(rm|mv|cp|tee|truncate|chmod|chown|ln)\b`)

	// hlRedirectField matches a redirection at the start of a segment field: an
	// optional file descriptor, then an operator. A target attached to the same
	// field is part of the match, so the operator and the target are dropped
	// together. A field that starts with a quote is not a redirection and never
	// matches.
	hlRedirectField = regexp.MustCompile(`^(?:[0-9]+)?(?:<<<|<<-|<<|>>|>&|<&|&>>|&>|<|>)`)

	// Inline interpreter code (REQ-HLF-005). These read the code string as raw
	// text; they are a heuristic, not a sandbox.
	hlDeleteCall = regexp.MustCompile(`\b(rmtree|rmSync|rmdirSync|rm_rf|remove_dir_all|rimraf)\b|\brm\s+-[rR]`)
	hlCodeWrite  = regexp.MustCompile(`\b(write|unlink|remove|rmtree|rmSync|rmdirSync|rm_rf|remove_dir_all|rimraf|truncate|chmod|rename)\b`)
)

// hlShellNames are the shells whose -c string is analyzed one level deeper.
var hlShellNames = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true, "fish": true,
}

// hlInterpreters are the interpreters whose inline code (-c, -e) is checked as
// raw text (REQ-HLF-005).
var hlInterpreters = map[string]bool{
	"python": true, "python3": true, "node": true,
	"perl": true, "ruby": true, "osascript": true,
}

// hlFindFilters are find predicates that narrow the walk; with one of them a
// delete is selective (HL-E-004) instead of a wholesale delete (HL-B-001).
var hlFindFilters = []string{
	"-name", "-iname", "-path", "-ipath", "-regex", "-iregex",
	"-type", "-mtime", "-mmin", "-newer", "-size", "-empty", "-user",
}

// MatchHardline returns the first hardline rule that matches a tool call,
// Block rules before Escalate rules, or nil. workspace is the call's working
// directory and home is the owner's home directory.
func MatchHardline(tool string, input json.RawMessage, workspace, home string) *core.HardlineMatch {
	if len(input) == 0 {
		return nil
	}
	text, ok := hardlineField(tool, input)
	if !ok {
		return nil
	}
	if home != "" {
		// Resolve home through symlinks once so comparisons are consistent
		// (on macOS a temp home lives under /var, a symlink to /private/var).
		home = resolveSymlinks(home)
	}
	switch tool {
	case "bash":
		return matchBash(text, workspace, home, 0)
	case "write_file", "edit_file":
		return matchWritePath(text, workspace, home)
	case "read_file":
		return matchReadPath(text, workspace, home)
	}
	return nil
}

// hardlineField reads the argument of a hardline tool. It returns false for
// every other tool, so MatchHardline returns nil.
func hardlineField(tool string, input json.RawMessage) (string, bool) {
	var value map[string]any
	if err := json.Unmarshal(input, &value); err != nil {
		return "", false
	}
	switch tool {
	case "bash":
		text, _ := value["command"].(string)
		return text, true
	case "write_file", "edit_file", "read_file":
		text, _ := value["path"].(string)
		return text, true
	}
	return "", false
}

// matchWritePath covers write_file and edit_file: a write or edit on a
// protected path is Block (HL-B-004).
func matchWritePath(p, cwd, home string) *core.HardlineMatch {
	if isProtectedPath(resolveHL(p, cwd, home), home) {
		return blockHardline(hlB004, reasonB004)
	}
	return nil
}

// matchReadPath covers read_file: reading a secret is Escalate (HL-E-005).
// Reading any other protected path, such as the config, is not a hardline hit.
func matchReadPath(p, cwd, home string) *core.HardlineMatch {
	if isSecretPath(resolveHL(p, cwd, home), home) {
		return escalateHardline(hlE005, reasonE005)
	}
	return nil
}

// matchBash analyzes a shell command. depth limits the recursion into
// bash|sh|zsh -c and eval strings (REQ-HLF-005).
func matchBash(command, cwd, home string, depth int) *core.HardlineMatch {
	// Heredoc bodies are data, not commands: split them out first so a body is
	// never read as its own shell command (spec 63). The opener line, with its
	// <<WORD operator, still goes through the segment analysis below.
	stripped, heredocs := splitHeredocs(command)
	var matches []*core.HardlineMatch
	// Whole-command checks that survive splitting (HL-B-003, HL-B-002,
	// HL-E-001).
	if forkBomb.MatchString(stripped) {
		matches = append(matches, blockHardline(hlB003, reasonB003))
	}
	if hlDiskRedirect.MatchString(stripped) {
		matches = append(matches, blockHardline(hlB002, reasonB002))
	}
	if pipesDownloadIntoInterpreter(stripped) {
		matches = append(matches, escalateHardline(hlE001, reasonE001))
	}
	for _, doc := range heredocs {
		if m := matchHeredoc(doc, cwd, home, depth); m != nil {
			matches = append(matches, m)
		}
	}
	segments, analyzable := pattern.SplitCommand(stripped)
	if !analyzable {
		// A command the precise splitter cannot analyze: check every rough
		// piece instead, then fall back to the raw text.
		segments = roughSegments(stripped)
	}
	dir := cwd
	for _, segment := range segments {
		if target, ok := cdTarget(segment); ok {
			dir = cleanAbs(expandHome(target, home), dir)
			continue
		}
		matches = append(matches, matchSimpleSegment(segment, dir, home)...)
		if depth < 1 {
			if inner, ok := nestedText(segment); ok {
				if m := matchBash(inner, dir, home, depth+1); m != nil {
					matches = append(matches, m)
				}
			}
		}
	}
	if !analyzable && len(matches) == 0 {
		if m := rawFallback(stripped, dir, home, depth); m != nil {
			matches = append(matches, m)
		}
	}
	return pickHardline(matches)
}

// matchSimpleSegment returns every hardline rule one simple command matches.
// The caller keeps Block over Escalate.
func matchSimpleSegment(segment, cwd, home string) []*core.HardlineMatch {
	fields, privilege := segmentFields(segment)
	var out []*core.HardlineMatch
	// A privilege wrapper (sudo or doas) is Escalate on its own, but the inner
	// command is still analyzed, so a Block from the inside wins.
	if privilege {
		out = append(out, escalateHardline(hlE002, reasonE002))
	}
	if len(fields) == 0 {
		return out
	}
	cmd := filepath.Base(fields[0])
	args := fields[1:]

	switch {
	case strings.HasPrefix(cmd, "mkfs"), strings.HasPrefix(cmd, "newfs"):
		out = append(out, blockHardline(hlB002, reasonB002))
	case cmd == "dd" && hasOfDev(args):
		out = append(out, blockHardline(hlB002, reasonB002))
	case cmd == "diskutil" && diskutilDangerous(args):
		out = append(out, blockHardline(hlB002, reasonB002))
	}
	if cmd == "su" {
		out = append(out, escalateHardline(hlE002, reasonE002))
	}
	if cmd == "git" && gitPushDangerous(args) {
		out = append(out, escalateHardline(hlE003, reasonE003))
	}
	if cmd == "crontab" && !crontabIsList(args) {
		out = append(out, escalateHardline(hlE006, reasonE006))
	}
	if cmd == "launchctl" && launchctlDangerous(args) {
		out = append(out, escalateHardline(hlE006, reasonE006))
	}
	out = append(out, deleteMatches(cmd, args, cwd, home)...)
	out = append(out, writeMatches(cmd, args, segment, cwd, home)...)
	out = append(out, secretMatches(cmd, args, cwd, home)...)
	if code, ok := interpreterCode(fields); ok {
		out = append(out, interpreterMatches(code, cwd, home)...)
	}
	return out
}

// deleteMatches covers the recursive-delete rules: HL-B-001 (root, a system
// directory, home itself, or an ancestor), HL-B-004 (a protected directory),
// and HL-E-004 (a direct child of home, or a filtered find -delete).
func deleteMatches(cmd string, args []string, cwd, home string) []*core.HardlineMatch {
	switch cmd {
	case "rm":
		if !rmRecursive(args) {
			return nil
		}
		var out []*core.HardlineMatch
		for _, target := range nonFlagArgs(args) {
			out = append(out, deleteTargetMatches(target, cwd, home, false)...)
		}
		return out
	case "find":
		if !findDeletes(args) {
			return nil
		}
		return deleteTargetMatches(findTarget(args), cwd, home, findFiltered(args))
	}
	return nil
}

// deleteTargetMatches classifies one recursive-delete target. filtered reports
// that a find walk is narrowed by a predicate, so a home, ancestor, or system
// target is selective (HL-E-004) rather than a wholesale delete (HL-B-001).
func deleteTargetMatches(raw, cwd, home string, filtered bool) []*core.HardlineMatch {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	// A recursive target ending in "/*" deletes every child of its parent;
	// classify it as if the parent itself were the target. A bare "*" is every
	// child of the working directory.
	if raw == "*" {
		raw = "."
	} else if strings.HasSuffix(raw, "/*") {
		raw = strings.TrimSuffix(raw, "/*")
		if raw == "" {
			raw = "/"
		}
	}
	cleaned := cleanArg(raw, cwd, home)
	resolved := resolveSymlinks(cleaned)
	// isSystemDir(cleaned) also catches a symlinked system path (/etc resolves
	// to /private/etc on macOS); withinFold(resolved, home) is true when
	// resolved is home or an ancestor of home.
	if isRoot(cleaned) || isSystemDir(cleaned) || isSystemDir(resolved) || withinFold(resolved, home) {
		if filtered {
			return []*core.HardlineMatch{escalateHardline(hlE004, reasonE004)}
		}
		return []*core.HardlineMatch{blockHardline(hlB001, reasonB001)}
	}
	if home == "" {
		return nil
	}
	if isProtectedDirTarget(resolved, home) {
		return []*core.HardlineMatch{blockHardline(hlB004, reasonB004)}
	}
	if isDirectChild(home, resolved) {
		return []*core.HardlineMatch{escalateHardline(hlE004, reasonE004)}
	}
	return nil
}

// writeMatches covers HL-B-004 (a write, delete, move, copy-onto, truncate,
// permission or owner change on a protected path) and the launch-agent half of
// HL-E-006 (a write under ~/Library/LaunchAgents/).
func writeMatches(cmd string, args []string, segment, cwd, home string) []*core.HardlineMatch {
	var out []*core.HardlineMatch
	files := nonFlagArgs(args)
	block := func(p string) {
		if isProtectedPath(resolveArg(p, cwd, home), home) {
			out = append(out, blockHardline(hlB004, reasonB004))
		}
	}
	agent := func(p string) {
		if isLaunchAgentPath(resolveArg(p, cwd, home), home) {
			out = append(out, escalateHardline(hlE006, reasonE006))
		}
	}
	blockDir := func(p string) {
		if isProtectedDirTarget(resolveArg(p, cwd, home), home) {
			out = append(out, blockHardline(hlB004, reasonB004))
		}
	}
	switch cmd {
	case "rm", "truncate", "chmod", "chown":
		for _, f := range files {
			block(f)
		}
	case "mv":
		// A move of a protected directory (or a write onto one) is Block; any
		// operand counts.
		for _, f := range files {
			block(f)
			blockDir(f)
		}
	case "cp":
		if len(files) > 0 {
			dest := files[len(files)-1]
			block(dest)
			agent(dest)
		}
	case "tee":
		for _, f := range files {
			block(f)
			agent(f)
		}
	case "sed":
		if sedInPlace(args) {
			for _, f := range files {
				block(f)
			}
		}
	case "ln":
		if lnSymlink(args) {
			for _, f := range files {
				block(f)
			}
		}
	}
	for _, m := range hlRedirectTarget.FindAllStringSubmatch(segment, -1) {
		target := m[1]
		block(target)
		agent(target)
	}
	return out
}

// secretMatches covers HL-E-005: a bash command that names a secret file.
func secretMatches(cmd string, args []string, cwd, home string) []*core.HardlineMatch {
	for _, a := range nonFlagArgs(args) {
		if isSecretPath(resolveArg(a, cwd, home), home) {
			return []*core.HardlineMatch{escalateHardline(hlE005, reasonE005)}
		}
	}
	return nil
}

// interpreterMatches checks inline interpreter code as raw text (REQ-HLF-005).
// It is a heuristic, not a sandbox: it looks for a delete call on the home
// directory or root (HL-B-001), and for a write to or read of a protected path
// (HL-B-004, HL-E-005).
func interpreterMatches(code, cwd, home string) []*core.HardlineMatch {
	var out []*core.HardlineMatch
	if hlDeleteCall.MatchString(code) && codeReferencesHomeOrRoot(code, cwd, home) {
		out = append(out, blockHardline(hlB001, reasonB001))
	}
	for _, tok := range codeTokens(code) {
		resolved := resolveArg(tok, cwd, home)
		if !isProtectedPath(resolved, home) {
			continue
		}
		if hlCodeWrite.MatchString(code) {
			out = append(out, blockHardline(hlB004, reasonB004))
		} else if isSecretPath(resolved, home) {
			out = append(out, escalateHardline(hlE005, reasonE005))
		}
	}
	return out
}

// codeReferencesHomeOrRoot reports whether inline code names the home directory
// or the filesystem root.
func codeReferencesHomeOrRoot(code, cwd, home string) bool {
	for _, ref := range []string{
		"process.env.HOME", "os.environ['HOME']", `os.environ["HOME"]`,
		"os.path.expanduser('~')", `os.path.expanduser("~")`,
		"Path.home()", "$ENV{HOME}", "ENV['HOME']", `ENV["HOME"]`,
	} {
		if strings.Contains(code, ref) {
			return true
		}
	}
	for _, tok := range codeTokens(code) {
		if tok == "/" {
			return true
		}
		if home != "" && strings.EqualFold(resolveArg(tok, cwd, home), home) {
			return true
		}
	}
	return false
}

// rawFallback handles a command pattern.SplitCommand cannot analyze and whose
// rough segments matched nothing (REQ-HLF-005). matchSimpleSegment already saw
// every visible segment, including the redirect targets it can read precisely,
// so the fallback covers only what the segment analysis cannot see: the bodies
// of command and process substitutions, and redirect targets that are not
// statically known. A Block command word inside an opaque body is Block; a
// protected path is Block only on a write; a secret that is only read is
// Escalate (HL-E-005).
func rawFallback(command, cwd, home string, depth int) *core.HardlineMatch {
	opaque := opaqueSpans(command)
	hidden := strings.Join(opaque, " ")
	if hlWordRm.MatchString(hidden) {
		return blockHardline(hlB001, reasonB001)
	}
	if hlWordDisk.MatchString(hidden) {
		return blockHardline(hlB002, reasonB002)
	}
	// A substitution body can hold a whole command; analyze each one once more
	// with the normal rules, up to the same depth limit as a nested shell.
	if depth < 1 {
		for _, body := range opaque {
			if m := matchBash(body, cwd, home, depth+1); m != nil {
				return m
			}
		}
	}
	protectedToken := func() bool {
		for _, token := range rawTokens(command) {
			if isProtectedPath(resolveHL(token, cwd, home), home) {
				return true
			}
		}
		return false
	}
	for _, m := range hlRedirectTarget.FindAllStringSubmatch(command, -1) {
		target := m[1]
		if isProtectedPath(resolveArg(target, cwd, home), home) {
			return blockHardline(hlB004, reasonB004)
		}
		if targetOpaque(target) && protectedToken() {
			return blockHardline(hlB004, reasonB004)
		}
	}
	if hlWriteVerb.MatchString(hidden) && protectedToken() {
		return blockHardline(hlB004, reasonB004)
	}
	// A secret that is named but only read is Escalate (HL-E-005).
	for _, token := range rawTokens(command) {
		if isSecretPath(resolveHL(token, cwd, home), home) {
			return escalateHardline(hlE005, reasonE005)
		}
	}
	return nil
}

// opaqueSpans returns the bodies of the command and process substitutions in a
// raw command: $(...), `...`, <(...), and >(...). Plain text, including text in
// single quotes, is skipped; a substitution inside a substitution is returned as
// part of its outer body.
func opaqueSpans(command string) []string {
	var out []string
	i, n := 0, len(command)
	single := false
	for i < n {
		c := command[i]
		if single {
			if c == '\'' {
				single = false
			}
			i++
			continue
		}
		switch {
		case c == '\\' && i+1 < n:
			i += 2
		case c == '\'':
			single = true
			i++
		case c == '$' && i+1 < n && command[i+1] == '(':
			body, next := balancedBody(command, i+2)
			out = append(out, body)
			i = next
		case (c == '<' || c == '>') && i+1 < n && command[i+1] == '(':
			body, next := balancedBody(command, i+2)
			out = append(out, body)
			i = next
		case c == '`':
			body, next := backtickBody(command, i+1)
			out = append(out, body)
			i = next
		default:
			i++
		}
	}
	return out
}

// balancedBody returns the text between a substitution's "(" and its matching
// ")" and the index just past the ")". It counts nested parentheses and skips
// quoted text. An unbalanced body runs to the end of the command.
func balancedBody(s string, start int) (string, int) {
	depth := 1
	i := start
	var quote byte
	for i < len(s) {
		c := s[i]
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			}
			i++
			continue
		}
		if quote == '"' {
			if c == '\\' && i+1 < len(s) {
				i += 2
				continue
			}
			if c == '"' {
				quote = 0
			}
			i++
			continue
		}
		switch {
		case c == '\\' && i+1 < len(s):
			i += 2
		case c == '\'':
			quote = '\''
			i++
		case c == '"':
			quote = '"'
			i++
		case c == '(':
			depth++
			i++
		case c == ')':
			depth--
			if depth == 0 {
				return s[start:i], i + 1
			}
			i++
		default:
			i++
		}
	}
	return s[start:], len(s)
}

// backtickBody returns the text between a pair of backticks and the index just
// past the closing backtick. An unbalanced body runs to the end of the command.
func backtickBody(s string, start int) (string, int) {
	for i := start; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			continue
		}
		if s[i] == '`' {
			return s[start:i], i + 1
		}
	}
	return s[start:], len(s)
}

// targetOpaque reports whether a redirect target is not statically known: it
// contains a command substitution or a backtick.
func targetOpaque(target string) bool {
	return strings.Contains(target, "$(") || strings.Contains(target, "`")
}

// ---- path resolution (REQ-HLF-004) ----

// resolveHL expands home expressions, makes the path absolute against cwd,
// cleans it, and resolves symlinks on the longest existing prefix. It is for
// tool arguments (write_file, read_file), which are not shell-quoted.
func resolveHL(p, cwd, home string) string {
	return resolveSymlinks(cleanAbs(expandHome(p, home), cwd))
}

// resolveArg resolves one shell argument for path analysis (REQ-HLF-004). It
// drops one pair of surrounding quotes; a single-quoted argument is literal (no
// expansion). In a bare or double-quoted argument $HOME and ${HOME} expand; a
// leading ~ expands only in a bare argument.
func resolveArg(raw, cwd, home string) string {
	return resolveSymlinks(cleanArg(raw, cwd, home))
}

// cleanArg is resolveArg without symlink resolution.
func cleanArg(raw, cwd, home string) string {
	switch {
	case len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'':
		return cleanAbs(raw[1:len(raw)-1], cwd)
	case len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"':
		return cleanAbs(expandHomeVars(raw[1:len(raw)-1], home), cwd)
	default:
		return cleanAbs(expandHome(expandHomeVars(raw, home), home), cwd)
	}
}

// cleanAbs makes a path absolute against cwd and cleans it.
func cleanAbs(p, cwd string) string {
	if !filepath.IsAbs(p) {
		if cwd == "" {
			cwd = "."
		}
		p = filepath.Join(cwd, p)
	}
	return filepath.Clean(p)
}

// expandHome replaces a leading ~, ~/, $HOME, or ${HOME} with home.
func expandHome(p, home string) string {
	if home == "" {
		return p
	}
	for _, prefix := range []string{"~/", "${HOME}/", "$HOME/"} {
		if strings.HasPrefix(p, prefix) {
			return filepath.Join(home, p[len(prefix):])
		}
	}
	switch p {
	case "~", "${HOME}", "$HOME":
		return home
	}
	return p
}

// expandHomeVars replaces $HOME and ${HOME} with home. It is applied to bare
// and double-quoted arguments; single-quoted text is never expanded.
func expandHomeVars(s, home string) string {
	if home == "" {
		return s
	}
	s = strings.ReplaceAll(s, "${HOME}", home)
	return strings.ReplaceAll(s, "$HOME", home)
}

// resolveSymlinks resolves the symlinks of the longest existing prefix of p and
// rejoins the rest.
func resolveSymlinks(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	cur := p
	rest := ""
	for {
		base := filepath.Base(cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(base, rest)
		cur = parent
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(resolved, rest)
		}
	}
}

// withinFold reports whether p is dir or lies below it, case-insensitively.
func withinFold(dir, p string) bool {
	if dir == "" || p == "" {
		return false
	}
	if strings.EqualFold(dir, p) {
		return true
	}
	prefix := strings.TrimRight(dir, string(os.PathSeparator)) + string(os.PathSeparator)
	return strings.HasPrefix(strings.ToLower(p), strings.ToLower(prefix))
}

// isDirectChild reports whether p is a direct child of parent.
func isDirectChild(parent, p string) bool {
	return parent != "" && p != "" && strings.EqualFold(filepath.Dir(p), parent)
}

// isRoot reports whether p is the filesystem root.
func isRoot(p string) bool {
	return p == string(filepath.Separator)
}

// isSystemDir reports whether p is one of the top-level system directories.
func isSystemDir(p string) bool {
	for _, d := range hlSystemDirs {
		if strings.EqualFold(p, d) {
			return true
		}
	}
	return false
}

// isProtectedDirTarget reports whether resolved is a protected path's directory
// (~/.lana or ~/.ssh) or an ancestor of one: deleting it would take out the
// protected paths inside (HL-B-004).
func isProtectedDirTarget(resolved, home string) bool {
	if home == "" || resolved == "" {
		return false
	}
	for _, d := range []string{filepath.Join(home, ".lana"), filepath.Join(home, ".ssh")} {
		if withinFold(resolved, d) {
			return true
		}
	}
	return false
}

// isProtectedPath reports whether resolved is a protected path (Definitions,
// spec 63).
func isProtectedPath(resolved, home string) bool {
	if home == "" || resolved == "" {
		return false
	}
	lana := filepath.Join(home, ".lana")
	if strings.EqualFold(resolved, filepath.Join(lana, "secrets.env")) ||
		strings.EqualFold(resolved, filepath.Join(lana, "daemon.secret")) ||
		strings.EqualFold(resolved, filepath.Join(lana, "config.toml")) {
		return true
	}
	if strings.EqualFold(filepath.Dir(resolved), lana) {
		base := strings.ToLower(filepath.Base(resolved))
		if strings.HasPrefix(base, "lana.db") || strings.HasPrefix(base, "knowledge.db") {
			return true
		}
	}
	ssh := filepath.Join(home, ".ssh")
	return withinFold(ssh, resolved)
}

// isSecretPath reports whether resolved is a secret file of HL-E-005:
// ~/.lana/secrets.env, ~/.lana/daemon.secret, or ~/.ssh/id_* except *.pub.
func isSecretPath(resolved, home string) bool {
	if home == "" || resolved == "" {
		return false
	}
	lana := filepath.Join(home, ".lana")
	if strings.EqualFold(resolved, filepath.Join(lana, "secrets.env")) ||
		strings.EqualFold(resolved, filepath.Join(lana, "daemon.secret")) {
		return true
	}
	ssh := filepath.Join(home, ".ssh")
	if strings.EqualFold(filepath.Dir(resolved), ssh) {
		base := strings.ToLower(filepath.Base(resolved))
		if strings.HasPrefix(base, "id_") && !strings.HasSuffix(base, ".pub") {
			return true
		}
	}
	return false
}

// isLaunchAgentPath reports whether resolved is under ~/Library/LaunchAgents/.
func isLaunchAgentPath(resolved, home string) bool {
	if home == "" || resolved == "" {
		return false
	}
	return withinFold(filepath.Join(home, "Library", "LaunchAgents"), resolved)
}

// ---- segment helpers ----

// segmentFields splits a segment into its command fields after dropping
// redirections and leading variable assignments and wrapper commands. It
// reports whether a privilege wrapper (sudo or doas) was present
// (REQ-HLF-005).
func segmentFields(segment string) ([]string, bool) {
	return stripWrappers(stripRedirects(strings.Fields(segment)))
}

// stripRedirects removes the shell redirections from a segment's fields, so the
// per-command rules never read a redirect operator or its target as an
// argument. A redirect that carries its target in the same field (>file,
// >>file, N>&M, <<WORD) drops that field alone; a bare operator (> or <) also
// drops the next field, its target. A quoted field that merely contains ">"
// (for example "a>b") is not a redirection and is kept.
func stripRedirects(fields []string) []string {
	var out []string
	for i := 0; i < len(fields); i++ {
		op := hlRedirectField.FindString(fields[i])
		if op == "" {
			out = append(out, fields[i])
			continue
		}
		if len(fields[i]) == len(op) && i+1 < len(fields) {
			i++ // the target is the next field
		}
	}
	return out
}

// stripWrappers removes leading wrapper commands and their own options, so the
// inner command is analyzed as a normal segment. It applies repeatedly, since
// wrappers nest (sudo env rm …).
func stripWrappers(fields []string) ([]string, bool) {
	privilege := false
	for {
		fields = stripAssignments(fields)
		if len(fields) == 0 {
			return fields, privilege
		}
		cmd := filepath.Base(fields[0])
		switch cmd {
		case "sudo", "doas":
			privilege = true
			fields = skipWrapperOptions(fields[1:], "ugCDhpUrt")
		case "env":
			fields = skipWrapperOptions(fields[1:], "u")
			fields = stripAssignments(fields)
		case "xargs":
			fields = skipWrapperOptions(fields[1:], "InPLdEs")
		case "nice":
			fields = skipWrapperOptions(fields[1:], "n")
		case "timeout":
			fields = skipWrapperOptions(fields[1:], "s")
			if len(fields) > 0 {
				fields = fields[1:] // the duration
			}
		case "command", "builtin", "exec", "nohup", "time", "stdbuf", "caffeinate":
			fields = skipWrapperOptions(fields[1:], "")
		default:
			return fields, privilege
		}
	}
}

// skipWrapperOptions drops the leading options of a wrapper command. valueFlags
// lists the short option letters that consume the next argument (for example
// "u" for sudo -u NAME). Long options are treated as value-less, and a short
// option whose value is attached (-uroot, -n5) consumes nothing.
func skipWrapperOptions(args []string, valueFlags string) []string {
	for len(args) > 0 {
		a := args[0]
		if a == "--" {
			return args[1:]
		}
		if a == "-" || !strings.HasPrefix(a, "-") {
			return args
		}
		args = args[1:]
		if strings.HasPrefix(a, "--") {
			continue
		}
		body := a[1:]
		if len(body) == 1 && strings.ContainsRune(valueFlags, rune(body[0])) && len(args) > 0 {
			args = args[1:]
		}
	}
	return args
}

// stripAssignments drops leading VAR=value assignments.
func stripAssignments(fields []string) []string {
	for len(fields) > 0 && strings.Contains(fields[0], "=") && !strings.HasPrefix(fields[0], "-") {
		fields = fields[1:]
	}
	return fields
}

// nonFlagArgs returns the arguments that are not options.
func nonFlagArgs(args []string) []string {
	var out []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") && a != "-" {
			continue
		}
		out = append(out, a)
	}
	return out
}

// firstNonFlag returns the first argument that is not an option.
func firstNonFlag(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// rmRecursive reports whether rm was given a recursive flag (-r, -R,
// --recursive, or a combined cluster such as -rf).
func rmRecursive(args []string) bool {
	for _, a := range args {
		if a == "--recursive" {
			return true
		}
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && len(a) > 1 &&
			strings.ContainsAny(a[1:], "rR") {
			return true
		}
	}
	return false
}

// findDeletes reports whether a find command deletes: -delete, or -exec rm.
func findDeletes(args []string) bool {
	for i, a := range args {
		switch a {
		case "-delete":
			return true
		case "-exec":
			if i+1 < len(args) {
				switch args[i+1] {
				case "rm", "rmdir", "shred":
					return true
				}
			}
		}
	}
	return false
}

// findFiltered reports whether a find walk is narrowed by a predicate.
func findFiltered(args []string) bool {
	for _, a := range args {
		for _, f := range hlFindFilters {
			if a == f {
				return true
			}
		}
	}
	return false
}

// findTarget returns the search path of a find command, defaulting to ".".
func findTarget(args []string) string {
	if t := firstNonFlag(args); t != "" {
		return t
	}
	return "."
}

// hasOfDev reports whether dd writes to a raw device (of=/dev/...).
func hasOfDev(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "of=/dev/") {
			return true
		}
	}
	return false
}

// diskutilDangerous reports whether diskutil acts on a volume or disk.
func diskutilDangerous(args []string) bool {
	sub := firstNonFlag(args)
	if sub == "" {
		return false
	}
	if strings.HasPrefix(sub, "erase") {
		return true
	}
	switch sub {
	case "partitionDisk", "zeroDisk", "secureErase":
		return true
	}
	return false
}

// gitPushDangerous reports whether git push forces or removes a remote ref.
func gitPushDangerous(args []string) bool {
	if len(args) == 0 || args[0] != "push" {
		return false
	}
	for _, a := range args[1:] {
		switch {
		case a == "--force", a == "--delete", a == "--mirror", a == "-f":
			return true
		case a == "--force-with-lease", strings.HasPrefix(a, "--force-with-lease="):
			return true
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a[1:], "f"):
			return true
		}
	}
	return false
}

// crontabIsList reports whether crontab only lists the table.
func crontabIsList(args []string) bool {
	return len(args) == 1 && args[0] == "-l"
}

// launchctlDangerous reports whether launchctl installs or arms a job.
func launchctlDangerous(args []string) bool {
	switch firstNonFlag(args) {
	case "load", "bootstrap", "enable":
		return true
	}
	return false
}

// sedInPlace reports whether sed edits a file in place.
func sedInPlace(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-i") && !strings.HasPrefix(a, "--") {
			return true
		}
	}
	return false
}

// lnSymlink reports whether ln creates a symbolic link.
func lnSymlink(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a[1:], "s") {
			return true
		}
	}
	return false
}

// cdTarget returns the directory of a leading cd segment.
func cdTarget(segment string) (string, bool) {
	m := hlCd.FindStringSubmatch(strings.TrimSpace(segment))
	if m == nil {
		return "", false
	}
	return unquote(strings.TrimSpace(m[1])), true
}

// nestedText returns the inner string of a shell's -c call or an eval, to be
// analyzed once more (REQ-HLF-005).
func nestedText(segment string) (string, bool) {
	fields, _ := segmentFields(segment)
	if len(fields) == 0 {
		return "", false
	}
	return shellCommand(fields)
}

// shellCommand returns the string a shell -c flag or eval would run. It accepts
// a path-qualified shell name (/bin/bash), the shells bash, sh, zsh, dash, ksh,
// fish, and a combined flag cluster that contains c (for example -lc, -ec).
func shellCommand(fields []string) (string, bool) {
	cmd := filepath.Base(fields[0])
	if cmd == "eval" && len(fields) > 1 {
		return unquote(strings.Join(fields[1:], " ")), true
	}
	if !hlShellNames[cmd] {
		return "", false
	}
	for i := 1; i < len(fields); i++ {
		a := fields[i]
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a[1:], "c") {
			return unquote(strings.Join(fields[i+1:], " ")), true
		}
	}
	return "", false
}

// interpreterCode returns the inline program of an interpreter called with -c
// or -e, for example python3 -c "…" or node -e '…'.
func interpreterCode(fields []string) (string, bool) {
	if len(fields) == 0 || !hlInterpreters[filepath.Base(fields[0])] {
		return "", false
	}
	for i := 1; i < len(fields); i++ {
		if fields[i] != "-c" && fields[i] != "-e" {
			continue
		}
		return unquote(strings.Join(fields[i+1:], " ")), true
	}
	return "", false
}

// pipesDownloadIntoInterpreter reports whether command pipes a curl or wget
// download into a shell or interpreter that reads its program from standard
// input (HL-E-001). A piped interpreter that takes its program from an inline
// flag (-c, -e, -m) or from a script file is only parsing the download as data,
// so it is not a match.
func pipesDownloadIntoInterpreter(command string) bool {
	for _, m := range hlPipeToShell.FindAllStringSubmatch(command, -1) {
		if interpreterReadsStdin(m[1]) {
			return true
		}
	}
	return false
}

// interpreterReadsStdin reports whether the arguments of a piped shell or
// interpreter make it read its program from standard input. No arguments, "-",
// or "-s" means the program is stdin. An option cluster that contains c, e, or
// m carries the program inline (-c), as an expression (-e), or as a module
// (-m), and the first non-option argument is a script file: for both, stdin is
// only data.
func interpreterReadsStdin(args string) bool {
	fields, _ := segmentFields(args)
	for _, a := range fields {
		switch {
		case a == "-" || a == "-s":
			return true
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a[1:], "cem"):
			return false
		case strings.HasPrefix(a, "-"):
			continue
		default:
			return false
		}
	}
	return true
}

// ---- heredocs (spec 63) ----

// hlHeredoc is one heredoc read out of a command: the body text an interpreter
// or shell would read from standard input, and the simple command segment that
// owns its <<WORD operator.
type hlHeredoc struct {
	body   string // the body text, without the terminator line
	owner  string // the segment that carries the <<WORD operator
	opText string // the operator as written, for example "<<'EOF'"
	tail   string // the rest of the opener line after the operator
}

// hlHeredocOp is one heredoc operator found in a line.
type hlHeredocOp struct {
	text      string // the operator as written, for example "<<'EOF'"
	word      string // the delimiter word, with quotes removed
	stripTabs bool   // <<- strips leading tabs from the body and the terminator
	end       int    // index just past the operator
}

// hlHeredocShells are the shells whose heredoc body is analyzed as commands when
// the shell reads its program from standard input.
var hlHeredocShells = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true,
}

// splitHeredocs removes every heredoc body and its terminator line from a
// command, so pattern.SplitCommand never reads a body as a shell command, and
// returns the remaining text with one entry per heredoc. A body runs from the
// line after "…<<WORD" to a line equal to WORD (leading tabs are stripped for
// <<-); an unterminated body runs to the end of the text. A here-string (<<<)
// and a "<<" inside quotes are left in place.
func splitHeredocs(command string) (string, []hlHeredoc) {
	if !strings.Contains(command, "<<") {
		return command, nil
	}
	lines := strings.Split(command, "\n")
	var out []string
	var docs []hlHeredoc
	for i := 0; i < len(lines); {
		line := lines[i]
		ops := heredocOps(line)
		out = append(out, line)
		i++
		for _, op := range ops {
			var body []string
			for i < len(lines) {
				cmp := lines[i]
				if op.stripTabs {
					cmp = strings.TrimLeft(cmp, "\t")
				}
				if cmp == op.word {
					i++ // consume the terminator line
					break
				}
				body = append(body, lines[i])
				i++
			}
			docs = append(docs, hlHeredoc{
				body:   strings.Join(body, "\n"),
				owner:  heredocOwner(line, op.text),
				opText: op.text,
				tail:   line[op.end:],
			})
		}
	}
	return strings.Join(out, "\n"), docs
}

// matchHeredoc classifies one heredoc by the simple command that owns its
// operator (spec 63). An interpreter or shell that reads its program from
// standard input runs the body as code: the interpreter body goes through the
// inline-code heuristic and the shell body goes through matchBash one level
// deeper. For a data command (cat, tee, echo, printf, …) a later stage of the
// same pipeline may be the consumer, so that case falls through to
// matchHeredocConsumer. The owner command is still analyzed as a normal segment,
// whatever the body turns out to be.
func matchHeredoc(doc hlHeredoc, cwd, home string, depth int) *core.HardlineMatch {
	fields, _ := segmentFields(heredocCommandText(doc.owner, doc.opText))
	if len(fields) > 0 {
		cmd := filepath.Base(fields[0])
		args := fields[1:]
		switch {
		case hlInterpreters[cmd]:
			if interpreterReadsStdin(strings.Join(args, " ")) {
				return pickHardline(interpreterMatches(doc.body, cwd, home))
			}
			return nil
		case hlHeredocShells[cmd]:
			if shellReadsStdin(args) && depth < 1 {
				return matchBash(doc.body, cwd, home, depth+1)
			}
			return nil
		}
	}
	return matchHeredocConsumer(doc, cwd, home, depth)
}

// matchHeredocConsumer looks for a later stage of a data command's pipeline
// that reads the heredoc body from standard input (spec 63). When the owner is
// a data command, a shell or interpreter further along the same line runs the
// body as code: the shell body is analyzed with matchBash and the interpreter
// body with interpreterMatches. A stage that only parses its input (grep, jq,
// a shell given -c, an interpreter given -c or a script) is not a consumer.
func matchHeredocConsumer(doc hlHeredoc, cwd, home string, depth int) *core.HardlineMatch {
	for _, stage := range splitPipeline(doc.tail)[1:] {
		fields, _ := segmentFields(stage)
		if len(fields) == 0 {
			continue
		}
		cmd := filepath.Base(fields[0])
		args := fields[1:]
		switch {
		case hlHeredocShells[cmd]:
			if shellReadsStdin(args) && depth < 1 {
				if m := matchBash(doc.body, cwd, home, depth+1); m != nil {
					return m
				}
			}
		case hlInterpreters[cmd]:
			if interpreterReadsStdin(strings.Join(args, " ")) {
				if m := pickHardline(interpreterMatches(doc.body, cwd, home)); m != nil {
					return m
				}
			}
		}
	}
	return nil
}

// heredocCommandText removes the heredoc operator from a segment, so a command
// glued to its operator (bash<<EOF, python3 -<<EOF, cat<<'EOF') is classified
// by its command word alone.
func heredocCommandText(owner, opText string) string {
	if opText == "" {
		return owner
	}
	return strings.Replace(owner, opText, " ", 1)
}

// splitPipeline splits the text after a heredoc operator on single "|"
// separators, leaving "||" alone and ignoring pipes inside quotes. The first
// part is the remainder of the owner segment; every later part is a pipeline
// stage.
func splitPipeline(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); {
		switch s[i] {
		case '\'':
			i = skipQuotedOrEnd(s, i, '\'')
		case '"':
			i = skipQuotedOrEnd(s, i, '"')
		case '\\':
			i += 2
		case '|':
			if i+1 < len(s) && s[i+1] == '|' {
				i += 2
				continue
			}
			parts = append(parts, s[start:i])
			i++
			start = i
		default:
			i++
		}
	}
	return append(parts, s[start:])
}

// shellReadsStdin reports whether a shell reads its program from standard input:
// no script argument, "-", or "-s". A shell given an inline -c string or a
// script file takes its program elsewhere, so its heredoc is only data.
func shellReadsStdin(args []string) bool {
	for _, a := range args {
		switch {
		case a == "-" || a == "-s":
			return true
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--"):
			if strings.Contains(a[1:], "c") {
				return false
			}
		case !strings.HasPrefix(a, "-"):
			return false
		}
	}
	return true
}

// heredocOps returns the heredoc operators of a line, in order. A here-string
// (<<<) and a "<<" inside single or double quotes are not operators.
func heredocOps(line string) []hlHeredocOp {
	var out []hlHeredocOp
	for i := 0; i < len(line); {
		c := line[i]
		switch {
		case c == '\'':
			i = skipQuotedOrEnd(line, i, '\'')
		case c == '"':
			i = skipQuotedOrEnd(line, i, '"')
		case c == '\\':
			i += 2
		case c == '<' && i+1 < len(line) && line[i+1] == '<' &&
			(i+2 >= len(line) || line[i+2] != '<'):
			op, next := heredocOp(line, i)
			if op.text == "" {
				i++
				continue
			}
			op.end = next
			out = append(out, op)
			i = next
		default:
			i++
		}
	}
	return out
}

// heredocOp reads a heredoc operator at i, where line[i:i+2] == "<<". It returns
// the operator and the index just past it; an empty text means it is not a
// heredoc (a missing delimiter).
func heredocOp(line string, i int) (hlHeredocOp, int) {
	j := i + 2
	stripTabs := false
	if j < len(line) && line[j] == '-' {
		stripTabs = true
		j++
	}
	if j >= len(line) {
		return hlHeredocOp{}, i + 1
	}
	if q := line[j]; q == '\'' || q == '"' {
		if end := skipQuoted(line, j, q); end >= 0 {
			return hlHeredocOp{text: line[i:end], word: line[j+1 : end-1], stripTabs: stripTabs}, end
		}
		return hlHeredocOp{}, i + 1
	}
	k := j
	for k < len(line) && heredocWordByte(line[k]) {
		k++
	}
	if k == j {
		return hlHeredocOp{}, i + 1
	}
	return hlHeredocOp{text: line[i:k], word: line[j:k], stripTabs: stripTabs}, k
}

// skipQuoted returns the index just past a quoted run that starts at open, or -1
// when the quote is never closed.
func skipQuoted(s string, open int, quote byte) int {
	for i := open + 1; i < len(s); i++ {
		if quote == '"' && s[i] == '\\' && i+1 < len(s) {
			i++
			continue
		}
		if s[i] == quote {
			return i + 1
		}
	}
	return -1
}

// skipQuotedOrEnd is skipQuoted but treats an unterminated quote as running to
// the end of the string.
func skipQuotedOrEnd(s string, open int, quote byte) int {
	if end := skipQuoted(s, open, quote); end >= 0 {
		return end
	}
	return len(s)
}

// heredocWordByte reports whether b may appear in an unquoted heredoc word.
func heredocWordByte(b byte) bool {
	switch b {
	case ' ', '\t', '\r', '\n', ';', '|', '&', '<', '>', '(', ')', '"', '\'', '`':
		return false
	}
	return true
}

// heredocOwner returns the simple command segment of a line that owns the
// operator text opText, so its program-reading behavior can be classified. The
// match respects the operator's word boundary, so "<<EOF" does not match
// "<<EOF2".
func heredocOwner(line, opText string) string {
	segments, _ := pattern.SplitCommand(line)
	for _, seg := range segments {
		if heredocHasOp(seg, opText) {
			return seg
		}
	}
	return strings.TrimSpace(line)
}

// heredocHasOp reports whether seg contains the operator text opText not
// followed by another word byte.
func heredocHasOp(seg, opText string) bool {
	for idx := 0; idx < len(seg); {
		j := strings.Index(seg[idx:], opText)
		if j < 0 {
			return false
		}
		j += idx
		end := j + len(opText)
		if end >= len(seg) || !heredocWordByte(seg[end]) {
			return true
		}
		idx = j + 1
	}
	return false
}

// unquote drops one pair of surrounding single or double quotes.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// rawTokens splits raw command text on whitespace and shell metacharacters.
func rawTokens(command string) []string {
	return strings.FieldsFunc(command, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', ';', '|', '&', '(', ')', '<', '>', '\'', '"', '`':
			return true
		}
		return false
	})
}

// codeTokens returns the path-like tokens of inline interpreter code, with the
// surrounding punctuation trimmed.
func codeTokens(code string) []string {
	var out []string
	for _, tok := range rawTokens(code) {
		tok = strings.TrimLeft(tok, "([{")
		tok = strings.TrimRight(tok, ")]},;:")
		if tok == "" || !strings.ContainsAny(tok, "/~$") {
			continue
		}
		out = append(out, tok)
	}
	return out
}

// ---- results ----

func blockHardline(rule, reason string) *core.HardlineMatch {
	return &core.HardlineMatch{Rule: rule, Tier: core.HardlineBlock, Reason: reason}
}

func escalateHardline(rule, reason string) *core.HardlineMatch {
	return &core.HardlineMatch{Rule: rule, Tier: core.HardlineEscalate, Reason: reason}
}

// pickHardline returns the first Block match, else the first Escalate match.
func pickHardline(matches []*core.HardlineMatch) *core.HardlineMatch {
	var escalate *core.HardlineMatch
	for _, m := range matches {
		if m == nil {
			continue
		}
		if m.Tier == core.HardlineBlock {
			return m
		}
		if escalate == nil {
			escalate = m
		}
	}
	return escalate
}
