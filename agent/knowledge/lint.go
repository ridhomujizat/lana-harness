package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/ridhomujizat/lana-harness/core"
	kv "github.com/ridhomujizat/lana-harness/internal/knowledge"
)

// Lint bounds (design §8).
const (
	lintReportDir      = "reports"
	lintMaxLLMPages    = 20
	lintMaxLinkedPages = 3
	lintPageRunes      = 4000
	lintMaxTokens      = 8192
	lintCallTimeout    = 90 * time.Second
	lintMaxItems       = 5
)

// lintLinkPattern matches a [[Title]] wikilink, capturing the target without an
// anchor or display text (design §8).
var lintLinkPattern = regexp.MustCompile(`\[\[([^\]|#]+)(#[^\]|]*)?(\|[^\]]*)?\]\]`)

// lintSystem is the system prompt of one contradiction check (design §8).
const lintSystem = `You check a personal knowledge wiki for contradictions. You receive one page and up to three pages it links to. Report only clear contradictions: a fact, date, number, or decision that cannot both be true.
Reply with only a JSON array, at most 5 items: [{"page": "...", "claim": "...", "contradicts": "...", "where": "Title or section"}], or [] when there is none.`

// lintContradiction is one item of a contradiction check reply (design §8).
type lintContradiction struct {
	Page        string `json:"page"`
	Claim       string `json:"claim"`
	Contradicts string `json:"contradicts"`
	Where       string `json:"where"`
}

// lintPage is one wiki page with its file mtime and extracted link targets.
type lintPage struct {
	page  kv.Page
	mtime time.Time
	links []string
}

// lintLookup resolves link targets and inbound links across the pages.
type lintLookup struct {
	root     string
	pages    []lintPage
	titles   map[string]bool
	aliases  map[string]bool
	resolved map[string]int
}

// lint runs the deterministic checks and the contradiction pass and writes
// reports/lint-YYYY-MM-DD.md (REQ-KNU-022, design §8). It never writes under
// wiki/ (REQ-KNU-023).
func (s *Service) lint(ctx context.Context) (string, error) {
	vault, _, err := s.open()
	if err != nil {
		return "", err
	}
	pages, err := readWikiPages(vault.Root())
	if err != nil {
		return "", err
	}
	lookup := newLintLookup(vault.Root(), pages)

	report := &lintReport{date: s.now().Format("2006-01-02"), count: len(pages), items: pages}
	report.checkBrokenLinks(lookup)
	report.checkOrphans(lookup)
	report.checkDuplicates(lookup)
	report.checkMissing()
	report.checkConflicts()
	report.contradictions = s.lintContradictions(ctx, lookup)

	return s.writeLintReport(vault.Root(), report.date, report.render())
}

// readWikiPages reads and parses every markdown page under the shared wiki and
// each domain wiki, sorted by path (design §4.4, §8).
func readWikiPages(root string) ([]lintPage, error) {
	dirs := []string{filepath.Join(root, "wiki")}
	for _, name := range domainFolders(root) {
		dirs = append(dirs, filepath.Join(root, "agents", name, "wiki"))
	}
	var out []lintPage
	for _, dir := range dirs {
		pages, err := readWikiDir(root, dir)
		if err != nil {
			return nil, err
		}
		out = append(out, pages...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].page.Path < out[j].page.Path })
	return out, nil
}

