package knowledge

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// knowledgeTypes are the page types a create and a meta Type must use
// (REQ-KNW-003).
var knowledgeTypes = []string{"person", "project", "organization", "place", "topic", "source"}

// logVerbs maps an effective action to its log bullet verb (REQ-KNW-004).
var logVerbs = map[string]string{
	"create":  "created",
	"append":  "appended to",
	"replace": "replaced text in",
	"meta":    "updated details of",
}

// Op is one page change. Action is one of create, append, replace, meta.
// Sources are wikilink strings such as [[sources/2026-11-01 Rapat.pdf]].
type Op struct {
	Action      string
	Title       string
	Type        string
	Description string
	Body        string
	Text        string
	Heading     string
	OldText     string
	NewText     string
	Aliases     []string
	Tags        []string
	Sources     []string
}

// Result describes an applied change. Op is the effective action taken (create
// or append when a create hit an existing page), and Size is the rune count of
// the written file.
type Result struct {
	Path string
	Op   string
	Size int
}

// Apply applies op to the vault under the lock and, on success, regenerates
// index.md and appends to log.md (REQ-KNW-010).
func (v *Vault) Apply(op Op, actor string) (Result, error) {
	if err := ValidTitle(op.Title); err != nil {
		return Result{}, err
	}
	if !validAction(op.Action) {
		return Result{}, fmt.Errorf("unknown action %q", op.Action)
	}
	if secretMismatch(op.Body, op.Text, op.NewText, op.Description) {
		return Result{}, errors.New("content looks like a secret; not saved")
	}

	unlock, err := v.lock()
	if err != nil {
		return Result{}, err
	}
	defer unlock()

	rel, action, op, err := v.resolveTarget(op)
	if err != nil {
		return Result{}, err
	}
	text, err := v.commit(filepath.Join(v.root, filepath.FromSlash(rel)), rel, action, op)
	if err != nil {
		return Result{}, err
	}
	if n := utf8.RuneCountInString(text); n > maxPageRunes {
		return Result{}, fmt.Errorf("page would be %d characters, over the %d limit; split the page", n, maxPageRunes)
	}
	if err := writeBytes(filepath.Join(v.root, filepath.FromSlash(rel)), []byte(text)); err != nil {
		return Result{}, err
	}
	if err := v.regenerateIndex(); err != nil {
		return Result{}, err
	}
	if err := v.appendLog(actor, action, rel, op); err != nil {
		return Result{}, err
	}
	return Result{Path: rel, Op: action, Size: utf8.RuneCountInString(text)}, nil
}

// resolveTarget returns the page path and effective action of op. A create
// whose title or alias names an existing page appends its body instead
// (REQ-KNW-011).
func (v *Vault) resolveTarget(op Op) (string, string, Op, error) {
	page, found, err := v.FindPage(op.Title)
	if err != nil {
		return "", "", op, err
	}
	if op.Action == "create" {
		asAppend := func() Op {
			out := op
			out.Text = op.Body
			out.Heading = ""
			return out
		}
		if found {
			return page.Path, "append", asAppend(), nil
		}
		if !validType(op.Type) {
			return "", "", op, fmt.Errorf("type %q must be one of %s", op.Type, strings.Join(knowledgeTypes, ", "))
		}
		rel := path.Join(wikiDir, op.Title+".md")
		_, statErr := os.Stat(filepath.Join(v.root, filepath.FromSlash(rel)))
		switch {
		case statErr == nil:
			return rel, "append", asAppend(), nil
		case errors.Is(statErr, fs.ErrNotExist):
			return rel, "create", op, nil
		default:
			return "", "", op, statErr
		}
	}
	if !found {
		return "", "", op, fmt.Errorf("page not found: %s", op.Title)
	}
	return page.Path, op.Action, op, nil
}

