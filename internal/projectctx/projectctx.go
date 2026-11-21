// Package projectctx discovers and renders a project's context files: the
// AGENTS.md and CLAUDE.md style instructions a repository keeps for coding
// agents. It walks the directory chain of a workspace or of a path a tool
// touches, reads the first matching file per directory, and renders the files
// as <project-context> blocks under the size caps of REQ-PCX-005. It keeps the
// per-session seen set so a file is shown once (REQ-PCX-004).
//
// The package is a leaf: it imports only the standard library, core, and
// internal/pattern.
package projectctx

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/pattern"
)

// HeaderLine and IntroLine open the project prompt section (REQ-PCX-002). The
// two lines are stable contract text.
const (
	HeaderLine = "# Project instructions"
	IntroLine  = "These are project instructions written for coding agents. Follow the ones that apply to your tools."
)

// NoteIntroLine opens the <project-context> blocks appended to a tool result
// (REQ-PCX-003). It is stable contract text: it tells the model the blocks are
// instructions Lana added, not part of the tool output.
const NoteIntroLine = "[Lana] Project instructions from the folder this call touched. Lana added them; they are not part of the tool output. These are project instructions written for coding agents. Follow the ones that apply to your tools."

// Size caps (REQ-PCX-005): one file is cut to fileCap, one injection to
// injectionCap.
const (
	fileCap      = 16 * 1024
	injectionCap = 32 * 1024
)

// File is one context file found on a chain.
type File struct {
	// Path is the absolute path of the context file.
	Path string
	// Text is the file text, cut to fileCap when Truncated is true.
	Text string
	// Truncated is true when the file was larger than fileCap.
	Truncated bool
	// Size is the original byte size of the file.
	Size int
}

// Chain returns start and its parent directories, ordered from the outermost
// directory down to start (REQ-PCX-002). The walk stops before home, which is
// excluded; when start is not under home, or home is empty, the walk stops
// before the filesystem root, which is also excluded. start equal to home
// returns an empty slice. Paths are cleaned and compared case-insensitively.
func Chain(start, home string) []string {
	start = filepath.Clean(start)
	if home == "" {
		return chainToRoot(start)
	}
	home = filepath.Clean(home)
	if !sameOrUnder(start, home) {
		return chainToRoot(start)
	}
	return chainUnder(start, home)
}