// readWikiDir reads and parses every markdown page under one wiki folder,
// sorted by path.
func readWikiDir(root, dir string) ([]lintPage, error) {
	var out []lintPage
	err := filepath.WalkDir(dir, func(abs string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			if abs != dir && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || !d.Type().IsRegular() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(abs), ".md") {
			return nil
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		out = append(out, lintPage{
			page:  kv.ParsePage(rel, string(data)),
			mtime: info.ModTime(),
			links: pageLinkTargets(string(data)),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].page.Path < out[j].page.Path })
	return out, nil
}

// pageLinkTargets returns the link targets of a page, without anchor or display
// text (design §8).
func pageLinkTargets(raw string) []string {
	var out []string
	for _, match := range lintLinkPattern.FindAllStringSubmatch(raw, -1) {
		if target := strings.TrimSpace(match[1]); target != "" {
			out = append(out, target)
		}
	}
	return out
}

// newLintLookup builds the title, alias, and link-resolution tables.
func newLintLookup(root string, pages []lintPage) *lintLookup {
	lookup := &lintLookup{
		root:     root,
		pages:    pages,
		titles:   map[string]bool{},
		aliases:  map[string]bool{},
		resolved: map[string]int{},
	}
	for i, p := range pages {
		keys := append([]string{p.page.Title}, p.page.Meta.Aliases...)
		for _, key := range keys {
			key = strings.ToLower(strings.TrimSpace(key))
			if key == "" {
				continue
			}
			if _, exists := lookup.resolved[key]; !exists {
				lookup.resolved[key] = i
			}
		}
		lookup.titles[strings.ToLower(strings.TrimSpace(p.page.Title))] = true
		for _, alias := range p.page.Meta.Aliases {
			lookup.aliases[strings.ToLower(strings.TrimSpace(alias))] = true
		}
	}
	return lookup
}

// resolvePage returns the index of the page a link target names, by title or
// alias, case-insensitively (design §8).
func (l *lintLookup) resolvePage(target string) (int, bool) {
	i, ok := l.resolved[strings.ToLower(strings.TrimSpace(target))]
	return i, ok
}

// targetValid reports whether a link target names a page title or alias or an
// existing vault-relative file, with or without .md (design §8).
func (l *lintLookup) targetValid(target string) bool {
	key := strings.ToLower(strings.TrimSpace(target))
	if key == "" {
		return true
	}
	if l.titles[key] || l.aliases[key] {
		return true
	}
	for _, candidate := range []string{target, target + ".md"} {
		if _, err := os.Stat(filepath.Join(l.root, filepath.FromSlash(candidate))); err == nil {
			return true
		}
	}
	return false
}

// lintReport is the accumulated result of one lint run (design §8).
type lintReport struct {
	date           string
	count          int
	items          []lintPage
	contradictions []string
	conflicts      []string
	duplicates     []string
	broken         []string
	orphans        []string
	missing        []string
}

// checkBrokenLinks lists links whose target names no page and no file
// (design §8).
func (r *lintReport) checkBrokenLinks(l *lintLookup) {
	seen := map[string]bool{}
	for _, p := range l.pages {
		for _, target := range p.links {
			if l.targetValid(target) {
				continue
			}
			line := fmt.Sprintf("- %s → [[%s]]", pageLabel(p), target)
			if seen[line] {
				continue
			}
			seen[line] = true
			r.broken = append(r.broken, line)
		}
	}
}

// checkOrphans lists pages other than sources that no other page links to
// (design §8).
func (r *lintReport) checkOrphans(l *lintLookup) {
	inbound := map[string]bool{}
	for _, p := range l.pages {
		for _, target := range p.links {
			i, ok := l.resolvePage(target)
			if !ok {
				continue
			}
			if l.pages[i].page.Path != p.page.Path {
				inbound[l.pages[i].page.Path] = true
			}
		}
	}
	for _, p := range l.pages {
		if strings.EqualFold(p.page.Meta.Type, "source") {
			continue
		}
		if !inbound[p.page.Path] {
			r.orphans = append(r.orphans, "- "+pageLabel(p))
		}
	}
}

// checkDuplicates lists pairs of pages in the same scope whose folded titles or
// aliases are equal (design §4.4, §8). A title may live in two scopes without
// being a duplicate.
func (r *lintReport) checkDuplicates(l *lintLookup) {
	groups := map[string][]int{}
	for i, p := range l.pages {
		scope := kv.ScopeOf(p.page.Path)
		keys := append([]string{p.page.Title}, p.page.Meta.Aliases...)
		for _, key := range keys {
			folded := foldKey(key)
			if folded == "" {
				continue
			}
			group := scope + "\x00" + folded
			groups[group] = appendUniqueIndex(groups[group], i)
		}
	}
	folded := make([]string, 0, len(groups))
	for key := range groups {
		folded = append(folded, key)
	}
	sort.Strings(folded)

	seen := map[string]bool{}
	for _, key := range folded {
		members := groups[key]
		for a := 0; a < len(members); a++ {
			for b := a + 1; b < len(members); b++ {
				i, j := members[a], members[b]
				pair := fmt.Sprintf("%d|%d", i, j)
				if seen[pair] {
					continue
				}
				seen[pair] = true
				r.duplicates = append(r.duplicates, fmt.Sprintf("- %s and %s", pageLabel(l.pages[i]), pageLabel(l.pages[j])))
			}
		}
	}
}

// checkMissing lists pages without a type or description (design §8).
func (r *lintReport) checkMissing() {
	for _, p := range r.items {
		if strings.TrimSpace(p.page.Meta.Type) == "" {
			r.missing = append(r.missing, fmt.Sprintf("- %s: no type", pageLabel(p)))
		}
		if strings.TrimSpace(p.page.Meta.Description) == "" {
			r.missing = append(r.missing, fmt.Sprintf("- %s: no description", pageLabel(p)))
		}
	}
}

// checkConflicts lists lines that carry a **Conflict:** marker with their page
// and line number (design §8).
func (r *lintReport) checkConflicts() {
	for _, p := range r.items {
		for i, line := range strings.Split(strings.ReplaceAll(p.page.Raw, "\r\n", "\n"), "\n") {
			if strings.Contains(line, "**Conflict:**") {
				r.conflicts = append(r.conflicts, fmt.Sprintf("- %s line %d: %s", pageLabel(p), i+1, strings.TrimSpace(line)))
			}
		}
	}
}

// lintContradictions asks the knowledge agent's model about pages changed since
// the last consolidation, at most lintMaxLLMPages newest first, one request per
// page with up to lintMaxLinkedPages pages it links (design §8). A page whose
// request fails is skipped.
func (s *Service) lintContradictions(ctx context.Context, l *lintLookup) []string {
	agentName, _ := knowledgeAgent(s.Config)
	if agentName == "" {
		return nil
	}
	agentCfg, ok := s.lookupAgent(agentName)
	if !ok {
		return nil
	}
	call, err := s.agentCall(agentName, agentCfg)
	if err != nil {
		return nil
	}

	since := int64(0)
	if last, ok := s.lastConsolidation(); ok {
		since = last.UnixMilli()
	}
	changed := l.changedSince(since)
	if len(changed) > lintMaxLLMPages {
		changed = changed[:lintMaxLLMPages]
	}

	var out []string
	for _, p := range changed {
		if err := ctx.Err(); err != nil {
			break
		}
		items, err := s.lintPageContradictions(ctx, call, l, p)
		if err != nil {
			slog.Debug("knowledge lint: contradiction pass skipped page", "page", p.page.Path, "error", err)
			continue
		}
		for _, item := range items {
			out = append(out, formatContradiction(p, item))
		}
	}
	return out
}

// changedSince returns the pages whose mtime is after sinceMS, newest first
// (design §8).
func (l *lintLookup) changedSince(sinceMS int64) []lintPage {
	var out []lintPage
	for _, p := range l.pages {
		if p.mtime.UnixMilli() > sinceMS {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].mtime.After(out[j].mtime) })
	return out
}