// commit renders op against the page on disk, re-reading and recomputing once
// when the file changed under it (REQ-KNW-010).
func (v *Vault) commit(abs, rel, action string, op Op) (string, error) {
	today := v.now().Format("2006-01-02")
	var (
		page   Page
		mtime  time.Time
		exists bool
	)
	load := func() error {
		data, err := os.ReadFile(abs)
		if errors.Is(err, fs.ErrNotExist) {
			page, mtime, exists = Page{Path: rel, Title: op.Title}, time.Time{}, false
			return nil
		}
		if err != nil {
			return err
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return err
		}
		page, mtime, exists = ParsePage(rel, string(data)), fi.ModTime(), true
		return nil
	}
	render := func() (string, error) {
		if action == "create" && exists {
			// The page appeared on disk under us: append the body instead of
			// overwriting what is there.
			out := op
			out.Text = op.Body
			out.Heading = ""
			return renderPage(page, "append", out, today)
		}
		return renderPage(page, action, op, today)
	}

	if err := load(); err != nil {
		return "", err
	}
	text, err := render()
	if err != nil {
		return "", err
	}
	for attempt := 0; ; attempt++ {
		if v.beforeCommit != nil {
			v.beforeCommit(abs)
		}
		fi, statErr := os.Stat(abs)
		nowExists := statErr == nil
		var changed bool
		switch {
		case statErr != nil && !errors.Is(statErr, fs.ErrNotExist):
			return "", statErr
		case nowExists != exists:
			changed = true
		case nowExists && !fi.ModTime().Equal(mtime):
			changed = true
		}
		if !changed {
			break
		}
		if attempt >= 1 {
			return "", errors.New("page changed on disk while writing")
		}
		if err := load(); err != nil {
			return "", err
		}
		if text, err = render(); err != nil {
			return "", err
		}
	}
	return text, nil
}

// renderPage returns the file text after applying one effective action.
func renderPage(page Page, action string, op Op, today string) (string, error) {
	meta := page.Meta
	if meta.Extra == nil {
		meta.Extra = map[string]any{}
	}
	body := page.Body
	switch action {
	case "create":
		meta = PageMeta{
			Type:        op.Type,
			Description: op.Description,
			Aliases:     append([]string(nil), op.Aliases...),
			Tags:        append([]string(nil), op.Tags...),
			Sources:     append([]string(nil), op.Sources...),
			Created:     today,
			Updated:     today,
			Extra:       map[string]any{},
		}
		body = strings.TrimRight(op.Body, "\n")
	case "append":
		body = appendText(body, op.Heading, op.Text)
	case "replace":
		next, err := replaceText(body, page.Title, op.OldText, op.NewText)
		if err != nil {
			return "", err
		}
		body = next
	case "meta":
		if op.Type != "" {
			if !validType(op.Type) {
				return "", fmt.Errorf("type %q must be one of %s", op.Type, strings.Join(knowledgeTypes, ", "))
			}
			meta.Type = op.Type
		}
		if op.Description != "" {
			meta.Description = op.Description
		}
	default:
		return "", fmt.Errorf("unknown action %q", action)
	}
	meta.Aliases = mergeStrings(meta.Aliases, op.Aliases, true)
	meta.Tags = mergeStrings(meta.Tags, op.Tags, false)
	meta.Sources = mergeStrings(meta.Sources, op.Sources, false)
	meta.Updated = today
	if meta.Created == "" {
		meta.Created = today
	}
	return FormatPage(meta, body), nil
}

// appendText adds text at the end of body, or at the end of the section named
// by heading, creating that section when it is missing (REQ-KNW-010).
func appendText(body, heading, text string) string {
	text = strings.TrimRight(text, "\n")
	// A model may send the heading with its markdown hashes ("## Peran").
	heading = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(heading), "#"))
	if heading == "" {
		return joinBlocks(body, text)
	}
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	want := strings.ToLower(strings.TrimSpace(heading))
	start, level := -1, 0
	for i, line := range lines {
		lvl, title, ok := parseHeading(line)
		if !ok || lvl < 2 || lvl > 4 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(title), want) {
			start, level = i, lvl
			break
		}
	}
	if start < 0 {
		return joinBlocks(body, "## "+heading+"\n\n"+text)
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if lvl, _, ok := parseHeading(lines[i]); ok && lvl <= level {
			end = i
			break
		}
	}
	section := append([]string(nil), lines[start+1:end]...)
	for len(section) > 0 && strings.TrimSpace(section[0]) == "" {
		section = section[1:]
	}
	for len(section) > 0 && strings.TrimSpace(section[len(section)-1]) == "" {
		section = section[:len(section)-1]
	}
	out := append([]string(nil), lines[:start+1]...)
	out = append(out, section...)
	if len(section) > 0 {
		out = append(out, "")
	}
	out = append(out, strings.Split(text, "\n")...)
	if end < len(lines) {
		out = append(out, "")
		out = append(out, lines[end:]...)
	}
	return strings.Join(out, "\n")
}

