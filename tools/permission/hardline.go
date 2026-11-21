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
	// hlPipeToShell is pipeToShell (auto.go) extended with python, node, and
	// perl (REQ-HLF-003, HL-E-001).
	hlPipeToShell = regexp.MustCompile(`(?i)\b(curl|wget)\b[^|]*\|\s*(sudo\s+)?((ba|z|da)?sh|python3?|node|perl)\b`)
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
	var matches []*core.HardlineMatch
	// Whole-command checks that survive splitting (HL-B-003, HL-B-002,
	// HL-E-001).
	if forkBomb.MatchString(command) {
		matches = append(matches, blockHardline(hlB003, reasonB003))
	}
	if hlDiskRedirect.MatchString(command) {
		matches = append(matches, blockHardline(hlB002, reasonB002))
	}
	if hlPipeToShell.MatchString(command) {
		matches = append(matches, escalateHardline(hlE001, reasonE001))
	}
	segments, analyzable := pattern.SplitCommand(command)
	if !analyzable {
		// A command the precise splitter cannot analyze: check every rough
		// piece instead, then fall back to the raw text.
		segments = roughSegments(command)
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
		if m := rawFallback(command, dir, home); m != nil {
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
// rough segments matched nothing (REQ-HLF-005). A raw Block command word is
// treated as Block; a raw protected path is Block when the text reads like a
// write, and Escalate when it names a secret.
func rawFallback(command, cwd, home string) *core.HardlineMatch {
	if hlWordRm.MatchString(command) {
		return blockHardline(hlB001, reasonB001)
	}
	if hlWordDisk.MatchString(command) {
		return blockHardline(hlB002, reasonB002)
	}
	for _, token := range rawTokens(command) {
		resolved := resolveHL(token, cwd, home)
		if !isProtectedPath(resolved, home) {
			continue
		}
		if hlWriteVerb.MatchString(command) || strings.Contains(command, ">") {
			return blockHardline(hlB004, reasonB004)
		}
		if isSecretPath(resolved, home) {
			return escalateHardline(hlE005, reasonE005)
		}
	}
	return nil
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

// segmentFields splits a segment into its command fields after dropping leading
// variable assignments and wrapper commands. It reports whether a privilege
// wrapper (sudo or doas) was present (REQ-HLF-005).
func segmentFields(segment string) ([]string, bool) {
	return stripWrappers(strings.Fields(segment))
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