// lintPageContradictions makes the one provider call of a page and parses its
// reply (design §8).
func (s *Service) lintPageContradictions(ctx context.Context, call agentCall, l *lintLookup, p lintPage) ([]lintContradiction, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n%s\n", p.page.Title, firstRunes(p.page.Body, lintPageRunes))
	linked := 0
	for _, target := range p.links {
		if linked >= lintMaxLinkedPages {
			break
		}
		i, ok := l.resolvePage(target)
		if !ok || l.pages[i].page.Path == p.page.Path {
			continue
		}
		other := l.pages[i]
		fmt.Fprintf(&b, "\n## %s\n%s\n", other.page.Title, firstRunes(other.page.Body, lintPageRunes))
		linked++
	}

	callCtx, cancel := context.WithTimeout(ctx, lintCallTimeout)
	defer cancel()
	stream, err := s.stream(callCtx, call, "", core.ChatRequest{
		System: lintSystem,
		Messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentBlock{
			{Kind: core.BlockText, Text: b.String()},
		}}},
		MaxTokens:       lintMaxTokens,
		ReasoningEffort: "off",
	})
	if err != nil {
		return nil, err
	}
	reply, _, err := collectReply(stream)
	if err != nil {
		return nil, err
	}
	return parseLintReply(reply)
}

