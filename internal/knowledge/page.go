package knowledge

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/ridhomujizat/lana-harness/internal/secretlike" // keep secret checks near page writes
)

// Page is one markdown page of the vault. Path is vault-relative with forward
// slashes, for example "wiki/Pak Budi.md".
type Page struct {
	Path  string
	Title string
	Meta  PageMeta
	Body  string
	Raw   string
}

// PageMeta is the frontmatter of a page (REQ-KNW-003). Extra keeps unknown keys
// so a meta update preserves them.
type PageMeta struct {
	Type        string         `yaml:"type"`
	Description string         `yaml:"description"`
	Aliases     []string       `yaml:"aliases"`
	Tags        []string       `yaml:"tags"`
	Sources     []string       `yaml:"sources"`
	Created     string         `yaml:"created"`
	Updated     string         `yaml:"updated"`
	Extra       map[string]any `yaml:"-"`
}

// titleMax is the longest allowed page title in runes (REQ-KNW-013).
const titleMax = 120

// forbiddenTitleChars must not appear in a title (REQ-KNW-013).
const forbiddenTitleChars = `/\:*?"<>|#^[]`

// titleSpaceRuns and titleDashRuns collapse the runs CleanTitle may produce
// (REQ-KBG-051).
var (
	titleSpaceRuns = regexp.MustCompile(`\s+`)
	titleDashRuns  = regexp.MustCompile(`-+`)
)

var datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// CleanTitle turns a model-supplied title into a legal page title
// (REQ-KBG-051): every forbidden character becomes "-", runs of whitespace and
// of "-" collapse to one space and one "-", and the result keeps no outer space
// or "-". "UI SDK (vercel-labs/ui)" becomes "UI SDK (vercel-labs-ui)".
func CleanTitle(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(forbiddenTitleChars, r) {
			return '-'
		}
		return r
	}, s)
	s = titleSpaceRuns.ReplaceAllString(s, " ")
	s = titleDashRuns.ReplaceAllString(s, "-")
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "-")
	return strings.TrimSpace(s)
}

// ParsePage splits path's text into frontmatter and body. A missing or invalid
// frontmatter block yields empty meta and the whole file as body, so
// hand-written pages with missing or extra fields still parse (REQ-KNW-003).
func ParsePage(path, raw string) Page {
	p := Page{Path: path, Title: titleOf(path), Raw: raw}
	meta, body, ok := splitFrontmatter(raw)
	if !ok {
		p.Body = raw
		return p
	}
	p.Meta = meta
	p.Body = body
	return p
}

// ValidTitle reports whether title is a legal page title (REQ-KNW-013).
func ValidTitle(title string) error {
	if utf8.RuneCountInString(title) == 0 {
		return errors.New("title is required")
	}
	if utf8.RuneCountInString(title) > titleMax {
		return fmt.Errorf("title is longer than %d characters", titleMax)
	}
	if strings.HasPrefix(title, ".") {
		return errors.New("title must not start with a dot")
	}
	if strings.ContainsAny(title, forbiddenTitleChars) {
		return fmt.Errorf("title must not contain any of %s", forbiddenTitleChars)
	}
	return nil
}

// FindPage matches title against the shared wiki pages (REQ-KNW-011).
func (v *Vault) FindPage(title string) (Page, bool, error) {
	return v.FindPageIn(ScopeShared, title)
}

// FindPageIn matches title against page file names under the wiki folder of
// scope and against frontmatter aliases, both case-insensitive (REQ-KNW-011,
// REQ-DKN-002). The second return value reports whether a page was found.
func (v *Vault) FindPageIn(scope, title string) (Page, bool, error) {
	target := strings.ToLower(strings.TrimSpace(title))
	if target == "" {
		return Page{}, false, nil
	}
	pages, err := v.wikiPages(scope)
	if err != nil {
		return Page{}, false, err
	}
	for _, p := range pages {
		if strings.ToLower(p.Title) == target {
			return p, true, nil
		}
	}
	for _, p := range pages {
		for _, alias := range p.Meta.Aliases {
			if strings.ToLower(strings.TrimSpace(alias)) == target {
				return p, true, nil
			}
		}
	}
	return Page{}, false, nil
}