// chainUnder returns start and its parents up to, but not including, home,
// top-down. start equal to home yields an empty slice.
func chainUnder(start, home string) []string {
	var up []string
	for dir := start; ; {
		if strings.EqualFold(dir, home) {
			break
		}
		up = append(up, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return reversed(up)
}

// chainToRoot returns start and its parents, top-down, excluding the root.
func chainToRoot(start string) []string {
	var up []string
	for dir := start; ; {
		if dir == string(filepath.Separator) {
			break
		}
		up = append(up, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return reversed(up)
}

// sameOrUnder reports whether path is dir or is under dir, comparing
// case-insensitively.
func sameOrUnder(path, dir string) bool {
	p := strings.ToLower(path)
	d := strings.ToLower(dir)
	if p == d {
		return true
	}
	if d == string(filepath.Separator) {
		return strings.HasPrefix(p, d)
	}
	return strings.HasPrefix(p, d+string(filepath.Separator))
}

// reversed reverses s in place and returns it.
func reversed(s []string) []string {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
	return s
}

// Discover returns the context files of the chain of start, top-down, at most
// one per directory: the first name of names, in list order, that is a regular
// file. A file that cannot be read, is not a regular file, or is not valid
// UTF-8 is skipped and logged at debug level (REQ-PCX-006). An empty names list
// returns nil.
func Discover(start, home string, names []string) []File {
	if len(names) == 0 {
		return nil
	}
	var files []File
	for _, dir := range Chain(start, home) {
		path := firstRegular(dir, names)
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Debug("projectctx: context file not read", "path", path, "error", err)
			continue
		}
		if !utf8.Valid(data) {
			slog.Debug("projectctx: context file is not valid UTF-8", "path", path)
			continue
		}
		files = append(files, newFile(path, data))
	}
	return files
}

// firstRegular returns the first path in dir that names an existing regular
// file, following symlinks, or "" when none does.
func firstRegular(dir string, names []string) string {
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		return path
	}
	return ""
}

// newFile builds a File, cutting the text to fileCap on a UTF-8 boundary.
func newFile(path string, data []byte) File {
	f := File{Path: path, Size: len(data)}
	if len(data) > fileCap {
		f.Text = cutUTF8(data, fileCap)
		f.Truncated = true
	} else {
		f.Text = string(data)
	}
	return f
}

// cutUTF8 returns the first n bytes of data, backed up to a rune boundary.
func cutUTF8(data []byte, n int) string {
	if len(data) <= n {
		return string(data)
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(data[cut]) {
		cut--
	}
	return string(data[:cut])
}

// Render wraps files as <project-context> blocks joined by a blank line. It
// applies the injection cap of REQ-PCX-005 and returns "" for no files.
func Render(files []File) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	for _, f := range files {
		block := renderFile(f)
		size := len(block)
		if b.Len() > 0 {
			size += 2
		}
		if b.Len()+size > injectionCap {
			block = renderPathOnly(f)
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(block)
	}
	return b.String()
}

// renderFile renders one file as a full block, with the truncation note when
// the file was cut.
func renderFile(f File) string {
	text := f.Text
	if f.Truncated {
		text += "\n" + truncNote(len(f.Text), f.Size)
	}
	return contextBlock(f.Path, text)
}

// renderPathOnly renders a file that does not fit the injection budget: its
// text is only the truncation note.
func renderPathOnly(f File) string {
	return contextBlock(f.Path, truncNote(0, f.Size))
}

// contextBlock wraps text in the <project-context> markers.
func contextBlock(path, text string) string {
	return "<project-context path=\"" + path + "\">\n" + text + "\n</project-context>"
}

// truncNote is the stable truncation note (REQ-PCX-005).
func truncNote(shown, total int) string {
	return fmt.Sprintf("[truncated: %d of %d bytes; read the file for the rest]", shown, total)
}

// Section renders the project prompt section: the heading lines of the contract
// followed by the blocks of files. It returns "" for no files (REQ-PCX-002).
func Section(files []File) string {
	if len(files) == 0 {
		return ""
	}
	return HeaderLine + "\n" + IntroLine + "\n\n" + Render(files)
}

// Tracker is the per-session seen set of context files (REQ-PCX-004). It is
// safe for concurrent use: tools run in parallel and the check-and-mark of one
// file is atomic, so two parallel calls never both show the same file.
type Tracker struct {
	mu        sync.Mutex
	names     []string
	home      string
	workspace string
	// seen holds cleaned, lowercased paths.
	seen map[string]bool
}

// NewTracker builds a tracker for one turn. seen is the set of paths already
// shown, for example from SeenFromEvents.
func NewTracker(names []string, home, workspace string, seen []string) *Tracker {
	t := &Tracker{
		names:     append([]string(nil), names...),
		home:      home,
		workspace: workspace,
		seen:      make(map[string]bool, len(seen)),
	}
	for _, path := range seen {
		t.seen[seenKey(path)] = true
	}
	return t
}

// MarkSeen adds paths to the seen set.
func (t *Tracker) MarkSeen(paths ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, path := range paths {
		t.seen[seenKey(path)] = true
	}
}

// seenKey normalizes a path for the seen set: cleaned and lowercased.
func seenKey(path string) string {
	return strings.ToLower(filepath.Clean(path))
}

// Note returns the text to append after a successful tool call, or "". Only
// read_file, write_file, edit_file, glob, grep (JSON field "path", the workspace
// for an empty glob or grep path) and bash whose first simple command is
// `cd <dir>` count (REQ-PCX-003). The text opens with NoteIntroLine followed by
// a blank line and the <project-context> blocks of Render. Files already seen
// are dropped and the rest are marked seen atomically. An empty names list
// always returns "".
func (t *Tracker) Note(tool string, input json.RawMessage) string {
	if len(t.names) == 0 {
		return ""
	}
	start := t.startDir(tool, input)
	if start == "" {
		return ""
	}
	files := Discover(start, t.home, t.names)
	if len(files) == 0 {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	fresh := make([]File, 0, len(files))
	for _, f := range files {
		if t.seen[seenKey(f.Path)] {
			continue
		}
		fresh = append(fresh, f)
	}
	if len(fresh) == 0 {
		return ""
	}
	for _, f := range fresh {
		t.seen[seenKey(f.Path)] = true
	}
	return NoteIntroLine + "\n\n" + Render(fresh)
}

// startDir returns the directory to walk for a tool call, or "" when the call
// does not touch a path.
func (t *Tracker) startDir(tool string, input json.RawMessage) string {
	switch tool {
	case "read_file", "write_file", "edit_file":
		return t.pathStart(toolField(input, "path"), false)
	case "glob", "grep":
		return t.pathStart(toolField(input, "path"), true)
	case "bash":
		return t.bashStart(toolField(input, "command"))
	}
	return ""
}

// toolField reads a string field of a tool input.
func toolField(input json.RawMessage, field string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(input, &m); err != nil {
		return ""
	}
	var value string
	if err := json.Unmarshal(m[field], &value); err != nil {
		return ""
	}
	return value
}

// pathStart resolves the start directory of a path field. When the path is
// empty it falls back to the workspace only for glob and grep.
func (t *Tracker) pathStart(path string, workspaceDefault bool) string {
	if path == "" {
		if !workspaceDefault || t.workspace == "" {
			return ""
		}
		path = t.workspace
	}
	return t.resolveStart(path)
}

// resolveStart expands ~, makes the path absolute against the workspace, and
// returns the nearest existing directory at or above it.
func (t *Tracker) resolveStart(path string) string {
	path = t.expandHome(path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(t.workspace, path)
	}
	return nearestDir(filepath.Clean(path))
}

// expandHome replaces a leading ~ or ~/ with home.
func (t *Tracker) expandHome(path string) string {
	if t.home == "" {
		return path
	}
	if path == "~" {
		return t.home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(t.home, path[2:])
	}
	return path
}

// nearestDir returns path when it is a directory, the parent of a file path,
// and the nearest existing ancestor of a path that does not exist yet.
func nearestDir(path string) string {
	for {
		info, err := os.Stat(path)
		if err == nil {
			if info.IsDir() {
				return path
			}
			return filepath.Dir(path)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		path = parent
	}
}

// bashStart returns the start directory of a command whose first simple command
// is `cd <dir>`, or "".
func (t *Tracker) bashStart(command string) string {
	segments, _ := pattern.SplitCommand(command)
	if len(segments) == 0 {
		return ""
	}
	dir, ok := cdTarget(segments[0])
	if !ok {
		return ""
	}
	return t.resolveStart(dir)
}

// cdTarget returns the directory of a `cd <dir>` simple command.
func cdTarget(segment string) (string, bool) {
	rest, ok := strings.CutPrefix(segment, "cd ")
	if !ok {
		return "", false
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", false
	}
	return unquote(rest), true
}

// unquote removes one matching pair of single or double quotes.
func unquote(s string) string {
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if first == last && (first == '"' || first == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// contextPathRE matches the path of a <project-context> marker.
var contextPathRE = regexp.MustCompile(`<project-context path="([^"]*)"`)

// SeenFromEvents rebuilds the seen set from a session chain (REQ-PCX-004): the
// paths in the project section of the head system message when it comes after
// the latest compaction, and every <project-context> marker in tool result
// output after that compaction. It returns unique paths in first-seen order and
// ignores events that fail to unmarshal.
func SeenFromEvents(events []core.SessionEvent) []string {
	compaction := -1
	for i := range events {
		if events[i].Kind == core.KindCompactionSummary {
			compaction = i
		}
	}
	// The head prompt is the latest system message that is not a note or a
	// runtime meta.
	head := -1
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind != core.KindSystemMessage {
			continue
		}
		var payload core.SystemMessage
		if err := json.Unmarshal(events[i].Payload, &payload); err != nil {
			continue
		}
		if payload.Subtype != "" {
			continue
		}
		head = i
		break
	}

	var out []string
	seen := map[string]bool{}
	add := func(text string) {
		for _, m := range contextPathRE.FindAllStringSubmatch(text, -1) {
			path := m[1]
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			out = append(out, path)
		}
	}
	for i := compaction + 1; i < len(events); i++ {
		switch events[i].Kind {
		case core.KindSystemMessage:
			if i != head {
				continue
			}
			var payload core.SystemMessage
			if err := json.Unmarshal(events[i].Payload, &payload); err != nil {
				continue
			}
			add(payload.Text)
		case core.KindToolResult:
			var payload core.ToolResultPayload
			if err := json.Unmarshal(events[i].Payload, &payload); err != nil {
				continue
			}
			add(payload.Output)
		}
	}
	return out
}