// parseLintReply strips a fence and parses a contradiction reply, skipping an
// item whose shape does not decode and capping the rest at lintMaxItems
// (design §8).
func parseLintReply(text string) ([]lintContradiction, error) {
	trimmed := stripFence(strings.TrimSpace(text))
	if !strings.HasPrefix(trimmed, "[") {
		return nil, errors.New("lint reply is not a JSON array")
	}
	var raw []json.RawMessage
	if err := decodeLLMJSON(trimmed, &raw); err != nil {
		return nil, err
	}
	var items []lintContradiction
	for _, item := range raw {
		var c lintContradiction
		if err := json.Unmarshal(item, &c); err != nil {
			slog.Debug("knowledge lint: item skipped: malformed JSON", "error", err)
			continue
		}
		items = append(items, c)
	}
	if len(items) > lintMaxItems {
		items = items[:lintMaxItems]
	}
	return items, nil
}

// formatContradiction renders one contradiction as a report bullet
// (design §8).
func formatContradiction(p lintPage, item lintContradiction) string {
	title := strings.TrimSpace(item.Page)
	if title == "" {
		title = p.page.Title
	}
	line := fmt.Sprintf("- [[%s]]: %q contradicts %q", title, item.Claim, item.Contradicts)
	if where := strings.TrimSpace(item.Where); where != "" {
		line += " (" + where + ")"
	}
	return line
}

// render builds the report markdown: the header, then the non-empty sections in
// the design order, or "No problems found." (design §8).
func (r *lintReport) render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Knowledge lint %s\n\n", r.date)
	fmt.Fprintf(&b, "Pages: %d. Problems: %d.\n", r.count, r.problems())
	if r.problems() == 0 {
		b.WriteString("\nNo problems found.\n")
		return b.String()
	}
	writeLintSection(&b, "Contradictions", r.contradictions)
	writeLintSection(&b, "Marked conflicts", r.conflicts)
	writeLintSection(&b, "Likely duplicates", r.duplicates)
	writeLintSection(&b, "Broken links", r.broken)
	writeLintSection(&b, "Orphan pages", r.orphans)
	writeLintSection(&b, "Missing details", r.missing)
	return b.String()
}

// problems counts every problem bullet of the report (design §8).
func (r *lintReport) problems() int {
	return len(r.contradictions) + len(r.conflicts) + len(r.duplicates) + len(r.broken) + len(r.orphans) + len(r.missing)
}

// writeLintSection writes one section when it has lines (design §8).
func writeLintSection(b *strings.Builder, heading string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## %s\n", heading)
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

// writeLintReport writes reports/lint-YYYY-MM-DD.md, overwriting a same-day
// report (design §8).
func (s *Service) writeLintReport(root, date, markdown string) (string, error) {
	rel := filepath.ToSlash(filepath.Join(lintReportDir, "lint-"+date+".md"))
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, []byte(markdown), 0o600); err != nil {
		return "", err
	}
	return rel, nil
}

// pageLabel renders a page as a report wikilink, with its scope in parentheses
// when it is not shared: "[[Gaji]] (dana)" (design §4.4).
func pageLabel(p lintPage) string {
	label := "[[" + p.page.Title + "]]"
	if scope := kv.ScopeOf(p.page.Path); scope != kv.ScopeShared {
		label += " (" + scope + ")"
	}
	return label
}

// foldKey folds a title or alias for duplicate detection: accented and
// precomposed forms become their base letters, the text is lower-cased, and
// everything but letters and digits is dropped (design §8).
func foldKey(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// appendUniqueIndex appends i to list when it is not present.
func appendUniqueIndex(list []int, i int) []int {
	for _, existing := range list {
		if existing == i {
			return list
		}
	}
	return append(list, i)
}