// FormatPage renders meta and body as page text: a frontmatter block in the
// order type, description, aliases, tags, sources, created, updated, then the
// Extra keys sorted. Empty description, aliases, tags, and sources are
// omitted. Output ends with a newline.
func FormatPage(meta PageMeta, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "type: %s\n", yamlScalar(meta.Type))
	if meta.Description != "" {
		fmt.Fprintf(&b, "description: %s\n", yamlScalar(meta.Description))
	}
	writeList(&b, "aliases", meta.Aliases)
	writeList(&b, "tags", meta.Tags)
	writeList(&b, "sources", meta.Sources)
	fmt.Fprintf(&b, "created: %s\n", yamlScalar(meta.Created))
	fmt.Fprintf(&b, "updated: %s\n", yamlScalar(meta.Updated))
	keys := make([]string, 0, len(meta.Extra))
	for k := range meta.Extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		data, err := yaml.Marshal(map[string]any{k: meta.Extra[k]})
		if err != nil {
			continue
		}
		b.Write(data)
	}
	b.WriteString("---\n")
	if body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// splitFrontmatter returns the meta and body of raw. It reports false when raw
// has no leading --- block or the block is not valid YAML.
func splitFrontmatter(raw string) (PageMeta, string, bool) {
	text := strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t") != "---" {
		return PageMeta{}, "", false
	}
	close := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == "---" {
			close = i
			break
		}
	}
	if close < 0 {
		return PageMeta{}, "", false
	}
	meta, ok := parseMeta(strings.Join(lines[1:close], "\n"))
	if !ok {
		return PageMeta{}, "", false
	}
	body := strings.Join(lines[close+1:], "\n")
	body = strings.TrimPrefix(body, "\n")
	return meta, body, true
}

// parseMeta reads a frontmatter block into PageMeta. Unknown keys go to Extra.
func parseMeta(header string) (PageMeta, bool) {
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(header), &raw); err != nil {
		return PageMeta{}, false
	}
	meta := PageMeta{Extra: map[string]any{}}
	for k, val := range raw {
		switch k {
		case "type":
			meta.Type = asString(val)
		case "description":
			meta.Description = asString(val)
		case "aliases":
			list, ok := asStrings(val)
			if !ok {
				return PageMeta{}, false
			}
			meta.Aliases = list
		case "tags":
			list, ok := asStrings(val)
			if !ok {
				return PageMeta{}, false
			}
			meta.Tags = list
		case "sources":
			list, ok := asStrings(val)
			if !ok {
				return PageMeta{}, false
			}
			meta.Sources = list
		case "created":
			meta.Created = asString(val)
		case "updated":
			meta.Updated = asString(val)
		default:
			meta.Extra[k] = val
		}
	}
	return meta, true
}

// asString renders a scalar frontmatter value.
func asString(v any) string {
	switch s := v.(type) {
	case nil:
		return ""
	case string:
		return s
	case time.Time:
		return s.Format("2006-01-02")
	default:
		return fmt.Sprint(v)
	}
}

// asStrings reads a frontmatter list, tolerating a single string as one item.
func asStrings(v any) ([]string, bool) {
	switch s := v.(type) {
	case nil:
		return nil, true
	case string:
		if strings.TrimSpace(s) == "" {
			return nil, true
		}
		return []string{s}, true
	case []any:
		out := make([]string, 0, len(s))
		for _, item := range s {
			switch item.(type) {
			case nil, string, time.Time, int, int64, float64, bool:
				out = append(out, asString(item))
			default:
				return nil, false
			}
		}
		return out, true
	default:
		return nil, false
	}
}

// writeList writes a YAML list key, omitting it when values is empty.
func writeList(b *strings.Builder, key string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(b, "%s:\n", key)
	for _, v := range values {
		fmt.Fprintf(b, "  - %s\n", yamlScalar(v))
	}
}

// yamlScalar renders a string as a YAML value: a plain date is kept bare, and
// anything else goes through the yaml encoder for safe quoting.
func yamlScalar(s string) string {
	if datePattern.MatchString(s) {
		return s
	}
	out, err := yaml.Marshal(s)
	if err != nil {
		return strconv.Quote(s)
	}
	return strings.TrimRight(string(out), "\n")
}

// titleOf returns the page title of a vault-relative path.
func titleOf(path string) string {
	base := path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	return strings.TrimSuffix(base, ".md")
}

// secretMismatch reports whether any of the given fields looks like a secret.
func secretMismatch(fields ...string) bool {
	for _, f := range fields {
		if secretlike.Match(f) {
			return true
		}
	}
	return false
}