// joinBlocks joins body and add with one blank line between them.
func joinBlocks(body, add string) string {
	if strings.TrimSpace(body) == "" {
		return add
	}
	return strings.TrimRight(body, "\n") + "\n\n" + add
}

// replaceText replaces old once when it occurs exactly once in body
// (REQ-KNW-010).
func replaceText(body, title, old, replacement string) (string, error) {
	if old == "" {
		return "", errors.New("old_text is required")
	}
	switch n := strings.Count(body, old); {
	case n == 0:
		return "", fmt.Errorf("old_text not found in %s", title)
	case n > 1:
		return "", fmt.Errorf("old_text matches %d places in %s; use more specific text", n, title)
	}
	return strings.Replace(body, old, replacement, 1), nil
}

// parseHeading returns the level and text of a markdown heading line.
func parseHeading(line string) (int, string, bool) {
	i := 0
	for i < len(line) && line[i] == '#' {
		i++
	}
	if i < 1 || i > 6 || i >= len(line) || line[i] != ' ' {
		return 0, "", false
	}
	return i, line[i+1:], true
}

// mergeStrings merges add into existing, keeping order and dropping duplicates.
// When ci is true, duplicates are detected case-insensitively.
func mergeStrings(existing, add []string, ci bool) []string {
	out := make([]string, 0, len(existing)+len(add))
	seen := map[string]bool{}
	key := func(s string) string {
		if ci {
			return strings.ToLower(s)
		}
		return s
	}
	for _, s := range existing {
		if s == "" || seen[key(s)] {
			continue
		}
		seen[key(s)] = true
		out = append(out, s)
	}
	for _, s := range add {
		s = strings.TrimSpace(s)
		if s == "" || seen[key(s)] {
			continue
		}
		seen[key(s)] = true
		out = append(out, s)
	}
	return out
}

// validAction reports whether action is a known page action.
func validAction(action string) bool {
	switch action {
	case "create", "append", "replace", "meta":
		return true
	}
	return false
}

// validType reports whether typ is one of the known page types.
func validType(typ string) bool {
	for _, t := range knowledgeTypes {
		if typ == t {
			return true
		}
	}
	return false
}

// renderIndex builds the index.md catalog from pages (REQ-KNW-004).
func renderIndex(pages []Page) string {
	var b strings.Builder
	b.WriteString(indexHeader)
	for _, section := range indexSections {
		var entries []Page
		for _, p := range pages {
			if section.typ == "" {
				if validType(p.Meta.Type) {
					continue
				}
			} else if p.Meta.Type != section.typ {
				continue
			}
			entries = append(entries, p)
		}
		if len(entries) == 0 {
			continue
		}
		sort.SliceStable(entries, func(i, j int) bool {
			return strings.ToLower(entries[i].Title) < strings.ToLower(entries[j].Title)
		})
		fmt.Fprintf(&b, "\n## %s\n", section.heading)
		for _, p := range entries {
			if p.Meta.Description != "" {
				fmt.Fprintf(&b, "- [[%s]] — %s\n", p.Title, p.Meta.Description)
			} else {
				fmt.Fprintf(&b, "- [[%s]]\n", p.Title)
			}
		}
	}
	return b.String()
}

// indexSections is the fixed order of the index, with the empty type standing
// for pages with a missing or unknown type (REQ-KNW-004).
var indexSections = []struct {
	heading string
	typ     string
}{
	{"People", "person"},
	{"Projects", "project"},
	{"Organizations", "organization"},
	{"Places", "place"},
	{"Topics", "topic"},
	{"Sources", "source"},
	{"Other", ""},
}

// appendLog adds one entry to log.md without rewriting the file (REQ-KNW-004).
func (v *Vault) appendLog(actor, action, rel string, op Op) error {
	filePath := filepath.Join(v.root, logFile)
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	prefix := ""
	if fi, err := f.Stat(); err == nil && fi.Size() > 0 {
		prefix = "\n"
	}
	_, err = f.WriteString(prefix + logEntry(v.now(), actor, action, rel, op))
	return err
}

// logEntry renders one log block.
func logEntry(now time.Time, actor, action, rel string, op Op) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s %s\n", now.Format("2006-01-02 15:04"), actor)
	verb := logVerbs[action]
	if verb == "" {
		verb = action
	}
	fmt.Fprintf(&b, "- %s [[%s]]\n", verb, titleOf(rel))
	for _, s := range op.Sources {
		fmt.Fprintf(&b, "- from %s\n", s)
	}
	return b.String()
}
